// Package store implements the continuum single-table persistence contract.
package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/model"
)

var (
	ErrNotFound           = errors.New("not found")
	ErrSubmissionConflict = errors.New("submission conflict")
	ErrEmailRegistered    = errors.New("email already registered")
)

// DynamoAPI is the subset of the AWS client used by Store. Keeping this
// interface small permits deterministic tests and avoids coupling business
// logic to a concrete SDK client.
type DynamoAPI interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	BatchWriteItem(context.Context, *dynamodb.BatchWriteItemInput, ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

// Store owns the DynamoDB table name and clock. Now is injectable to make
// timestamps and expiry behavior testable.
type Store struct {
	Client       DynamoAPI
	TableName    string
	Region       string
	HeartbeatTTL int
	Now          func() time.Time
}

// New constructs a Store using the default AWS credential/region chain.
func New(ctx context.Context, settings config.Settings) (*Store, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(settings.Region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	endpoint := strings.TrimSpace(settings.DDBEndpointMode)
	if endpoint != "" {
		endpoint = strings.ReplaceAll(endpoint, "{region}", settings.Region)
	}
	client := dynamodb.NewFromConfig(awsCfg, func(o *dynamodb.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
	return NewWithClient(client, settings), nil
}

func NewWithClient(client DynamoAPI, settings config.Settings) *Store {
	return &Store{Client: client, TableName: settings.TableName, Region: settings.Region,
		HeartbeatTTL: settings.HeartbeatTTLS, Now: time.Now}
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
func (s *Store) nowMS() int64 { return s.now().UnixMilli() }
func iso(t time.Time) string  { return t.UTC().Format(time.RFC3339Nano) }
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
func key(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk}}
}

func marshal(v any) (map[string]types.AttributeValue, error) { return attributevalue.MarshalMap(v) }
func unmarshal(item map[string]types.AttributeValue, out any) error {
	return attributevalue.UnmarshalMap(item, out)
}
func isConditional(err error) bool {
	var c *types.ConditionalCheckFailedException
	if errors.As(err, &c) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "conditionalcheckfailed")
}

func (s *Store) get(ctx context.Context, k map[string]types.AttributeValue) (map[string]types.AttributeValue, error) {
	out, err := s.Client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.TableName), Key: k})
	if err != nil {
		return nil, err
	}
	if len(out.Item) == 0 {
		return nil, nil
	}
	return out.Item, nil
}

// GetUser retrieves USER#id / PROFILE.
func (s *Store) GetUser(ctx context.Context, userID string) (*model.User, error) {
	item, err := s.get(ctx, key("USER#"+userID, "PROFILE"))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, nil
	}
	var user model.User
	if err := unmarshal(item, &user); err != nil {
		return nil, fmt.Errorf("decode user: %w", err)
	}
	return &user, nil
}

// FindUserIDByEmail retrieves USEREMAIL#normalised / LOGIN.
func (s *Store) FindUserIDByEmail(ctx context.Context, email string) (string, error) {
	item, err := s.get(ctx, key("USEREMAIL#"+strings.TrimSpace(strings.ToLower(email)), "LOGIN"))
	if err != nil {
		return "", err
	}
	if item == nil {
		return "", nil
	}
	var idx model.EmailIndex
	if err := unmarshal(item, &idx); err != nil {
		return "", fmt.Errorf("decode email index: %w", err)
	}
	return idx.UserID, nil
}

// CreateUser atomically creates the profile and unique email index.
func (s *Store) CreateUser(ctx context.Context, user *model.User, passwordHash string) error {
	profile := *user
	profile.PasswordHash = passwordHash
	if profile.PK == "" {
		profile.PK = "USER#" + profile.UserID
	}
	if profile.SK == "" {
		profile.SK = "PROFILE"
	}
	profileItem, err := marshal(profile)
	if err != nil {
		return err
	}
	idx, err := marshal(model.EmailIndex{PK: "USEREMAIL#" + profile.Email, SK: "LOGIN", UserID: profile.UserID, TTL: s.now().Unix() + 10*365*24*3600})
	if err != nil {
		return err
	}
	_, err = s.Client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: profileItem}},
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: idx, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	}})
	if err != nil && isTransactionConditional(err) {
		return ErrEmailRegistered
	}
	return err
}

func isTransactionConditional(err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(strings.ToLower(err.Error()), "conditionalcheckfailed") {
		return true
	}
	var e *types.TransactionCanceledException
	if errors.As(err, &e) {
		for _, reason := range e.CancellationReasons {
			if reason.Code != nil && *reason.Code == "ConditionalCheckFailed" {
				return true
			}
		}
	}
	return false
}

// EnsureExam writes EXAM#id/META and QUESTION#NNN items.
func (s *Store) EnsureExam(ctx context.Context, examID, title string, questions []model.Question, window map[string]any) error {
	meta := model.ExamMeta{PK: "EXAM#" + examID, SK: "META", Title: title, CreatedAt: iso(s.now()), QuestionCount: len(questions)}
	item, err := marshal(meta)
	if err != nil {
		return err
	}
	for field, value := range window {
		av, e := attributevalue.Marshal(value)
		if e != nil {
			return fmt.Errorf("marshal window %s: %w", field, e)
		}
		item[field] = av
	}
	if _, err := s.Client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.TableName), Item: item}); err != nil {
		return err
	}
	for i, question := range questions {
		qItem, err := marshal(struct {
			PK       string   `dynamodbav:"pk"`
			SK       string   `dynamodbav:"sk"`
			Question string   `dynamodbav:"question"`
			Options  []string `dynamodbav:"options"`
			Answer   int      `dynamodbav:"answer"`
		}{
			PK: "EXAM#" + examID, SK: fmt.Sprintf("QUESTION#%03d", i), Question: question.Question, Options: question.Options, Answer: question.Answer,
		})
		if err != nil {
			return err
		}
		if _, err = s.Client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.TableName), Item: qItem}); err != nil {
			return err
		}
	}
	return nil
}

// UpdateExamWindow patches only supplied mutable metadata and returns META.
func (s *Store) UpdateExamWindow(ctx context.Context, examID string, title *string, window map[string]any) (*model.ExamMeta, error) {
	parts := make([]string, 0, 4)
	names := map[string]string{}
	values := map[string]types.AttributeValue{}
	if title != nil {
		parts = append(parts, "#title = :title")
		names["#title"] = "title"
		values[":title"] = &types.AttributeValueMemberS{Value: *title}
	}
	for _, field := range []string{"opens_at", "closes_at", "duration_minutes"} {
		if value, ok := window[field]; ok {
			av, err := attributevalue.Marshal(value)
			if err != nil {
				return nil, err
			}
			alias := "#" + field
			parts = append(parts, alias+" = :"+field)
			names[alias] = field
			values[":"+field] = av
		}
	}
	if len(parts) == 0 {
		return s.GetExamMeta(ctx, examID)
	}
	out, err := s.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(s.TableName), Key: key("EXAM#"+examID, "META"), UpdateExpression: aws.String("SET " + strings.Join(parts, ", ")), ExpressionAttributeNames: names, ExpressionAttributeValues: values, ReturnValues: types.ReturnValueAllNew})
	if err != nil {
		return nil, err
	}
	var meta model.ExamMeta
	if err := unmarshal(out.Attributes, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func (s *Store) GetExamMeta(ctx context.Context, examID string) (*model.ExamMeta, error) {
	item, err := s.get(ctx, key("EXAM#"+examID, "META"))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, nil
	}
	var meta model.ExamMeta
	if err := unmarshal(item, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

type questionItem struct {
	PK       string   `dynamodbav:"pk"`
	SK       string   `dynamodbav:"sk"`
	Question string   `dynamodbav:"question"`
	Options  []string `dynamodbav:"options"`
	Answer   int      `dynamodbav:"answer"`
}

func (s *Store) queryAll(ctx context.Context, input *dynamodb.QueryInput) ([]map[string]types.AttributeValue, error) {
	var all []map[string]types.AttributeValue
	for {
		out, err := s.Client.Query(ctx, input)
		if err != nil {
			return nil, err
		}
		all = append(all, out.Items...)
		if len(out.LastEvaluatedKey) == 0 {
			return all, nil
		}
		input.ExclusiveStartKey = out.LastEvaluatedKey
	}
}

// GetExam returns questions sorted by their QUESTION# index. Answers are
// included only when includeAnswers is true.
func (s *Store) GetExam(ctx context.Context, examID string, includeAnswers bool) (*model.Exam, error) {
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{TableName: aws.String(s.TableName), KeyConditionExpression: aws.String("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "EXAM#" + examID}}})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	var exam model.Exam
	foundMeta := false
	for _, item := range items {
		var sk string
		if av, ok := item["sk"].(*types.AttributeValueMemberS); ok {
			sk = av.Value
		}
		if sk == "META" {
			if err := unmarshal(item, &exam.ExamMeta); err != nil {
				return nil, err
			}
			foundMeta = true
			continue
		}
		if strings.HasPrefix(sk, "QUESTION#") {
			var q questionItem
			if err := unmarshal(item, &q); err != nil {
				return nil, err
			}
			index, e := strconv.Atoi(strings.TrimPrefix(sk, "QUESTION#"))
			if e != nil {
				continue
			}
			exam.Questions = append(exam.Questions, model.Question{Index: index, Question: q.Question, Options: q.Options, Answer: q.Answer})
		}
	}
	if !foundMeta {
		return nil, nil
	}
	sort.Slice(exam.Questions, func(i, j int) bool { return exam.Questions[i].Index < exam.Questions[j].Index })
	exam.ExamMeta.ID = examID
	if !includeAnswers {
		for i := range exam.Questions {
			exam.Questions[i].Answer = 0
		}
	}
	return &exam, nil
}

func (s *Store) ListExams(ctx context.Context, limit int) ([]model.ExamMeta, error) {
	if limit <= 0 {
		limit = 50
	}
	out, err := s.Client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String(s.TableName), Limit: aws.Int32(int32(limit)), FilterExpression: aws.String("begins_with(#pk, :prefix) AND #sk = :meta"), ExpressionAttributeNames: map[string]string{"#pk": "pk", "#sk": "sk"}, ExpressionAttributeValues: map[string]types.AttributeValue{":prefix": &types.AttributeValueMemberS{Value: "EXAM#"}, ":meta": &types.AttributeValueMemberS{Value: "META"}}})
	if err != nil {
		return nil, err
	}
	result := make([]model.ExamMeta, 0, len(out.Items))
	for _, item := range out.Items {
		var m model.ExamMeta
		if err := unmarshal(item, &m); err != nil {
			return nil, err
		}
		m.ID = strings.TrimPrefix(m.PK, "EXAM#")
		result = append(result, m)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].CreatedAt < result[j].CreatedAt })
	return result, nil
}

func (s *Store) AnswerKey(ctx context.Context, examID string) (map[int]int, error) {
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{TableName: aws.String(s.TableName), KeyConditionExpression: aws.String("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "EXAM#" + examID}}})
	if err != nil {
		return nil, err
	}
	result := map[int]int{}
	for _, item := range items {
		av, ok := item["sk"].(*types.AttributeValueMemberS)
		if !ok || !strings.HasPrefix(av.Value, "QUESTION#") {
			continue
		}
		index, e := strconv.Atoi(strings.TrimPrefix(av.Value, "QUESTION#"))
		if e != nil {
			continue
		}
		var a int
		if err := attributevalue.Unmarshal(item["answer"], &a); err != nil {
			return nil, err
		}
		result[index] = a
	}
	return result, nil
}

func (s *Store) GradeAttempt(ctx context.Context, examID string, answers map[string]int) (score, total int, err error) {
	keys, err := s.AnswerKey(ctx, examID)
	if err != nil {
		return 0, 0, err
	}
	total = len(keys)
	for raw, chosen := range answers {
		index, e := strconv.Atoi(raw)
		if e == nil && keys[index] == chosen {
			score++
		}
	}
	return score, total, nil
}

func (s *Store) RecordAttemptStart(ctx context.Context, examID, studentID string) (*model.Attempt, error) {
	k := key("ATTEMPT#"+examID, "STUDENT#"+studentID)
	item, err := s.get(ctx, k)
	if err != nil {
		return nil, err
	}
	if item != nil {
		var a model.Attempt
		if err := unmarshal(item, &a); err != nil {
			return nil, err
		}
		return &a, nil
	}
	t := s.now()
	a := model.Attempt{PK: "ATTEMPT#" + examID, SK: "STUDENT#" + studentID, StartedAt: t.UnixMilli(), StartedAtISO: iso(t)}
	av, err := marshal(a)
	if err != nil {
		return nil, err
	}
	_, err = s.Client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.TableName), Item: av, ConditionExpression: aws.String("attribute_not_exists(pk)")})
	if err == nil {
		return &a, nil
	}
	if !isConditional(err) {
		return nil, err
	}
	item, err = s.get(ctx, k)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return &a, nil
	}
	if err := unmarshal(item, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) AttemptDeadline(ctx context.Context, examID, studentID string) (*model.Attempt, *int64, error) {
	meta, err := s.GetExamMeta(ctx, examID)
	if err != nil {
		return nil, nil, err
	}
	if meta == nil || meta.DurationMinutes <= 0 {
		return nil, nil, nil
	}
	item, err := s.get(ctx, key("ATTEMPT#"+examID, "STUDENT#"+studentID))
	if err != nil {
		return nil, nil, err
	}
	if item == nil {
		return nil, nil, nil
	}
	var a model.Attempt
	if err := unmarshal(item, &a); err != nil {
		return nil, nil, err
	}
	deadline := a.StartedAt + int64(meta.DurationMinutes)*60_000
	return &a, &deadline, nil
}

// SubmitAttempt performs grading and atomically writes both submission and
// journal. A condition on submission prevents concurrent requests from
// overwriting one another, while same request IDs remain idempotent.
func (s *Store) SubmitAttempt(ctx context.Context, examID, studentID, studentName string, answers map[string]int, requestID string) (model.SubmissionResult, error) {
	if requestID == "" {
		requestID = newID()
	}
	submissionKey := key("SUBMISSION#"+examID, "STUDENT#"+studentID)
	existing, err := s.get(ctx, submissionKey)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	if existing != nil {
		var old model.Submission
		if e := unmarshal(existing, &old); e != nil {
			return model.SubmissionResult{}, e
		}
		if old.RequestID == requestID {
			return model.SubmissionResult{Status: "duplicate", RequestID: requestID, Version: old.Version, CommittedAt: old.UpdatedAt, Score: old.Score, Total: old.Total}, nil
		}
		return model.SubmissionResult{}, ErrSubmissionConflict
	}
	score, total, err := s.GradeAttempt(ctx, examID, answers)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	now := s.now()
	sub := model.Submission{PK: "SUBMISSION#" + examID, SK: "STUDENT#" + studentID, Answers: answers, Version: 1, RequestID: requestID, UpdatedAt: now.UnixMilli(), UpdatedAtISO: iso(now), WrittenByRegion: s.Region, StudentName: studentName, Score: score, Total: total}
	subItem, err := marshal(sub)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	journal := model.OutboxEntry{PK: "JOURNAL#" + requestID, SK: "OUTBOX", TargetPK: sub.PK, TargetSK: sub.SK, Version: 1, RequestID: requestID, CreatedAt: now.UnixMilli(), Answers: answers, StudentName: studentName, Score: score, Total: total, TTL: now.Unix() + 7*24*3600}
	journalItem, err := marshal(journal)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	_, err = s.Client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{{Put: &types.Put{TableName: aws.String(s.TableName), Item: subItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}}, {Put: &types.Put{TableName: aws.String(s.TableName), Item: journalItem}}}})
	if err != nil {
		if isTransactionConditional(err) {
			winner, e := s.get(ctx, submissionKey)
			if e != nil {
				return model.SubmissionResult{}, e
			}
			if winner != nil {
				var old model.Submission
				if e := unmarshal(winner, &old); e == nil && old.RequestID == requestID {
					return model.SubmissionResult{Status: "duplicate", RequestID: requestID, Version: old.Version, CommittedAt: old.UpdatedAt, Score: old.Score, Total: old.Total}, nil
				}
			}
			return model.SubmissionResult{}, ErrSubmissionConflict
		}
		return model.SubmissionResult{}, err
	}
	return model.SubmissionResult{Status: "committed", RequestID: requestID, Version: 1, Score: score, Total: total}, nil
}

func (s *Store) GetSubmission(ctx context.Context, examID, studentID string) (*model.Submission, error) {
	item, err := s.get(ctx, key("SUBMISSION#"+examID, "STUDENT#"+studentID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, nil
	}
	var sub model.Submission
	if err := unmarshal(item, &sub); err != nil {
		return nil, err
	}
	return &sub, nil
}

func (s *Store) ListOutbox(ctx context.Context, limit int) ([]model.OutboxEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	out, err := s.Client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String(s.TableName), Limit: aws.Int32(int32(limit)), FilterExpression: aws.String("#sk = :outbox"), ExpressionAttributeNames: map[string]string{"#sk": "sk"}, ExpressionAttributeValues: map[string]types.AttributeValue{":outbox": &types.AttributeValueMemberS{Value: "OUTBOX"}}})
	if err != nil {
		return nil, err
	}
	result := make([]model.OutboxEntry, 0, len(out.Items))
	for _, item := range out.Items {
		var e model.OutboxEntry
		if err := unmarshal(item, &e); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].CreatedAt < result[j].CreatedAt })
	return result, nil
}

func (s *Store) HeartbeatProbe(ctx context.Context, region string) (ok bool, latencyMS float64, err error) {
	id := newID()
	t := s.now()
	probeKey := key("HEARTBEAT", "PROBE#"+region+"#"+id)
	started := time.Now()
	item := map[string]any{"pk": "HEARTBEAT", "sk": "PROBE#" + region + "#" + id, "ts": t.UnixMilli(), "region": region, "ttl": t.Unix() + int64(s.HeartbeatTTL)}
	av, e := marshal(item)
	if e != nil {
		return false, 0, e
	}
	if _, e = s.Client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.TableName), Item: av}); e != nil {
		return false, 0, e
	}
	read, e := s.get(ctx, probeKey)
	if e != nil {
		return false, roundMillis(time.Since(started)), e
	}
	ok = read != nil
	latencyMS = roundMillis(time.Since(started))
	if ok {
		_, e = s.Client.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: aws.String(s.TableName), Key: probeKey})
	}
	return ok, latencyMS, e
}

func roundMillis(elapsed time.Duration) float64 {
	return math.Round(float64(elapsed.Microseconds())/100) / 10
}

func (s *Store) LatestReplicatedTS(ctx context.Context, sourceRegion string) (int64, error) {
	out, err := s.Client.Query(ctx, &dynamodb.QueryInput{TableName: aws.String(s.TableName), KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "HEARTBEAT"}, ":prefix": &types.AttributeValueMemberS{Value: "PROBE#" + sourceRegion}}, ScanIndexForward: aws.Bool(false), Limit: aws.Int32(1)})
	if err != nil {
		return 0, err
	}
	if len(out.Items) == 0 {
		return 0, nil
	}
	var ts int64
	if err := attributevalue.Unmarshal(out.Items[0]["ts"], &ts); err != nil {
		return 0, err
	}
	return ts, nil
}

func (s *Store) ListSubmissions(ctx context.Context, examID string, limit int) ([]model.Submission, error) {
	if limit <= 0 {
		limit = 50
	}
	var items []map[string]types.AttributeValue
	var err error
	if examID != "" {
		// Preserve the caller's limit. Unlike GetExam, this endpoint is a
		// bounded admin listing; paginating through every page would silently
		// defeat the API's limit and can load an unbounded number of rows.
		out, queryErr := s.Client.Query(ctx, &dynamodb.QueryInput{TableName: aws.String(s.TableName), KeyConditionExpression: aws.String("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "SUBMISSION#" + examID}}, ScanIndexForward: aws.Bool(false), Limit: aws.Int32(int32(limit))})
		if queryErr == nil {
			items = out.Items
		}
		err = queryErr
	} else {
		out, e := s.Client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String(s.TableName), Limit: aws.Int32(int32(limit)), FilterExpression: aws.String("begins_with(#pk, :prefix)"), ExpressionAttributeNames: map[string]string{"#pk": "pk"}, ExpressionAttributeValues: map[string]types.AttributeValue{":prefix": &types.AttributeValueMemberS{Value: "SUBMISSION#"}}})
		if e == nil {
			items = out.Items
		}
		err = e
	}
	if err != nil {
		return nil, err
	}
	result := make([]model.Submission, 0, len(items))
	for _, item := range items {
		var sub model.Submission
		if err := unmarshal(item, &sub); err != nil {
			return nil, err
		}
		result = append(result, sub)
	}
	return result, nil
}

func (s *Store) DeleteExam(ctx context.Context, examID string) (int, error) {
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{TableName: aws.String(s.TableName), KeyConditionExpression: aws.String("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "EXAM#" + examID}}})
	if err != nil {
		return 0, err
	}
	count := 0
	for start := 0; start < len(items); start += 25 {
		end := start + 25
		if end > len(items) {
			end = len(items)
		}
		writes := make([]types.WriteRequest, 0, end-start)
		for _, item := range items[start:end] {
			writes = append(writes, types.WriteRequest{DeleteRequest: &types.DeleteRequest{Key: map[string]types.AttributeValue{"pk": item["pk"], "sk": item["sk"]}}})
		}
		pending := writes
		for attempt := 0; len(pending) > 0; attempt++ {
			out, err := s.Client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{RequestItems: map[string][]types.WriteRequest{s.TableName: pending}})
			if err != nil {
				return count, err
			}
			pending = out.UnprocessedItems[s.TableName]
			if len(pending) == 0 {
				break
			}
			// DynamoDB may return throttled writes without an API error. Match
			// Batch writes can be throttled, so back off and retry those writes,
			// while still allowing cancellation of a stuck delete.
			delay := 50 * time.Millisecond * time.Duration(1<<min(attempt, 6))
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return count, ctx.Err()
			case <-timer.C:
			}
		}
		count += len(writes)
	}
	return count, nil
}

// MemoryStore is a concurrency-safe in-memory implementation of the store
// contract useful for local development and service tests. It deliberately
// uses the same logical keys and result semantics as DynamoDBStore.
type MemoryStore struct {
	mu          sync.RWMutex
	users       map[string]*model.User
	emails      map[string]string
	exams       map[string]*model.Exam
	attempts    map[string]*model.Attempt
	submissions map[string]*model.Submission
	outbox      []model.OutboxEntry
	now         func() time.Time
	region      string
}

func NewMemory(region string) *MemoryStore {
	return &MemoryStore{users: map[string]*model.User{}, emails: map[string]string{}, exams: map[string]*model.Exam{}, attempts: map[string]*model.Attempt{}, submissions: map[string]*model.Submission{}, now: time.Now, region: region}
}
func (m *MemoryStore) GetUser(_ context.Context, id string) (*model.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u := m.users[id]
	if u == nil {
		return nil, nil
	}
	v := *u
	return &v, nil
}
func (m *MemoryStore) FindUserIDByEmail(_ context.Context, email string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.emails[strings.ToLower(strings.TrimSpace(email))], nil
}
func (m *MemoryStore) CreateUser(_ context.Context, u *model.User, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.emails[u.Email]; ok {
		return ErrEmailRegistered
	}
	v := *u
	v.PasswordHash = hash
	m.users[u.UserID] = &v
	m.emails[u.Email] = u.UserID
	return nil
}
func (m *MemoryStore) EnsureExam(_ context.Context, id, title string, qs []model.Question, w map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.exams == nil {
		m.exams = map[string]*model.Exam{}
	}
	v := &model.Exam{ExamMeta: model.ExamMeta{PK: "EXAM#" + id, SK: "META", ID: id, Title: title, CreatedAt: iso(m.now()), QuestionCount: len(qs)}}
	for field, val := range w {
		switch field {
		case "opens_at":
			v.OpensAt = fmt.Sprint(val)
		case "closes_at":
			v.ClosesAt = fmt.Sprint(val)
		case "duration_minutes":
			v.DurationMinutes = toInt(val)
		}
	}
	for i, q := range qs {
		q.Index = i
		v.Questions = append(v.Questions, q)
	}
	m.exams[id] = v
	return nil
}
func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
func cloneAnswers(in map[string]int) map[string]int {
	if in == nil {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func (m *MemoryStore) GetExamMeta(_ context.Context, id string) (*model.ExamMeta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e := m.exams[id]
	if e == nil {
		return nil, nil
	}
	v := e.ExamMeta
	v.ID = id
	return &v, nil
}
func (m *MemoryStore) GetExam(_ context.Context, id string, include bool) (*model.Exam, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e := m.exams[id]
	if e == nil {
		return nil, nil
	}
	v := *e
	v.Questions = append([]model.Question(nil), e.Questions...)
	if !include {
		for i := range v.Questions {
			v.Questions[i].Answer = 0
		}
	}
	return &v, nil
}

func (m *MemoryStore) UpdateExamWindow(_ context.Context, id string, title *string, window map[string]any) (*model.ExamMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.exams[id]
	if e == nil {
		return nil, nil
	}
	if title != nil {
		e.Title = *title
	}
	for field, val := range window {
		switch field {
		case "opens_at":
			e.OpensAt = fmt.Sprint(val)
		case "closes_at":
			e.ClosesAt = fmt.Sprint(val)
		case "duration_minutes":
			e.DurationMinutes = toInt(val)
		}
	}
	v := e.ExamMeta
	v.ID = id
	return &v, nil
}

func (m *MemoryStore) ListExams(_ context.Context, limit int) ([]model.ExamMeta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := make([]model.ExamMeta, 0, len(m.exams))
	for id, exam := range m.exams {
		row := exam.ExamMeta
		row.ID = id
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt < rows[j].CreatedAt })
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (m *MemoryStore) DeleteExam(_ context.Context, id string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.exams[id]
	if e == nil {
		return 0, nil
	}
	delete(m.exams, id)
	return 1 + len(e.Questions), nil
}

func (m *MemoryStore) ListSubmissions(_ context.Context, examID string, limit int) ([]model.Submission, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := make([]model.Submission, 0, len(m.submissions))
	prefix := examID + "/"
	for k, sub := range m.submissions {
		if examID != "" && !strings.HasPrefix(k, prefix) {
			continue
		}
		row := *sub
		row.Answers = cloneAnswers(sub.Answers)
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].UpdatedAt > rows[j].UpdatedAt })
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (m *MemoryStore) ListOutbox(_ context.Context, limit int) ([]model.OutboxEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := make([]model.OutboxEntry, len(m.outbox))
	copy(rows, m.outbox)
	for i := range rows {
		rows[i].Answers = cloneAnswers(rows[i].Answers)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt < rows[j].CreatedAt })
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (m *MemoryStore) HeartbeatProbe(_ context.Context, _ string) (bool, float64, error) {
	return true, 0, nil
}

func (m *MemoryStore) LatestReplicatedTS(_ context.Context, _ string) (int64, error) {
	return 0, nil
}
func (m *MemoryStore) RecordAttemptStart(_ context.Context, e, id string) (*model.Attempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := e + "/" + id
	if a := m.attempts[k]; a != nil {
		v := *a
		return &v, nil
	}
	t := m.now()
	a := &model.Attempt{PK: "ATTEMPT#" + e, SK: "STUDENT#" + id, StartedAt: t.UnixMilli(), StartedAtISO: iso(t)}
	m.attempts[k] = a
	v := *a
	return &v, nil
}
func (m *MemoryStore) AttemptDeadline(_ context.Context, e, id string) (*model.Attempt, *int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	meta := m.exams[e]
	a := m.attempts[e+"/"+id]
	if meta == nil || meta.DurationMinutes <= 0 || a == nil {
		return nil, nil, nil
	}
	v := *a
	d := a.StartedAt + int64(meta.DurationMinutes)*60000
	return &v, &d, nil
}
func (m *MemoryStore) GradeAttempt(_ context.Context, e string, ans map[string]int) (int, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ex := m.exams[e]
	if ex == nil {
		return 0, 0, nil
	}
	score := 0
	for _, q := range ex.Questions {
		if a, ok := ans[strconv.Itoa(q.Index)]; ok && a == q.Answer {
			score++
		}
	}
	return score, len(ex.Questions), nil
}
func (m *MemoryStore) SubmitAttempt(_ context.Context, e, id, name string, ans map[string]int, req string) (model.SubmissionResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if req == "" {
		req = newID()
	}
	k := e + "/" + id
	if old := m.submissions[k]; old != nil {
		if old.RequestID == req {
			return model.SubmissionResult{Status: "duplicate", RequestID: req, Version: old.Version, CommittedAt: old.UpdatedAt, Score: old.Score, Total: old.Total}, nil
		}
		return model.SubmissionResult{}, ErrSubmissionConflict
	}
	ex := m.exams[e]
	score, total := 0, 0
	if ex != nil {
		total = len(ex.Questions)
		for _, q := range ex.Questions {
			if a, ok := ans[strconv.Itoa(q.Index)]; ok && a == q.Answer {
				score++
			}
		}
	}
	t := m.now()
	sub := &model.Submission{PK: "SUBMISSION#" + e, SK: "STUDENT#" + id, RequestID: req, Version: 1, UpdatedAt: t.UnixMilli(), UpdatedAtISO: iso(t), WrittenByRegion: m.region, Answers: cloneAnswers(ans), StudentName: name, Score: score, Total: total}
	m.submissions[k] = sub
	m.outbox = append(m.outbox, model.OutboxEntry{PK: "JOURNAL#" + req, SK: "OUTBOX", TargetPK: sub.PK, TargetSK: sub.SK, Version: 1, RequestID: req, CreatedAt: t.UnixMilli(), Answers: cloneAnswers(ans), StudentName: name, Score: score, Total: total, TTL: t.Unix() + 7*24*3600})
	return model.SubmissionResult{Status: "committed", RequestID: req, Version: 1, Score: score, Total: total}, nil
}
func (m *MemoryStore) GetSubmission(_ context.Context, e, id string) (*model.Submission, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v := m.submissions[e+"/"+id]
	if v == nil {
		return nil, nil
	}
	x := *v
	x.Answers = cloneAnswers(v.Answers)
	return &x, nil
}
