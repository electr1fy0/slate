// Package store implements the continuum single-table persistence contract.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"github.com/ayush/slate/internal/clusterstate"
	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/model"
)

var (
	ErrNotFound                  = errors.New("not found")
	ErrSubmissionConflict        = errors.New("submission conflict")
	ErrEmailRegistered           = errors.New("email already registered")
	ErrClassroomCodeTaken        = errors.New("classroom join code already in use")
	ErrClassroomForbidden        = errors.New("classroom ownership required")
	ErrClassroomMember           = errors.New("classroom member already exists")
	ErrClassroomMemberNotFound   = errors.New("classroom member not found")
	ErrQuizNotFound              = errors.New("quiz not found")
	ErrQuizConflict              = errors.New("quiz conflict")
	ErrQuizForbidden             = errors.New("quiz ownership violation")
	ErrAttemptConflict           = errors.New("attempt version conflict")
	ErrAttemptSubmitted          = errors.New("attempt already submitted")
	ErrSubmissionRequestConflict = errors.New("submission request ID was reused with a different payload")
	ErrAttemptsExhausted         = errors.New("attempt limit reached")
	ErrInvalidAnswer             = errors.New("invalid answer")
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
	Client         DynamoAPI
	TableName      string
	Region         string
	HeartbeatTTL   int
	FencingEnabled bool
	Now            func() time.Time
}

var loadAWSConfig = awsconfig.LoadDefaultConfig
var readRandomBytes = rand.Read

// New constructs a Store using the default AWS credential/region chain.
func New(ctx context.Context, settings config.Settings) (*Store, error) {
	awsCfg, err := loadAWSConfig(ctx, awsconfig.WithRegion(settings.Region))
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
		HeartbeatTTL: settings.HeartbeatTTLS, FencingEnabled: settings.FencingEnabled, Now: time.Now}
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
	if _, err := readRandomBytes(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
func key(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk}}
}

var marshal = attributevalue.MarshalMap

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

func (s *Store) getConsistent(ctx context.Context, k map[string]types.AttributeValue) (map[string]types.AttributeValue, error) {
	out, err := s.Client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.TableName), Key: k, ConsistentRead: aws.Bool(true)})
	if err != nil {
		return nil, err
	}
	if len(out.Item) == 0 {
		return nil, nil
	}
	return out.Item, nil
}

func writerFromContext(ctx context.Context) (clusterstate.Writer, bool, error) {
	writer, ok := clusterstate.WriterFrom(ctx)
	if !ok {
		return clusterstate.Writer{}, false, nil
	}
	if (writer.Backend != "A" && writer.Backend != "B") || writer.Epoch <= 0 {
		return clusterstate.Writer{}, false, clusterstate.ErrFenced
	}
	return writer, true, nil
}

func appendFenceCheck(ctx context.Context, table string, items []types.TransactWriteItem) ([]types.TransactWriteItem, bool, error) {
	writer, present, err := writerFromContext(ctx)
	if err != nil {
		return nil, false, err
	}
	if !present {
		return items, false, nil
	}
	return append(items, clusterstate.FenceConditionCheck(table, writer)), true, nil
}

// transactWrite keeps the writer condition in the same DynamoDB transaction
// as the application writes. A missing writer is allowed only for explicitly
// marked bootstrap calls and legacy/local configurations where fencing is
// disabled.
func (s *Store) transactWrite(ctx context.Context, items []types.TransactWriteItem) error {
	if _, err := s.requireMutationWriter(ctx); err != nil {
		return err
	}
	transaction, fenced, err := appendFenceCheck(ctx, s.TableName, items)
	if err != nil {
		return err
	}
	if len(transaction) > 100 {
		return fmt.Errorf("DynamoDB transaction exceeds 100 items")
	}
	_, err = s.Client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: transaction})
	if err != nil && fenced && isFenceTransactionFailure(err, len(transaction)-1) {
		return clusterstate.ErrFenced
	}
	return err
}

func (s *Store) writerPresent(ctx context.Context) (bool, error) {
	_, present, err := writerFromContext(ctx)
	return present, err
}

// requireMutationWriter rejects headerless durable writes when API fencing is
// enabled. Bootstrap seeding is the sole intentional headerless exception;
// it uses create-only operations and a private context marker.
func (s *Store) requireMutationWriter(ctx context.Context) (bool, error) {
	present, err := s.writerPresent(ctx)
	if err != nil {
		return false, err
	}
	if s.FencingEnabled && !present && !isBootstrapWrite(ctx) {
		return false, clusterstate.ErrFenced
	}
	return present, nil
}

func (s *Store) fencedUpdateItem(ctx context.Context, input *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
	present, err := s.requireMutationWriter(ctx)
	if err != nil {
		return nil, err
	}
	if !present {
		return s.Client.UpdateItem(ctx, input)
	}
	update := &types.Update{
		TableName: aws.String(s.TableName), Key: input.Key,
		UpdateExpression: input.UpdateExpression, ConditionExpression: input.ConditionExpression,
		ExpressionAttributeNames:            input.ExpressionAttributeNames,
		ExpressionAttributeValues:           input.ExpressionAttributeValues,
		ReturnValuesOnConditionCheckFailure: input.ReturnValuesOnConditionCheckFailure,
	}
	if err := s.transactWrite(ctx, []types.TransactWriteItem{{Update: update}}); err != nil {
		return nil, err
	}
	item, err := s.getConsistent(ctx, input.Key)
	if err != nil {
		return nil, err
	}
	return &dynamodb.UpdateItemOutput{Attributes: item}, nil
}

func (s *Store) fencedPutItem(ctx context.Context, input *dynamodb.PutItemInput) error {
	present, err := s.requireMutationWriter(ctx)
	if err != nil {
		return err
	}
	if !present {
		_, err := s.Client.PutItem(ctx, input)
		return err
	}
	put := &types.Put{
		TableName: aws.String(s.TableName), Item: input.Item,
		ConditionExpression:                 input.ConditionExpression,
		ExpressionAttributeNames:            input.ExpressionAttributeNames,
		ExpressionAttributeValues:           input.ExpressionAttributeValues,
		ReturnValuesOnConditionCheckFailure: input.ReturnValuesOnConditionCheckFailure,
	}
	return s.transactWrite(ctx, []types.TransactWriteItem{{Put: put}})
}

func isFenceTransactionFailure(err error, index int) bool {
	var canceled *types.TransactionCanceledException
	if !errors.As(err, &canceled) || index < 0 || len(canceled.CancellationReasons) <= index {
		return false
	}
	reason := canceled.CancellationReasons[index]
	return reason.Code != nil && *reason.Code == "ConditionalCheckFailed"
}

func submissionPayloadHash(examID, studentID string, answers map[string]int) string {
	payload, _ := json.Marshal(struct {
		ExamID    string         `json:"exam_id"`
		StudentID string         `json:"student_id"`
		Answers   map[string]int `json:"answers"`
	}{ExamID: examID, StudentID: studentID, Answers: answers})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func submissionRequestKey(requestID string) map[string]types.AttributeValue {
	sum := sha256.Sum256([]byte(requestID))
	return key("SUBMISSION_REQUEST#"+hex.EncodeToString(sum[:]), "RECEIPT")
}

func hasTransactionCancellationReasons(err error) bool {
	var canceled *types.TransactionCanceledException
	return errors.As(err, &canceled) && len(canceled.CancellationReasons) > 0
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

// CreateUser atomically creates the profile and unique email index. The only
// unfenced production call is startup seeding, which is explicitly marked by
// WithBootstrap and remains idempotent through the unique email index.
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
	err = s.transactWrite(ctx, []types.TransactWriteItem{
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: profileItem}},
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: idx, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	})
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

// EnsureExam writes an exam definition as one fenced transaction when invoked
// by an API writer. Startup must use EnsureExamIfMissing instead.
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
	items := []types.TransactWriteItem{{Put: &types.Put{TableName: aws.String(s.TableName), Item: item}}}
	for i, question := range questions {
		qItem, err := quizQuestionItem("EXAM#"+examID, i, question)
		if err != nil {
			return err
		}
		items = append(items, types.TransactWriteItem{Put: &types.Put{TableName: aws.String(s.TableName), Item: qItem}})
	}
	return s.transactWrite(ctx, items)
}

// EnsureExamIfMissing is a bootstrap-only, idempotent seed operation. It is
// deliberately independent of router headers: the metadata condition and
// one transaction for metadata+questions make concurrent A/B startup safe,
// and an existing exam (including faculty edits) is never overwritten.
func (s *Store) EnsureExamIfMissing(ctx context.Context, examID, title string, questions []model.Question, window map[string]any) error {
	if _, err := s.requireMutationWriter(ctx); err != nil {
		return err
	}
	metaKey := key("EXAM#"+examID, "META")
	if existing, err := s.getConsistent(ctx, metaKey); err != nil {
		return err
	} else if existing != nil {
		return nil
	}
	meta := model.ExamMeta{PK: "EXAM#" + examID, SK: "META", Title: title, CreatedAt: iso(s.now()), QuestionCount: len(questions)}
	item, err := marshal(meta)
	if err != nil {
		return err
	}
	for field, value := range window {
		av, marshalErr := attributevalue.Marshal(value)
		if marshalErr != nil {
			return fmt.Errorf("marshal window %s: %w", field, marshalErr)
		}
		item[field] = av
	}
	items := []types.TransactWriteItem{{Put: &types.Put{TableName: aws.String(s.TableName), Item: item, ConditionExpression: aws.String("attribute_not_exists(pk)")}}}
	for index, question := range questions {
		questionItem, marshalErr := quizQuestionItem("EXAM#"+examID, index, question)
		if marshalErr != nil {
			return marshalErr
		}
		items = append(items, types.TransactWriteItem{Put: &types.Put{TableName: aws.String(s.TableName), Item: questionItem}})
	}
	items, fenced, err := appendFenceCheck(ctx, s.TableName, items)
	if err != nil {
		return err
	}
	_, err = s.Client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	if fenced && isFenceTransactionFailure(err, len(items)-1) {
		return clusterstate.ErrFenced
	}
	if isTransactionConditional(err) {
		// A concurrent backend seeded this exam first. Its transaction wrote the
		// complete definition because metadata and questions commit together.
		return nil
	}
	return err
}

func quizQuestionItem(pk string, index int, question model.Question) (map[string]types.AttributeValue, error) {
	return marshal(struct {
		PK       string   `dynamodbav:"pk"`
		SK       string   `dynamodbav:"sk"`
		Question string   `dynamodbav:"question"`
		Options  []string `dynamodbav:"options"`
		Answer   int      `dynamodbav:"answer"`
		Points   int      `dynamodbav:"points,omitempty"`
		Feedback string   `dynamodbav:"feedback,omitempty"`
	}{
		PK: pk, SK: fmt.Sprintf("QUESTION#%03d", index), Question: question.Question,
		Options: question.Options, Answer: question.Answer, Points: question.Points, Feedback: question.Feedback,
	})
}

func cloneQuestions(in []model.Question) []model.Question {
	if in == nil {
		return nil
	}
	out := make([]model.Question, len(in))
	for i, question := range in {
		out[i] = question
		out[i].Index = i
		out[i].Options = append([]string(nil), question.Options...)
	}
	return out
}

func cloneDraft(in *model.QuizDraft) *model.QuizDraft {
	if in == nil {
		return nil
	}
	out := *in
	out.Questions = cloneQuestions(in.Questions)
	return &out
}

func effectiveAttempts(value int) int {
	if value <= 0 {
		return 1
	}
	return value
}

func effectivePoints(value int) int {
	if value <= 0 {
		return 1
	}
	return value
}

func ownerCanEdit(actorID, ownerID string) bool {
	return actorID == "admin" || ownerID == "" || actorID == ownerID
}

func (s *Store) writeExamDefinition(ctx context.Context, examID string, meta model.ExamMeta, questions []model.Question, conditional bool) error {
	meta.PK = "EXAM#" + examID
	meta.SK = "META"
	meta.ID = ""
	meta.QuestionCount = len(questions)
	item, err := marshal(meta)
	if err != nil {
		return err
	}
	put := &types.Put{TableName: aws.String(s.TableName), Item: item}
	if conditional {
		put.ConditionExpression = aws.String("attribute_not_exists(pk)")
	}
	items := []types.TransactWriteItem{{Put: put}}
	for i, question := range questions {
		qItem, err := quizQuestionItem("EXAM#"+examID, i, question)
		if err != nil {
			return err
		}
		items = append(items, types.TransactWriteItem{Put: &types.Put{TableName: aws.String(s.TableName), Item: qItem}})
	}
	err = s.transactWrite(ctx, items)
	if conditional && err != nil && isTransactionConditional(err) {
		return ErrQuizConflict
	}
	return err
}

// CreateQuizDraft stores teacher-owned metadata and question items. The
// transaction makes a draft either fully visible or not visible at all.
func (s *Store) CreateQuizDraft(ctx context.Context, draft *model.QuizDraft) (*model.QuizDraft, error) {
	if draft == nil || strings.TrimSpace(draft.ID) == "" || strings.TrimSpace(draft.OwnerID) == "" {
		return nil, fmt.Errorf("quiz draft id and owner are required")
	}
	if strings.TrimSpace(draft.Title) == "" {
		return nil, fmt.Errorf("quiz draft title is required")
	}
	if len(draft.Questions) > 98 {
		return nil, fmt.Errorf("quiz draft has too many questions")
	}
	now := iso(s.now())
	value := *draft
	value.PK = "QUIZ#" + value.ID
	value.SK = "META"
	value.Title = strings.TrimSpace(value.Title)
	value.Status = "draft"
	value.QuestionCount = len(value.Questions)
	if value.CreatedAt == "" {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	value.Questions = cloneQuestions(value.Questions)
	metaItem, err := marshal(value)
	if err != nil {
		return nil, err
	}
	items := []types.TransactWriteItem{{Put: &types.Put{TableName: aws.String(s.TableName), Item: metaItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}}}
	for i, question := range value.Questions {
		qItem, err := quizQuestionItem(value.PK, i, question)
		if err != nil {
			return nil, err
		}
		items = append(items, types.TransactWriteItem{Put: &types.Put{TableName: aws.String(s.TableName), Item: qItem}})
	}
	if len(items) > 100 {
		return nil, fmt.Errorf("quiz draft is too large")
	}
	err = s.transactWrite(ctx, items)
	if err != nil {
		if isTransactionConditional(err) || isConditional(err) {
			return nil, ErrQuizConflict
		}
		return nil, err
	}
	return cloneDraft(&value), nil
}

func (s *Store) GetQuizDraft(ctx context.Context, id string) (*model.QuizDraft, error) {
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{TableName: aws.String(s.TableName), KeyConditionExpression: aws.String("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "QUIZ#" + id}}})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	var draft model.QuizDraft
	foundMeta := false
	for _, item := range items {
		var sk string
		if av, ok := item["sk"].(*types.AttributeValueMemberS); ok {
			sk = av.Value
		}
		if sk == "META" {
			if err := unmarshal(item, &draft); err != nil {
				return nil, err
			}
			foundMeta = true
			continue
		}
		if strings.HasPrefix(sk, "QUESTION#") {
			var question questionItem
			if err := unmarshal(item, &question); err != nil {
				return nil, err
			}
			index, err := strconv.Atoi(strings.TrimPrefix(sk, "QUESTION#"))
			if err != nil {
				continue
			}
			draft.Questions = append(draft.Questions, model.Question{Index: index, Question: question.Question, Options: question.Options, Answer: question.Answer, Points: question.Points, Feedback: question.Feedback})
		}
	}
	if !foundMeta {
		return nil, nil
	}
	sort.Slice(draft.Questions, func(i, j int) bool { return draft.Questions[i].Index < draft.Questions[j].Index })
	draft.ID = strings.TrimPrefix(draft.PK, "QUIZ#")
	if draft.ID == "" {
		draft.ID = id
	}
	draft.QuestionCount = len(draft.Questions)
	if draft.Status == "" {
		draft.Status = "draft"
	}
	return cloneDraft(&draft), nil
}

func (s *Store) scanAll(ctx context.Context, input *dynamodb.ScanInput) ([]map[string]types.AttributeValue, error) {
	var all []map[string]types.AttributeValue
	for {
		out, err := s.Client.Scan(ctx, input)
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

func (s *Store) ListQuizDrafts(ctx context.Context, ownerID string, limit int) ([]model.QuizDraft, error) {
	if limit <= 0 {
		limit = 50
	}
	names := map[string]string{"#pk": "pk", "#sk": "sk", "#status": "status"}
	values := map[string]types.AttributeValue{
		":prefix": &types.AttributeValueMemberS{Value: "QUIZ#"},
		":meta":   &types.AttributeValueMemberS{Value: "META"},
		":draft":  &types.AttributeValueMemberS{Value: "draft"},
	}
	filter := "begins_with(#pk, :prefix) AND #sk = :meta AND #status = :draft"
	if ownerID != "" && ownerID != "admin" {
		filter += " AND owner_id = :owner"
		values[":owner"] = &types.AttributeValueMemberS{Value: ownerID}
	}
	items, err := s.scanAll(ctx, &dynamodb.ScanInput{TableName: aws.String(s.TableName), Limit: aws.Int32(int32(limit)), FilterExpression: aws.String(filter), ExpressionAttributeNames: names, ExpressionAttributeValues: values})
	if err != nil {
		return nil, err
	}
	result := make([]model.QuizDraft, 0, len(items))
	for _, item := range items {
		var draft model.QuizDraft
		if err := unmarshal(item, &draft); err != nil {
			return nil, err
		}
		draft.ID = strings.TrimPrefix(draft.PK, "QUIZ#")
		result = append(result, draft)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].UpdatedAt == result[j].UpdatedAt {
			return result[i].ID < result[j].ID
		}
		return result[i].UpdatedAt > result[j].UpdatedAt
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *Store) deleteByPK(ctx context.Context, pk string) (int, error) {
	if _, err := s.requireMutationWriter(ctx); err != nil {
		return 0, err
	}
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{TableName: aws.String(s.TableName), KeyConditionExpression: aws.String("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: pk}}})
	if err != nil {
		return 0, err
	}
	count := 0
	fenced, err := s.writerPresent(ctx)
	if err != nil {
		return 0, err
	}
	chunkSize := 25
	if fenced {
		chunkSize = 99
	}
	for start := 0; start < len(items); start += chunkSize {
		end := min(start+chunkSize, len(items))
		if fenced {
			transaction := make([]types.TransactWriteItem, 0, end-start)
			for _, item := range items[start:end] {
				transaction = append(transaction, types.TransactWriteItem{Delete: &types.Delete{TableName: aws.String(s.TableName), Key: map[string]types.AttributeValue{"pk": item["pk"], "sk": item["sk"]}}})
			}
			if err := s.transactWrite(ctx, transaction); err != nil {
				return count, err
			}
			count += len(transaction)
			continue
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

func (s *Store) UpdateQuizDraft(ctx context.Context, ownerID string, draft *model.QuizDraft) (*model.QuizDraft, error) {
	if draft == nil {
		return nil, fmt.Errorf("quiz draft is required")
	}
	current, err := s.GetQuizDraft(ctx, draft.ID)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, ErrQuizNotFound
	}
	if !ownerCanEdit(ownerID, current.OwnerID) {
		return nil, ErrQuizForbidden
	}
	if current.Status != "draft" {
		return nil, ErrQuizConflict
	}
	value := *draft
	value.ID = current.ID
	value.PK = current.PK
	value.SK = current.SK
	value.OwnerID = current.OwnerID
	value.CreatedAt = current.CreatedAt
	value.Status = "draft"
	value.Title = strings.TrimSpace(value.Title)
	value.QuestionCount = len(value.Questions)
	value.UpdatedAt = iso(s.now())
	value.Questions = cloneQuestions(value.Questions)
	if value.Title == "" {
		return nil, fmt.Errorf("quiz draft title is required")
	}
	if len(value.Questions) > 98 {
		return nil, fmt.Errorf("quiz draft has too many questions")
	}
	metaItem, err := marshal(value)
	if err != nil {
		return nil, err
	}
	metaPut := &types.Put{
		TableName: aws.String(s.TableName), Item: metaItem,
		ConditionExpression:      aws.String("owner_id = :owner AND #status = :draft"),
		ExpressionAttributeNames: map[string]string{"#status": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":owner": &types.AttributeValueMemberS{Value: current.OwnerID},
			":draft": &types.AttributeValueMemberS{Value: "draft"},
		},
	}
	items := []types.TransactWriteItem{{Put: metaPut}}
	for i, question := range value.Questions {
		item, err := quizQuestionItem(value.PK, i, question)
		if err != nil {
			return nil, err
		}
		items = append(items, types.TransactWriteItem{Put: &types.Put{TableName: aws.String(s.TableName), Item: item}})
	}
	oldQuestionCount := max(len(current.Questions), current.QuestionCount)
	for i := len(value.Questions); i < oldQuestionCount; i++ {
		items = append(items, types.TransactWriteItem{Delete: &types.Delete{TableName: aws.String(s.TableName), Key: key(value.PK, fmt.Sprintf("QUESTION#%03d", i))}})
	}
	if err := s.transactWrite(ctx, items); err != nil {
		if isTransactionConditional(err) {
			return nil, ErrQuizConflict
		}
		return nil, err
	}
	return cloneDraft(&value), nil
}

func (s *Store) DeleteQuizDraft(ctx context.Context, id, ownerID string) (bool, error) {
	draft, err := s.GetQuizDraft(ctx, id)
	if err != nil {
		return false, err
	}
	if draft == nil {
		return false, nil
	}
	if !ownerCanEdit(ownerID, draft.OwnerID) {
		return false, ErrQuizForbidden
	}
	if draft.Status != "draft" {
		return false, ErrQuizConflict
	}
	n, err := s.deleteByPK(ctx, draft.PK)
	return n > 0, err
}

func (s *Store) PublishQuizDraft(ctx context.Context, id, ownerID string) (*model.ExamMeta, error) {
	draft, err := s.GetQuizDraft(ctx, id)
	if err != nil {
		return nil, err
	}
	if draft == nil {
		return nil, ErrQuizNotFound
	}
	if !ownerCanEdit(ownerID, draft.OwnerID) {
		return nil, ErrQuizForbidden
	}
	if draft.Status != "draft" {
		return nil, ErrQuizConflict
	}
	if len(draft.Questions) == 0 {
		return nil, fmt.Errorf("at least one question is required to publish")
	}
	if existing, err := s.GetExamMeta(ctx, id); err != nil {
		return nil, err
	} else if existing != nil {
		return nil, ErrQuizConflict
	}
	if len(draft.Questions) > 97 {
		return nil, fmt.Errorf("quiz draft has too many questions to publish atomically")
	}
	meta := model.ExamMeta{PK: "EXAM#" + id, SK: "META", Title: draft.Title, CreatedAt: iso(s.now()), OwnerID: draft.OwnerID, Status: "published", QuestionCount: len(draft.Questions), OpensAt: draft.OpensAt, ClosesAt: draft.ClosesAt, DurationMinutes: draft.DurationMinutes, MaxAttempts: draft.MaxAttempts, AllowLate: draft.AllowLate, LateGraceMinutes: draft.LateGraceMinutes}
	metaItem, err := marshal(meta)
	if err != nil {
		return nil, err
	}
	items := []types.TransactWriteItem{{Put: &types.Put{TableName: aws.String(s.TableName), Item: metaItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}}}
	for i, question := range draft.Questions {
		item, err := quizQuestionItem("EXAM#"+id, i, question)
		if err != nil {
			return nil, err
		}
		items = append(items, types.TransactWriteItem{Put: &types.Put{TableName: aws.String(s.TableName), Item: item}})
	}
	publishedAt := iso(s.now())
	items = append(items, types.TransactWriteItem{Update: &types.Update{
		TableName: aws.String(s.TableName), Key: key(draft.PK, draft.SK),
		UpdateExpression:         aws.String("SET #status = :status, published_at = :published_at, updated_at = :updated_at"),
		ConditionExpression:      aws.String("owner_id = :owner AND #status = :draft"),
		ExpressionAttributeNames: map[string]string{"#status": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":status": &types.AttributeValueMemberS{Value: "published"}, ":draft": &types.AttributeValueMemberS{Value: "draft"},
			":owner": &types.AttributeValueMemberS{Value: draft.OwnerID}, ":published_at": &types.AttributeValueMemberS{Value: publishedAt}, ":updated_at": &types.AttributeValueMemberS{Value: publishedAt},
		},
	}})
	err = s.transactWrite(ctx, items)
	if err != nil {
		if isTransactionConditional(err) || isConditional(err) {
			return nil, ErrQuizConflict
		}
		return nil, err
	}
	meta.ID = id
	return &meta, nil
}

func (s *Store) DuplicateQuiz(ctx context.Context, sourceID, ownerID, requestedID, title string) (*model.QuizDraft, error) {
	if strings.TrimSpace(ownerID) == "" {
		return nil, fmt.Errorf("owner is required")
	}
	var sourceTitle string
	var sourceQuestions []model.Question
	var opensAt, closesAt string
	var duration, maxAttempts, lateGrace int
	var allowLate bool
	draft, err := s.GetQuizDraft(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if draft != nil {
		if !ownerCanEdit(ownerID, draft.OwnerID) {
			return nil, ErrQuizForbidden
		}
		sourceTitle, sourceQuestions = draft.Title, draft.Questions
		opensAt, closesAt, duration = draft.OpensAt, draft.ClosesAt, draft.DurationMinutes
		maxAttempts, allowLate, lateGrace = draft.MaxAttempts, draft.AllowLate, draft.LateGraceMinutes
	} else {
		exam, err := s.GetExam(ctx, sourceID, true)
		if err != nil {
			return nil, err
		}
		if exam == nil {
			return nil, ErrQuizNotFound
		}
		if !ownerCanEdit(ownerID, exam.OwnerID) {
			return nil, ErrQuizForbidden
		}
		sourceTitle, sourceQuestions = exam.Title, exam.Questions
		opensAt, closesAt, duration = exam.OpensAt, exam.ClosesAt, exam.DurationMinutes
		maxAttempts, allowLate, lateGrace = exam.MaxAttempts, exam.AllowLate, exam.LateGraceMinutes
	}
	copyID := strings.TrimSpace(requestedID)
	if copyID == "" {
		copyID = "quiz-" + newID()
	}
	if strings.TrimSpace(title) == "" {
		title = "Copy of " + sourceTitle
	}
	copy := &model.QuizDraft{ID: copyID, OwnerID: ownerID, Title: title, Questions: cloneQuestions(sourceQuestions), OpensAt: opensAt, ClosesAt: closesAt, DurationMinutes: duration, MaxAttempts: maxAttempts, AllowLate: allowLate, LateGraceMinutes: lateGrace}
	return s.CreateQuizDraft(ctx, copy)
}

// UpdateExamWindow patches only supplied mutable metadata and returns META.
func (s *Store) UpdateExamWindow(ctx context.Context, examID string, title *string, window map[string]any) (*model.ExamMeta, error) {
	parts := make([]string, 0, 8)
	names := map[string]string{}
	values := map[string]types.AttributeValue{}
	if title != nil {
		parts = append(parts, "#title = :title")
		names["#title"] = "title"
		values[":title"] = &types.AttributeValueMemberS{Value: *title}
	}
	for _, field := range []string{"opens_at", "closes_at", "duration_minutes", "owner_id", "status", "max_attempts", "allow_late", "late_grace_minutes"} {
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
	out, err := s.fencedUpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(s.TableName), Key: key("EXAM#"+examID, "META"), UpdateExpression: aws.String("SET " + strings.Join(parts, ", ")), ExpressionAttributeNames: names, ExpressionAttributeValues: values, ReturnValues: types.ReturnValueAllNew})
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
	Points   int      `dynamodbav:"points"`
	Feedback string   `dynamodbav:"feedback"`
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
			exam.Questions = append(exam.Questions, model.Question{Index: index, Question: q.Question, Options: q.Options, Answer: q.Answer, Points: q.Points, Feedback: q.Feedback})
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
			exam.Questions[i].Feedback = ""
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
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{TableName: aws.String(s.TableName), KeyConditionExpression: aws.String("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "EXAM#" + examID}}})
	if err != nil {
		return 0, 0, err
	}
	for _, item := range items {
		av, ok := item["sk"].(*types.AttributeValueMemberS)
		if !ok || !strings.HasPrefix(av.Value, "QUESTION#") {
			continue
		}
		index, e := strconv.Atoi(strings.TrimPrefix(av.Value, "QUESTION#"))
		if e != nil {
			continue
		}
		var q questionItem
		if err := unmarshal(item, &q); err != nil {
			return 0, 0, err
		}
		points := q.Points
		if points <= 0 {
			points = 1
		}
		total += points
		if chosen, ok := answers[strconv.Itoa(index)]; ok && chosen == q.Answer {
			score += points
		}
	}
	return score, total, nil
}

func (s *Store) RecordAttemptStart(ctx context.Context, examID, studentID string) (*model.Attempt, error) {
	if _, err := s.requireMutationWriter(ctx); err != nil {
		return nil, err
	}
	k := key("ATTEMPT#"+examID, "STUDENT#"+studentID)
	item, err := s.getConsistent(ctx, k)
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
	if fenceWriter, fenced, fenceErr := appendFenceCheck(ctx, s.TableName, []types.TransactWriteItem{{Put: &types.Put{TableName: aws.String(s.TableName), Item: av, ConditionExpression: aws.String("attribute_not_exists(pk)")}}}); fenceErr != nil {
		return nil, fenceErr
	} else if fenced {
		_, err = s.Client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: fenceWriter})
		if err != nil {
			if isFenceTransactionFailure(err, len(fenceWriter)-1) {
				return nil, clusterstate.ErrFenced
			}
			if isTransactionConditional(err) {
				item, readErr := s.getConsistent(ctx, k)
				if readErr != nil {
					return nil, readErr
				}
				if item != nil {
					if decodeErr := unmarshal(item, &a); decodeErr != nil {
						return nil, decodeErr
					}
					return &a, nil
				}
			}
			return nil, err
		}
		return &a, nil
	} else {
		_, err = s.Client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.TableName), Item: av, ConditionExpression: aws.String("attribute_not_exists(pk)")})
	}
	if err == nil {
		return &a, nil
	}
	if !isConditional(err) {
		return nil, err
	}
	item, err = s.getConsistent(ctx, k)
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
	item, err := s.getConsistent(ctx, key("ATTEMPT#"+examID, "STUDENT#"+studentID))
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

// GetStudentAttempt returns the durable answer snapshot for one student.
func (s *Store) GetStudentAttempt(ctx context.Context, examID, studentID string) (*model.Attempt, error) {
	item, err := s.getConsistent(ctx, key("ATTEMPT#"+examID, "STUDENT#"+studentID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, nil
	}
	var attempt model.Attempt
	if err := unmarshal(item, &attempt); err != nil {
		return nil, err
	}
	attempt.Answers = cloneAnswers(attempt.Answers)
	return &attempt, nil
}

// SaveAttempt persists a versioned answer snapshot. The conditional put is
// intentionally optimistic: concurrent browser tabs cannot overwrite a newer
// version, and a repeated request ID is returned as an idempotent duplicate.
func (s *Store) SaveAttempt(ctx context.Context, examID, studentID string, answers map[string]int, expectedVersion int, requestID string) (*model.Attempt, error) {
	if _, err := s.requireMutationWriter(ctx); err != nil {
		return nil, err
	}
	if requestID == "" {
		return nil, fmt.Errorf("request_id is required")
	}
	attempt, err := s.GetStudentAttempt(ctx, examID, studentID)
	if err != nil {
		return nil, err
	}
	if attempt == nil {
		attempt, err = s.RecordAttemptStart(ctx, examID, studentID)
		if err != nil {
			return nil, err
		}
	}
	if attempt.LastRequestID == requestID {
		return attempt, nil
	}
	// The legacy submit endpoint writes a submission but may not have an
	// attempt record with Submitted=true. Checking the submission key here
	// closes that gap and prevents autosave from reopening a completed exam.
	if submission, checkErr := s.GetSubmission(ctx, examID, studentID); checkErr != nil {
		return nil, checkErr
	} else if submission != nil {
		return nil, ErrAttemptSubmitted
	}
	if attempt.Submitted {
		return nil, ErrAttemptSubmitted
	}
	if attempt.Version != expectedVersion {
		return nil, ErrAttemptConflict
	}
	value := *attempt
	value.Answers = cloneAnswers(answers)
	value.Version = attempt.Version + 1
	value.LastRequestID = requestID
	value.UpdatedAt = s.nowMS()
	value.UpdatedAtISO = iso(s.now())
	if value.AttemptNumber <= 0 {
		value.AttemptNumber = 1
	}
	item, err := marshal(value)
	if err != nil {
		return nil, err
	}
	transaction := []types.TransactWriteItem{{Put: &types.Put{
		TableName:                 aws.String(s.TableName),
		Item:                      item,
		ConditionExpression:       aws.String("version = :expected AND (attribute_not_exists(submitted) OR submitted = :false)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":expected": &types.AttributeValueMemberN{Value: strconv.Itoa(attempt.Version)}, ":false": &types.AttributeValueMemberBOOL{Value: false}},
	}}}
	transaction, fenced, fenceErr := appendFenceCheck(ctx, s.TableName, transaction)
	if fenceErr != nil {
		return nil, fenceErr
	}
	_, err = s.Client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: transaction})
	if err != nil {
		if fenced && isFenceTransactionFailure(err, len(transaction)-1) {
			return nil, clusterstate.ErrFenced
		}
		if isTransactionConditional(err) {
			current, readErr := s.GetStudentAttempt(ctx, examID, studentID)
			if readErr != nil {
				return nil, readErr
			}
			if current != nil && current.LastRequestID == requestID {
				return current, nil
			}
			if fenced && len(transaction) > 1 && isFenceTransactionFailure(err, len(transaction)-1) {
				return nil, clusterstate.ErrFenced
			}
			return nil, ErrAttemptConflict
		}
		return nil, err
	}
	return &value, nil
}

// FindSubmissionRequest returns the durable receipt only when this request ID
// is bound to the same student, exam and answer payload.
func (s *Store) FindSubmissionRequest(ctx context.Context, examID, studentID string, answers map[string]int, requestID string) (model.SubmissionResult, bool, error) {
	if strings.TrimSpace(requestID) == "" {
		return model.SubmissionResult{}, false, nil
	}
	item, err := s.getConsistent(ctx, submissionRequestKey(requestID))
	if err != nil || item == nil {
		return model.SubmissionResult{}, false, err
	}
	var receipt model.SubmissionRequest
	if err := unmarshal(item, &receipt); err != nil {
		return model.SubmissionResult{}, false, fmt.Errorf("decode submission receipt: %w", err)
	}
	if receipt.PayloadHash != submissionPayloadHash(examID, studentID, answers) {
		return model.SubmissionResult{}, false, ErrSubmissionRequestConflict
	}
	return receipt.Result("duplicate"), true, nil
}

// SubmitAttempt grades and atomically writes the canonical submission, its
// durable request-ID receipt, the downstream outbox record, and the submitted
// attempt state. The attempt write is conditional on the version read above so
// a concurrent save cannot be overwritten by this transaction.
func (s *Store) SubmitAttempt(ctx context.Context, examID, studentID, studentName string, answers map[string]int, requestID string) (model.SubmissionResult, error) {
	if _, err := s.requireMutationWriter(ctx); err != nil {
		return model.SubmissionResult{}, err
	}
	if requestID == "" {
		requestID = newID()
	}
	payloadHash := submissionPayloadHash(examID, studentID, answers)
	if duplicate, found, err := s.FindSubmissionRequest(ctx, examID, studentID, answers, requestID); err != nil || found {
		return duplicate, err
	}
	submissionKey := key("SUBMISSION#"+examID, "STUDENT#"+studentID)
	existing, err := s.getConsistent(ctx, submissionKey)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	if existing != nil {
		var old model.Submission
		if e := unmarshal(existing, &old); e != nil {
			return model.SubmissionResult{}, e
		}
		if old.RequestID == requestID {
			oldHash := old.PayloadHash
			if oldHash == "" {
				oldHash = submissionPayloadHash(examID, studentID, old.Answers)
			}
			if oldHash == payloadHash {
				return model.SubmissionResult{Status: "duplicate", RequestID: requestID, Version: old.Version, CommittedAt: old.UpdatedAt, Score: old.Score, Total: old.Total}, nil
			}
			return model.SubmissionResult{}, ErrSubmissionRequestConflict
		}
		return model.SubmissionResult{}, ErrSubmissionConflict
	}
	score, total, err := s.GradeAttempt(ctx, examID, answers)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	attempt, err := s.GetStudentAttempt(ctx, examID, studentID)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	if attempt != nil && attempt.Submitted {
		return model.SubmissionResult{}, ErrAttemptSubmitted
	}
	attemptExisted := attempt != nil
	now := s.now()
	committedAt := now.UnixMilli()
	attemptNumber := 1
	if attempt == nil {
		attempt = &model.Attempt{PK: "ATTEMPT#" + examID, SK: "STUDENT#" + studentID, AttemptNumber: 1, StartedAt: committedAt, StartedAtISO: iso(now)}
	} else {
		attempt = cloneAttempt(attempt)
		if attempt.AttemptNumber > 0 {
			attemptNumber = attempt.AttemptNumber
		}
	}
	if attempt.AttemptNumber <= 0 {
		attempt.AttemptNumber = 1
	}
	attempt.Answers = cloneAnswers(answers)
	attempt.Version++
	attempt.Submitted = true
	attempt.SubmittedAt = committedAt
	attempt.UpdatedAt = committedAt
	attempt.UpdatedAtISO = iso(now)
	attemptItem, err := marshal(attempt)
	if err != nil {
		return model.SubmissionResult{}, err
	}

	sub := model.Submission{PK: "SUBMISSION#" + examID, SK: "STUDENT#" + studentID, Answers: cloneAnswers(answers), Version: 1, RequestID: requestID, PayloadHash: payloadHash, UpdatedAt: committedAt, UpdatedAtISO: iso(now), WrittenByRegion: s.Region, StudentName: studentName, AttemptNumber: attemptNumber, Score: score, Total: total}
	subItem, err := marshal(sub)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	requestKey := submissionRequestKey(requestID)
	requestPK := requestKey["pk"].(*types.AttributeValueMemberS).Value
	receipt := model.SubmissionRequest{PK: requestPK, SK: "RECEIPT", RequestID: requestID, PayloadHash: payloadHash, ExamID: examID, StudentID: studentID, Version: 1, CommittedAt: committedAt, Score: score, Total: total, AttemptNumber: attemptNumber}
	receiptItem, err := marshal(receipt)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	journal := model.OutboxEntry{PK: "JOURNAL#" + requestID, SK: "OUTBOX", TargetPK: sub.PK, TargetSK: sub.SK, Version: 1, RequestID: requestID, CreatedAt: committedAt, Answers: cloneAnswers(answers), StudentName: studentName, AttemptNumber: attemptNumber, Score: score, Total: total, TTL: now.Unix() + 7*24*3600, Processed: false}
	journalItem, err := marshal(journal)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	attemptPut := &types.Put{TableName: aws.String(s.TableName), Item: attemptItem}
	if !attemptExisted {
		attemptPut.ConditionExpression = aws.String("attribute_not_exists(pk)")
	} else {
		previousVersion := attempt.Version - 1
		attemptPut.ConditionExpression = aws.String("version = :version AND (attribute_not_exists(submitted) OR submitted = :false)")
		attemptPut.ExpressionAttributeValues = map[string]types.AttributeValue{
			":version": &types.AttributeValueMemberN{Value: strconv.Itoa(previousVersion)},
			":false":   &types.AttributeValueMemberBOOL{Value: false},
		}
	}
	transaction := []types.TransactWriteItem{
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: subItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: receiptItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: journalItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: attemptPut},
	}
	transaction, fenced, fenceErr := appendFenceCheck(ctx, s.TableName, transaction)
	if fenceErr != nil {
		return model.SubmissionResult{}, fenceErr
	}
	_, err = s.Client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: transaction})
	if err != nil {
		if fenced && isFenceTransactionFailure(err, len(transaction)-1) {
			return model.SubmissionResult{}, clusterstate.ErrFenced
		}
		if isTransactionConditional(err) {
			if duplicate, found, readErr := s.FindSubmissionRequest(ctx, examID, studentID, answers, requestID); readErr != nil || found {
				return duplicate, readErr
			}
			winner, readErr := s.getConsistent(ctx, submissionKey)
			if readErr != nil {
				return model.SubmissionResult{}, readErr
			}
			if winner != nil {
				var old model.Submission
				if decodeErr := unmarshal(winner, &old); decodeErr != nil {
					return model.SubmissionResult{}, decodeErr
				}
				if old.RequestID == requestID {
					oldHash := old.PayloadHash
					if oldHash == "" {
						oldHash = submissionPayloadHash(examID, studentID, old.Answers)
					}
					if oldHash == payloadHash {
						return model.SubmissionResult{Status: "duplicate", RequestID: requestID, Version: old.Version, CommittedAt: old.UpdatedAt, Score: old.Score, Total: old.Total, AttemptNumber: old.AttemptNumber}, nil
					}
					return model.SubmissionResult{}, ErrSubmissionRequestConflict
				}
				return model.SubmissionResult{}, ErrSubmissionConflict
			}
			if fenced && !hasTransactionCancellationReasons(err) {
				return model.SubmissionResult{}, clusterstate.ErrFenced
			}
			current, readErr := s.GetStudentAttempt(ctx, examID, studentID)
			if readErr != nil {
				return model.SubmissionResult{}, readErr
			}
			if current != nil && current.Submitted {
				return model.SubmissionResult{}, ErrAttemptSubmitted
			}
			return model.SubmissionResult{}, ErrAttemptConflict
		}
		return model.SubmissionResult{}, err
	}
	return model.SubmissionResult{Status: "committed", RequestID: requestID, Version: 1, CommittedAt: committedAt, Score: score, Total: total, AttemptNumber: attemptNumber}, nil
}

func (s *Store) GetSubmission(ctx context.Context, examID, studentID string) (*model.Submission, error) {
	item, err := s.getConsistent(ctx, key("SUBMISSION#"+examID, "STUDENT#"+studentID))
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

// HeartbeatProbe writes a disposable health canary so standby nodes can verify
// DynamoDB reachability. It is operational telemetry, not user-facing exam
// state, and intentionally remains available without an active writer epoch.
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
	fencedWriter, err := s.requireMutationWriter(ctx)
	if err != nil {
		return 0, err
	}
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{TableName: aws.String(s.TableName), KeyConditionExpression: aws.String("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "EXAM#" + examID}}})
	if err != nil {
		return 0, err
	}
	count := 0
	chunkSize := 25
	if fencedWriter {
		chunkSize = 99
	}
	for start := 0; start < len(items); start += chunkSize {
		end := min(start+chunkSize, len(items))
		if fencedWriter {
			transaction := make([]types.TransactWriteItem, 0, end-start)
			for _, item := range items[start:end] {
				transaction = append(transaction, types.TransactWriteItem{Delete: &types.Delete{TableName: aws.String(s.TableName), Key: map[string]types.AttributeValue{"pk": item["pk"], "sk": item["sk"]}}})
			}
			if err := s.transactWrite(ctx, transaction); err != nil {
				return count, err
			}
			count += len(transaction)
			continue
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

type classroomIndexItem struct {
	PK          string `dynamodbav:"pk"`
	SK          string `dynamodbav:"sk"`
	ClassroomID string `dynamodbav:"classroom_id"`
}

func normaliseClassroomCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

func classroomMetaKey(id string) map[string]types.AttributeValue {
	return key("CLASSROOM#"+id, "META")
}

func classroomMemberKey(classroomID, userID string) map[string]types.AttributeValue {
	return key("CLASSROOM#"+classroomID, "MEMBER#"+userID)
}

func classroomCodeKey(code string) map[string]types.AttributeValue {
	return key("CLASSROOM_CODE#"+normaliseClassroomCode(code), "CLASSROOM")
}

func classroomOwnerIndexKey(ownerID, classroomID string) map[string]types.AttributeValue {
	return key("CLASSROOM_OWNER#"+ownerID, "CLASSROOM#"+classroomID)
}

func classroomMemberIndexKey(userID, classroomID string) map[string]types.AttributeValue {
	return key("CLASSROOM_MEMBER#"+userID, "CLASSROOM#"+classroomID)
}

// CreateClassroom writes the classroom and its owner/code lookup records as a
// single transaction. The lookup records keep list and join operations out of
// table-wide scans while remaining compatible with the existing single-table
// layout.
func (s *Store) CreateClassroom(ctx context.Context, classroom *model.Classroom) error {
	if classroom == nil {
		return fmt.Errorf("classroom is required")
	}
	v := *classroom
	v.ID = strings.TrimSpace(v.ID)
	v.OwnerID = strings.TrimSpace(v.OwnerID)
	v.Name = strings.TrimSpace(v.Name)
	v.JoinCode = normaliseClassroomCode(v.JoinCode)
	if v.ID == "" {
		v.ID = newID()
	}
	if v.PK == "" {
		v.PK = "CLASSROOM#" + v.ID
	}
	if v.SK == "" {
		v.SK = "META"
	}
	if v.CreatedAt == "" {
		v.CreatedAt = iso(s.now())
	}
	if v.UpdatedAt == "" {
		v.UpdatedAt = v.CreatedAt
	}
	if v.JoinCode == "" {
		return fmt.Errorf("classroom join code is required")
	}
	if v.MemberCount < 0 {
		v.MemberCount = 0
	}
	meta, err := marshal(v)
	if err != nil {
		return err
	}
	codeRef, err := marshal(classroomIndexItem{PK: "CLASSROOM_CODE#" + v.JoinCode, SK: "CLASSROOM", ClassroomID: v.ID})
	if err != nil {
		return err
	}
	ownerRef, err := marshal(classroomIndexItem{PK: "CLASSROOM_OWNER#" + v.OwnerID, SK: "CLASSROOM#" + v.ID, ClassroomID: v.ID})
	if err != nil {
		return err
	}
	err = s.transactWrite(ctx, []types.TransactWriteItem{
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: meta, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: codeRef, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: ownerRef, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	})
	if err != nil && isTransactionConditional(err) {
		// A generated ID collision and a join-code collision are both safe to
		// retry at the service layer. The latter is the useful public error.
		return ErrClassroomCodeTaken
	}
	if err == nil {
		*classroom = v
	}
	return err
}

// GetClassroom returns the metadata record without exposing DynamoDB keys in
// the API model.
func (s *Store) GetClassroom(ctx context.Context, classroomID string) (*model.Classroom, error) {
	item, err := s.get(ctx, classroomMetaKey(strings.TrimSpace(classroomID)))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, nil
	}
	var classroom model.Classroom
	if err := unmarshal(item, &classroom); err != nil {
		return nil, fmt.Errorf("decode classroom: %w", err)
	}
	classroom.ID = strings.TrimPrefix(classroom.PK, "CLASSROOM#")
	if classroom.JoinCode != "" {
		classroom.JoinCode = normaliseClassroomCode(classroom.JoinCode)
	}
	return &classroom, nil
}

func (s *Store) listClassroomRefs(ctx context.Context, partitionKey string) ([]classroomIndexItem, error) {
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{TableName: aws.String(s.TableName), KeyConditionExpression: aws.String("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: partitionKey}}})
	if err != nil {
		return nil, err
	}
	refs := make([]classroomIndexItem, 0, len(items))
	for _, item := range items {
		var ref classroomIndexItem
		if err := unmarshal(item, &ref); err != nil {
			return nil, err
		}
		if ref.ClassroomID != "" {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

func (s *Store) classroomsFromRefs(ctx context.Context, refs []classroomIndexItem, limit int) ([]model.Classroom, error) {
	rows := make([]model.Classroom, 0, len(refs))
	for _, ref := range refs {
		classroom, err := s.GetClassroom(ctx, ref.ClassroomID)
		if err != nil {
			return nil, err
		}
		if classroom == nil {
			continue // tolerate an index item left behind by an interrupted delete
		}
		rows = append(rows, *classroom)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].CreatedAt == rows[j].CreatedAt {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].CreatedAt < rows[j].CreatedAt
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (s *Store) ListClassroomsByOwner(ctx context.Context, ownerID string, limit int) ([]model.Classroom, error) {
	if limit <= 0 {
		limit = 50
	}
	refs, err := s.listClassroomRefs(ctx, "CLASSROOM_OWNER#"+strings.TrimSpace(ownerID))
	if err != nil {
		return nil, err
	}
	return s.classroomsFromRefs(ctx, refs, limit)
}

func (s *Store) ListClassroomsForMember(ctx context.Context, userID string, limit int) ([]model.Classroom, error) {
	if limit <= 0 {
		limit = 50
	}
	refs, err := s.listClassroomRefs(ctx, "CLASSROOM_MEMBER#"+strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	return s.classroomsFromRefs(ctx, refs, limit)
}

func (s *Store) FindClassroomByJoinCode(ctx context.Context, joinCode string) (*model.Classroom, error) {
	item, err := s.get(ctx, classroomCodeKey(joinCode))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, nil
	}
	var ref classroomIndexItem
	if err := unmarshal(item, &ref); err != nil {
		return nil, err
	}
	if ref.ClassroomID == "" {
		return nil, nil
	}
	return s.GetClassroom(ctx, ref.ClassroomID)
}

func (s *Store) UpdateClassroom(ctx context.Context, classroomID, ownerID string, patch model.ClassroomPatch) (*model.Classroom, error) {
	classroom, err := s.GetClassroom(ctx, classroomID)
	if err != nil {
		return nil, err
	}
	if classroom == nil {
		return nil, ErrNotFound
	}
	if classroom.OwnerID != strings.TrimSpace(ownerID) {
		return nil, ErrClassroomForbidden
	}
	parts := make([]string, 0, 3)
	names := map[string]string{"#owner_id": "owner_id", "#updated_at": "updated_at"}
	values := map[string]types.AttributeValue{
		":owner_id":   &types.AttributeValueMemberS{Value: classroom.OwnerID},
		":updated_at": &types.AttributeValueMemberS{Value: iso(s.now())},
	}
	if patch.Name != nil {
		parts = append(parts, "#name = :name")
		names["#name"] = "name"
		values[":name"] = &types.AttributeValueMemberS{Value: strings.TrimSpace(*patch.Name)}
	}
	if patch.Description != nil {
		parts = append(parts, "#description = :description")
		names["#description"] = "description"
		values[":description"] = &types.AttributeValueMemberS{Value: *patch.Description}
	}
	if len(parts) == 0 {
		return classroom, nil
	}
	parts = append(parts, "#updated_at = :updated_at")
	out, err := s.fencedUpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(s.TableName),
		Key:                       classroomMetaKey(classroom.ID),
		UpdateExpression:          aws.String("SET " + strings.Join(parts, ", ")),
		ConditionExpression:       aws.String("#owner_id = :owner_id"),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
		ReturnValues:              types.ReturnValueAllNew,
	})
	if err != nil {
		if isConditional(err) {
			return nil, ErrClassroomForbidden
		}
		return nil, err
	}
	var updated model.Classroom
	if err := unmarshal(out.Attributes, &updated); err != nil {
		return nil, err
	}
	updated.ID = classroom.ID
	return &updated, nil
}

func (s *Store) DeleteClassroom(ctx context.Context, classroomID, ownerID string) error {
	classroom, err := s.GetClassroom(ctx, classroomID)
	if err != nil {
		return err
	}
	if classroom == nil {
		return ErrNotFound
	}
	if classroom.OwnerID != strings.TrimSpace(ownerID) {
		return ErrClassroomForbidden
	}
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{TableName: aws.String(s.TableName), KeyConditionExpression: aws.String("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "CLASSROOM#" + classroom.ID}}})
	if err != nil {
		return err
	}
	keys := make([]map[string]types.AttributeValue, 0, len(items)+3)
	seen := make(map[string]struct{})
	addKey := func(k map[string]types.AttributeValue) {
		pk := ""
		sk := ""
		if value, ok := k["pk"].(*types.AttributeValueMemberS); ok {
			pk = value.Value
		}
		if value, ok := k["sk"].(*types.AttributeValueMemberS); ok {
			sk = value.Value
		}
		keyID := pk + "\x00" + sk
		if pk == "" || sk == "" {
			return
		}
		if _, ok := seen[keyID]; ok {
			return
		}
		seen[keyID] = struct{}{}
		keys = append(keys, k)
	}
	addKey(classroomMetaKey(classroom.ID))
	addKey(classroomCodeKey(classroom.JoinCode))
	addKey(classroomOwnerIndexKey(classroom.OwnerID, classroom.ID))
	for _, item := range items {
		addKey(map[string]types.AttributeValue{"pk": item["pk"], "sk": item["sk"]})
		var member model.ClassroomMember
		if err := unmarshal(item, &member); err == nil && member.UserID != "" {
			addKey(classroomMemberIndexKey(member.UserID, classroom.ID))
		}
	}
	return s.batchDeleteKeys(ctx, keys)
}

func (s *Store) batchDeleteKeys(ctx context.Context, keys []map[string]types.AttributeValue) error {
	fenced, err := s.requireMutationWriter(ctx)
	if err != nil {
		return err
	}
	chunkSize := 25
	if fenced {
		chunkSize = 99
	}
	for start := 0; start < len(keys); start += chunkSize {
		end := min(start+chunkSize, len(keys))
		if fenced {
			transaction := make([]types.TransactWriteItem, 0, end-start)
			for _, itemKey := range keys[start:end] {
				transaction = append(transaction, types.TransactWriteItem{Delete: &types.Delete{TableName: aws.String(s.TableName), Key: itemKey}})
			}
			if err := s.transactWrite(ctx, transaction); err != nil {
				return err
			}
			continue
		}
		pending := make([]types.WriteRequest, 0, end-start)
		for _, itemKey := range keys[start:end] {
			pending = append(pending, types.WriteRequest{DeleteRequest: &types.DeleteRequest{Key: itemKey}})
		}
		for attempt := 0; len(pending) > 0; attempt++ {
			out, err := s.Client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{RequestItems: map[string][]types.WriteRequest{s.TableName: pending}})
			if err != nil {
				return err
			}
			pending = out.UnprocessedItems[s.TableName]
			if len(pending) == 0 {
				break
			}
			delay := 50 * time.Millisecond * time.Duration(1<<min(attempt, 6))
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil
}

func (s *Store) AddClassroomMember(ctx context.Context, member *model.ClassroomMember) error {
	if member == nil {
		return fmt.Errorf("classroom member is required")
	}
	classroom, err := s.GetClassroom(ctx, member.ClassroomID)
	if err != nil {
		return err
	}
	if classroom == nil {
		return ErrNotFound
	}
	userID := strings.TrimSpace(member.UserID)
	if userID == "" {
		return fmt.Errorf("classroom member user id is required")
	}
	existing, err := s.get(ctx, classroomMemberKey(classroom.ID, userID))
	if err != nil {
		return err
	}
	if existing != nil {
		return ErrClassroomMember
	}
	v := *member
	v.ClassroomID = classroom.ID
	v.UserID = userID
	v.PK = "CLASSROOM#" + classroom.ID
	v.SK = "MEMBER#" + userID
	if v.Role == "" {
		v.Role = "student"
	}
	if v.JoinedAt == "" {
		v.JoinedAt = iso(s.now())
	}
	memberItem, err := marshal(v)
	if err != nil {
		return err
	}
	memberRef, err := marshal(classroomIndexItem{PK: "CLASSROOM_MEMBER#" + userID, SK: "CLASSROOM#" + classroom.ID, ClassroomID: classroom.ID})
	if err != nil {
		return err
	}
	updatedAt := iso(s.now())
	err = s.transactWrite(ctx, []types.TransactWriteItem{
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: memberItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: memberRef, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Update: &types.Update{TableName: aws.String(s.TableName), Key: classroomMetaKey(classroom.ID), UpdateExpression: aws.String("SET #member_count = if_not_exists(#member_count, :zero) + :one, #updated_at = :updated_at"), ConditionExpression: aws.String("attribute_exists(pk)"), ExpressionAttributeNames: map[string]string{"#member_count": "member_count", "#updated_at": "updated_at"}, ExpressionAttributeValues: map[string]types.AttributeValue{":zero": &types.AttributeValueMemberN{Value: "0"}, ":one": &types.AttributeValueMemberN{Value: "1"}, ":updated_at": &types.AttributeValueMemberS{Value: updatedAt}}}},
	})
	if err != nil && isTransactionConditional(err) {
		return ErrClassroomMember
	}
	if err == nil {
		*member = v
	}
	return err
}

func (s *Store) ListClassroomMembers(ctx context.Context, classroomID string) ([]model.ClassroomMember, error) {
	classroom, err := s.GetClassroom(ctx, classroomID)
	if err != nil {
		return nil, err
	}
	if classroom == nil {
		return nil, ErrNotFound
	}
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{TableName: aws.String(s.TableName), KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :member)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "CLASSROOM#" + classroom.ID}, ":member": &types.AttributeValueMemberS{Value: "MEMBER#"}}})
	if err != nil {
		return nil, err
	}
	rows := make([]model.ClassroomMember, 0, len(items))
	for _, item := range items {
		var member model.ClassroomMember
		if err := unmarshal(item, &member); err != nil {
			return nil, err
		}
		member.ClassroomID = classroom.ID
		rows = append(rows, member)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].JoinedAt == rows[j].JoinedAt {
			return rows[i].UserID < rows[j].UserID
		}
		return rows[i].JoinedAt < rows[j].JoinedAt
	})
	return rows, nil
}

func (s *Store) IsClassroomMember(ctx context.Context, classroomID, userID string) (bool, error) {
	item, err := s.get(ctx, classroomMemberKey(strings.TrimSpace(classroomID), strings.TrimSpace(userID)))
	if err != nil {
		return false, err
	}
	return item != nil, nil
}

func (s *Store) RemoveClassroomMember(ctx context.Context, classroomID, ownerID, userID string) error {
	classroom, err := s.GetClassroom(ctx, classroomID)
	if err != nil {
		return err
	}
	if classroom == nil {
		return ErrNotFound
	}
	if classroom.OwnerID != strings.TrimSpace(ownerID) {
		return ErrClassroomForbidden
	}
	userID = strings.TrimSpace(userID)
	memberItem, err := s.get(ctx, classroomMemberKey(classroom.ID, userID))
	if err != nil {
		return err
	}
	if memberItem == nil {
		return ErrClassroomMemberNotFound
	}
	err = s.transactWrite(ctx, []types.TransactWriteItem{
		{Delete: &types.Delete{TableName: aws.String(s.TableName), Key: classroomMemberKey(classroom.ID, userID), ConditionExpression: aws.String("attribute_exists(pk)")}},
		{Delete: &types.Delete{TableName: aws.String(s.TableName), Key: classroomMemberIndexKey(userID, classroom.ID)}},
		{Update: &types.Update{TableName: aws.String(s.TableName), Key: classroomMetaKey(classroom.ID), UpdateExpression: aws.String("SET #member_count = if_not_exists(#member_count, :zero) - :one, #updated_at = :updated_at"), ConditionExpression: aws.String("attribute_exists(pk)"), ExpressionAttributeNames: map[string]string{"#member_count": "member_count", "#updated_at": "updated_at"}, ExpressionAttributeValues: map[string]types.AttributeValue{":zero": &types.AttributeValueMemberN{Value: "0"}, ":one": &types.AttributeValueMemberN{Value: "1"}, ":updated_at": &types.AttributeValueMemberS{Value: iso(s.now())}}}},
	})
	if err != nil && isTransactionConditional(err) {
		return ErrClassroomMemberNotFound
	}
	return err
}

// MemoryStore is a concurrency-safe in-memory implementation of the store
// contract useful for local development and service tests. It deliberately
// uses the same logical keys and result semantics as DynamoDBStore.
type MemoryStore struct {
	mu                 sync.RWMutex
	guardMu            sync.RWMutex
	fenceGuard         clusterstate.FenceGuard
	users              map[string]*model.User
	emails             map[string]string
	exams              map[string]*model.Exam
	classrooms         map[string]*model.Classroom
	classroomCodes     map[string]string
	classroomMembers   map[string]map[string]*model.ClassroomMember
	drafts             map[string]*model.QuizDraft
	attempts           map[string]*model.Attempt
	submissions        map[string]*model.Submission
	submissionRequests map[string]memorySubmissionReceipt
	outbox             []model.OutboxEntry
	auditSummaries     map[string]model.AuditSummary
	now                func() time.Time
	region             string
}

type memorySubmissionReceipt struct {
	payloadHash string
	examID      string
	studentID   string
	result      model.SubmissionResult
}

type bootstrapWriteKey struct{}

// WithBootstrap marks the small set of startup writes that are safe to run
// before a routing writer exists. Callers must keep these writes conditional
// and idempotent; ordinary API writes never receive this marker.
func WithBootstrap(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, bootstrapWriteKey{}, true)
}

func isBootstrapWrite(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(bootstrapWriteKey{}).(bool)
	return value
}

func NewMemory(region string) *MemoryStore {
	return &MemoryStore{
		users:              map[string]*model.User{},
		emails:             map[string]string{},
		exams:              map[string]*model.Exam{},
		classrooms:         map[string]*model.Classroom{},
		classroomCodes:     map[string]string{},
		classroomMembers:   map[string]map[string]*model.ClassroomMember{},
		drafts:             map[string]*model.QuizDraft{},
		attempts:           map[string]*model.Attempt{},
		submissions:        map[string]*model.Submission{},
		submissionRequests: map[string]memorySubmissionReceipt{},
		auditSummaries:     map[string]model.AuditSummary{},
		now:                time.Now,
		region:             region,
	}
}

// SetFenceGuard installs the control-plane guard used to serialize in-memory
// exam mutations with promotion. A configured guard requires every writer to
// carry a validated backend and epoch in its context.
func (m *MemoryStore) SetFenceGuard(guard clusterstate.FenceGuard) {
	m.guardMu.Lock()
	m.fenceGuard = guard
	m.guardMu.Unlock()
}

func (m *MemoryStore) withFence(ctx context.Context, fn func() error) error {
	m.guardMu.RLock()
	guard := m.fenceGuard
	m.guardMu.RUnlock()
	writer, hasWriter, writerErr := writerFromContext(ctx)
	if writerErr != nil {
		return writerErr
	}
	if guard == nil {
		if hasWriter {
			return clusterstate.ErrFenced
		}
		return fn()
	}
	if !hasWriter {
		if isBootstrapWrite(ctx) {
			return fn()
		}
		return clusterstate.ErrFenced
	}
	return guard.WithFence(ctx, writer, fn)
}

func withMemoryFence[T any](m *MemoryStore, ctx context.Context, fn func() (T, error)) (T, error) {
	var result T
	err := m.withFence(ctx, func() error {
		var err error
		result, err = fn()
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return result, nil
}

func withMemoryFenceError(m *MemoryStore, ctx context.Context, fn func() error) error {
	return m.withFence(ctx, fn)
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
func (m *MemoryStore) CreateUser(ctx context.Context, u *model.User, hash string) error {
	return withMemoryFenceError(m, ctx, func() error { return m.createUser(u, hash) })
}

func (m *MemoryStore) createUser(u *model.User, hash string) error {
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

func cloneClassroom(classroom *model.Classroom) *model.Classroom {
	if classroom == nil {
		return nil
	}
	v := *classroom
	return &v
}

func cloneClassroomMember(member *model.ClassroomMember) *model.ClassroomMember {
	if member == nil {
		return nil
	}
	v := *member
	return &v
}

func (m *MemoryStore) CreateClassroom(ctx context.Context, classroom *model.Classroom) error {
	return withMemoryFenceError(m, ctx, func() error { return m.createClassroom(classroom) })
}

func (m *MemoryStore) createClassroom(classroom *model.Classroom) error {
	if classroom == nil {
		return fmt.Errorf("classroom is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.classrooms == nil {
		m.classrooms = map[string]*model.Classroom{}
	}
	if m.classroomCodes == nil {
		m.classroomCodes = map[string]string{}
	}
	v := *classroom
	v.ID = strings.TrimSpace(v.ID)
	v.OwnerID = strings.TrimSpace(v.OwnerID)
	v.Name = strings.TrimSpace(v.Name)
	v.JoinCode = normaliseClassroomCode(v.JoinCode)
	if v.ID == "" {
		v.ID = newID()
	}
	if v.PK == "" {
		v.PK = "CLASSROOM#" + v.ID
	}
	if v.SK == "" {
		v.SK = "META"
	}
	if v.CreatedAt == "" {
		v.CreatedAt = iso(m.now())
	}
	if v.UpdatedAt == "" {
		v.UpdatedAt = v.CreatedAt
	}
	if v.JoinCode == "" {
		return fmt.Errorf("classroom join code is required")
	}
	if _, exists := m.classrooms[v.ID]; exists {
		return ErrClassroomCodeTaken
	}
	if _, exists := m.classroomCodes[v.JoinCode]; exists {
		return ErrClassroomCodeTaken
	}
	if v.MemberCount < 0 {
		v.MemberCount = 0
	}
	m.classrooms[v.ID] = cloneClassroom(&v)
	m.classroomCodes[v.JoinCode] = v.ID
	if m.classroomMembers == nil {
		m.classroomMembers = map[string]map[string]*model.ClassroomMember{}
	}
	if m.classroomMembers[v.ID] == nil {
		m.classroomMembers[v.ID] = map[string]*model.ClassroomMember{}
	}
	*classroom = v
	return nil
}

func (m *MemoryStore) GetClassroom(_ context.Context, classroomID string) (*model.Classroom, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneClassroom(m.classrooms[strings.TrimSpace(classroomID)]), nil
}

func (m *MemoryStore) listClassrooms(filter func(*model.Classroom) bool, limit int) []model.Classroom {
	rows := make([]model.Classroom, 0, len(m.classrooms))
	for _, classroom := range m.classrooms {
		if filter != nil && !filter(classroom) {
			continue
		}
		rows = append(rows, *classroom)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].CreatedAt == rows[j].CreatedAt {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].CreatedAt < rows[j].CreatedAt
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func (m *MemoryStore) ListClassroomsByOwner(_ context.Context, ownerID string, limit int) ([]model.Classroom, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	ownerID = strings.TrimSpace(ownerID)
	return m.listClassrooms(func(classroom *model.Classroom) bool { return classroom.OwnerID == ownerID }, limit), nil
}

func (m *MemoryStore) ListClassroomsForMember(_ context.Context, userID string, limit int) ([]model.Classroom, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	userID = strings.TrimSpace(userID)
	return m.listClassrooms(func(classroom *model.Classroom) bool {
		members := m.classroomMembers[classroom.ID]
		_, ok := members[userID]
		return ok
	}, limit), nil
}

func (m *MemoryStore) FindClassroomByJoinCode(_ context.Context, joinCode string) (*model.Classroom, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id := m.classroomCodes[normaliseClassroomCode(joinCode)]
	return cloneClassroom(m.classrooms[id]), nil
}

func (m *MemoryStore) UpdateClassroom(ctx context.Context, classroomID, ownerID string, patch model.ClassroomPatch) (*model.Classroom, error) {
	return withMemoryFence(m, ctx, func() (*model.Classroom, error) { return m.updateClassroom(classroomID, ownerID, patch) })
}

func (m *MemoryStore) updateClassroom(classroomID, ownerID string, patch model.ClassroomPatch) (*model.Classroom, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	classroom := m.classrooms[strings.TrimSpace(classroomID)]
	if classroom == nil {
		return nil, ErrNotFound
	}
	if classroom.OwnerID != strings.TrimSpace(ownerID) {
		return nil, ErrClassroomForbidden
	}
	if patch.Name != nil {
		classroom.Name = strings.TrimSpace(*patch.Name)
	}
	if patch.Description != nil {
		classroom.Description = *patch.Description
	}
	classroom.UpdatedAt = iso(m.now())
	return cloneClassroom(classroom), nil
}

func (m *MemoryStore) DeleteClassroom(ctx context.Context, classroomID, ownerID string) error {
	return withMemoryFenceError(m, ctx, func() error { return m.deleteClassroom(classroomID, ownerID) })
}

func (m *MemoryStore) deleteClassroom(classroomID, ownerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	classroom := m.classrooms[strings.TrimSpace(classroomID)]
	if classroom == nil {
		return ErrNotFound
	}
	if classroom.OwnerID != strings.TrimSpace(ownerID) {
		return ErrClassroomForbidden
	}
	delete(m.classrooms, classroom.ID)
	delete(m.classroomCodes, normaliseClassroomCode(classroom.JoinCode))
	delete(m.classroomMembers, classroom.ID)
	return nil
}

func (m *MemoryStore) AddClassroomMember(ctx context.Context, member *model.ClassroomMember) error {
	return withMemoryFenceError(m, ctx, func() error { return m.addClassroomMember(member) })
}

func (m *MemoryStore) addClassroomMember(member *model.ClassroomMember) error {
	if member == nil {
		return fmt.Errorf("classroom member is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	classroom := m.classrooms[strings.TrimSpace(member.ClassroomID)]
	if classroom == nil {
		return ErrNotFound
	}
	userID := strings.TrimSpace(member.UserID)
	if userID == "" {
		return fmt.Errorf("classroom member user id is required")
	}
	if m.classroomMembers[classroom.ID] == nil {
		m.classroomMembers[classroom.ID] = map[string]*model.ClassroomMember{}
	}
	if _, exists := m.classroomMembers[classroom.ID][userID]; exists {
		return ErrClassroomMember
	}
	v := *member
	v.ClassroomID = classroom.ID
	v.UserID = userID
	v.PK = "CLASSROOM#" + classroom.ID
	v.SK = "MEMBER#" + userID
	if v.Role == "" {
		v.Role = "student"
	}
	if v.JoinedAt == "" {
		v.JoinedAt = iso(m.now())
	}
	m.classroomMembers[classroom.ID][userID] = cloneClassroomMember(&v)
	classroom.MemberCount++
	classroom.UpdatedAt = iso(m.now())
	*member = v
	return nil
}

func (m *MemoryStore) ListClassroomMembers(_ context.Context, classroomID string) ([]model.ClassroomMember, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	classroomID = strings.TrimSpace(classroomID)
	if m.classrooms[classroomID] == nil {
		return nil, ErrNotFound
	}
	members := m.classroomMembers[classroomID]
	rows := make([]model.ClassroomMember, 0, len(members))
	for _, member := range members {
		rows = append(rows, *member)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].JoinedAt == rows[j].JoinedAt {
			return rows[i].UserID < rows[j].UserID
		}
		return rows[i].JoinedAt < rows[j].JoinedAt
	})
	return rows, nil
}

func (m *MemoryStore) IsClassroomMember(_ context.Context, classroomID, userID string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.classrooms[strings.TrimSpace(classroomID)] == nil {
		return false, nil
	}
	_, ok := m.classroomMembers[strings.TrimSpace(classroomID)][strings.TrimSpace(userID)]
	return ok, nil
}

func (m *MemoryStore) RemoveClassroomMember(ctx context.Context, classroomID, ownerID, userID string) error {
	return withMemoryFenceError(m, ctx, func() error { return m.removeClassroomMember(classroomID, ownerID, userID) })
}

func (m *MemoryStore) removeClassroomMember(classroomID, ownerID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	classroom := m.classrooms[strings.TrimSpace(classroomID)]
	if classroom == nil {
		return ErrNotFound
	}
	if classroom.OwnerID != strings.TrimSpace(ownerID) {
		return ErrClassroomForbidden
	}
	userID = strings.TrimSpace(userID)
	members := m.classroomMembers[classroom.ID]
	if members == nil || members[userID] == nil {
		return ErrClassroomMemberNotFound
	}
	delete(members, userID)
	if classroom.MemberCount > 0 {
		classroom.MemberCount--
	}
	classroom.UpdatedAt = iso(m.now())
	return nil
}

func (m *MemoryStore) EnsureExam(ctx context.Context, id, title string, qs []model.Question, w map[string]any) error {
	return withMemoryFenceError(m, ctx, func() error { return m.ensureExam(id, title, qs, w) })
}

func (m *MemoryStore) ensureExam(id, title string, qs []model.Question, w map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureExamLocked(id, title, qs, w)
	return nil
}

// EnsureExamIfMissing mirrors the atomic create-only behavior of the
// DynamoDB implementation for the local harness.
func (m *MemoryStore) EnsureExamIfMissing(ctx context.Context, id, title string, qs []model.Question, w map[string]any) error {
	return withMemoryFenceError(m, ctx, func() error { return m.ensureExamIfMissing(id, title, qs, w) })
}

func (m *MemoryStore) ensureExamIfMissing(id, title string, qs []model.Question, w map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.exams[id] != nil {
		return nil
	}
	m.ensureExamLocked(id, title, qs, w)
	return nil
}

func (m *MemoryStore) ensureExamLocked(id, title string, qs []model.Question, w map[string]any) {
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
		case "owner_id":
			v.OwnerID = fmt.Sprint(val)
		case "status":
			v.Status = fmt.Sprint(val)
		case "max_attempts":
			v.MaxAttempts = toInt(val)
		case "allow_late":
			v.AllowLate = toBool(val)
		case "late_grace_minutes":
			v.LateGraceMinutes = toInt(val)
		}
	}
	for i, q := range qs {
		q.Index = i
		v.Questions = append(v.Questions, q)
	}
	m.exams[id] = v
}

func toBool(v any) bool {
	switch value := v.(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true") || strings.TrimSpace(value) == "1"
	default:
		return false
	}
}

func (m *MemoryStore) CreateQuizDraft(ctx context.Context, draft *model.QuizDraft) (*model.QuizDraft, error) {
	return withMemoryFence(m, ctx, func() (*model.QuizDraft, error) { return m.createQuizDraft(draft) })
}

func (m *MemoryStore) createQuizDraft(draft *model.QuizDraft) (*model.QuizDraft, error) {
	if draft == nil || strings.TrimSpace(draft.ID) == "" || strings.TrimSpace(draft.OwnerID) == "" {
		return nil, fmt.Errorf("quiz draft id and owner are required")
	}
	if strings.TrimSpace(draft.Title) == "" {
		return nil, fmt.Errorf("quiz draft title is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.drafts == nil {
		m.drafts = map[string]*model.QuizDraft{}
	}
	if _, exists := m.drafts[draft.ID]; exists {
		return nil, ErrQuizConflict
	}
	if _, exists := m.exams[draft.ID]; exists {
		return nil, ErrQuizConflict
	}
	now := iso(m.now())
	value := *draft
	value.ID = strings.TrimSpace(value.ID)
	value.PK = "QUIZ#" + value.ID
	value.SK = "META"
	value.Title = strings.TrimSpace(value.Title)
	value.Status = "draft"
	value.QuestionCount = len(value.Questions)
	if value.CreatedAt == "" {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	value.Questions = cloneQuestions(value.Questions)
	m.drafts[value.ID] = cloneDraft(&value)
	return cloneDraft(&value), nil
}

func (m *MemoryStore) GetQuizDraft(_ context.Context, id string) (*model.QuizDraft, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneDraft(m.drafts[strings.TrimSpace(id)]), nil
}

func (m *MemoryStore) ListQuizDrafts(_ context.Context, ownerID string, limit int) ([]model.QuizDraft, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	rows := make([]model.QuizDraft, 0, len(m.drafts))
	for _, draft := range m.drafts {
		if draft.Status != "draft" || (ownerID != "" && ownerID != "admin" && draft.OwnerID != ownerID) {
			continue
		}
		rows = append(rows, *cloneDraft(draft))
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].UpdatedAt == rows[j].UpdatedAt {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].UpdatedAt > rows[j].UpdatedAt
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (m *MemoryStore) UpdateQuizDraft(ctx context.Context, ownerID string, draft *model.QuizDraft) (*model.QuizDraft, error) {
	return withMemoryFence(m, ctx, func() (*model.QuizDraft, error) { return m.updateQuizDraft(ownerID, draft) })
}

func (m *MemoryStore) updateQuizDraft(ownerID string, draft *model.QuizDraft) (*model.QuizDraft, error) {
	if draft == nil {
		return nil, fmt.Errorf("quiz draft is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.drafts[strings.TrimSpace(draft.ID)]
	if current == nil {
		return nil, ErrQuizNotFound
	}
	if !ownerCanEdit(ownerID, current.OwnerID) {
		return nil, ErrQuizForbidden
	}
	if current.Status != "draft" {
		return nil, ErrQuizConflict
	}
	value := *draft
	value.ID, value.PK, value.SK = current.ID, current.PK, current.SK
	value.OwnerID, value.CreatedAt, value.Status = current.OwnerID, current.CreatedAt, "draft"
	value.Title = strings.TrimSpace(value.Title)
	if value.Title == "" {
		return nil, fmt.Errorf("quiz draft title is required")
	}
	value.QuestionCount = len(value.Questions)
	value.UpdatedAt = iso(m.now())
	value.Questions = cloneQuestions(value.Questions)
	m.drafts[value.ID] = cloneDraft(&value)
	return cloneDraft(&value), nil
}

func (m *MemoryStore) DeleteQuizDraft(ctx context.Context, id, ownerID string) (bool, error) {
	return withMemoryFence(m, ctx, func() (bool, error) { return m.deleteQuizDraft(id, ownerID) })
}

func (m *MemoryStore) deleteQuizDraft(id, ownerID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	draft := m.drafts[strings.TrimSpace(id)]
	if draft == nil {
		return false, nil
	}
	if !ownerCanEdit(ownerID, draft.OwnerID) {
		return false, ErrQuizForbidden
	}
	if draft.Status != "draft" {
		return false, ErrQuizConflict
	}
	delete(m.drafts, draft.ID)
	return true, nil
}

func (m *MemoryStore) PublishQuizDraft(ctx context.Context, id, ownerID string) (*model.ExamMeta, error) {
	return withMemoryFence(m, ctx, func() (*model.ExamMeta, error) { return m.publishQuizDraft(id, ownerID) })
}

func (m *MemoryStore) publishQuizDraft(id, ownerID string) (*model.ExamMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	draft := m.drafts[strings.TrimSpace(id)]
	if draft == nil {
		return nil, ErrQuizNotFound
	}
	if !ownerCanEdit(ownerID, draft.OwnerID) {
		return nil, ErrQuizForbidden
	}
	if draft.Status != "draft" {
		return nil, ErrQuizConflict
	}
	if len(draft.Questions) == 0 {
		return nil, fmt.Errorf("at least one question is required to publish")
	}
	if _, exists := m.exams[draft.ID]; exists {
		return nil, ErrQuizConflict
	}
	meta := model.ExamMeta{PK: "EXAM#" + draft.ID, SK: "META", ID: draft.ID, Title: draft.Title, CreatedAt: iso(m.now()), OwnerID: draft.OwnerID, Status: "published", QuestionCount: len(draft.Questions), OpensAt: draft.OpensAt, ClosesAt: draft.ClosesAt, DurationMinutes: draft.DurationMinutes, MaxAttempts: draft.MaxAttempts, AllowLate: draft.AllowLate, LateGraceMinutes: draft.LateGraceMinutes}
	m.exams[draft.ID] = &model.Exam{ExamMeta: meta, Questions: cloneQuestions(draft.Questions)}
	draft.Status = "published"
	draft.PublishedAt = iso(m.now())
	draft.UpdatedAt = draft.PublishedAt
	return &meta, nil
}

func (m *MemoryStore) DuplicateQuiz(ctx context.Context, sourceID, ownerID, requestedID, title string) (*model.QuizDraft, error) {
	draft, _ := m.GetQuizDraft(ctx, sourceID)
	var sourceTitle string
	var sourceQuestions []model.Question
	var opensAt, closesAt string
	var duration, maxAttempts, lateGrace int
	var allowLate bool
	if draft != nil {
		if !ownerCanEdit(ownerID, draft.OwnerID) {
			return nil, ErrQuizForbidden
		}
		sourceTitle, sourceQuestions, opensAt, closesAt = draft.Title, draft.Questions, draft.OpensAt, draft.ClosesAt
		duration, maxAttempts, allowLate, lateGrace = draft.DurationMinutes, draft.MaxAttempts, draft.AllowLate, draft.LateGraceMinutes
	} else {
		exam, _ := m.GetExam(ctx, sourceID, true)
		if exam == nil {
			return nil, ErrQuizNotFound
		}
		if !ownerCanEdit(ownerID, exam.OwnerID) {
			return nil, ErrQuizForbidden
		}
		sourceTitle, sourceQuestions, opensAt, closesAt = exam.Title, exam.Questions, exam.OpensAt, exam.ClosesAt
		duration, maxAttempts, allowLate, lateGrace = exam.DurationMinutes, exam.MaxAttempts, exam.AllowLate, exam.LateGraceMinutes
	}
	newQuizID := strings.TrimSpace(requestedID)
	if newQuizID == "" {
		newQuizID = "quiz-" + newID()
	}
	if strings.TrimSpace(title) == "" {
		title = "Copy of " + sourceTitle
	}
	return m.CreateQuizDraft(ctx, &model.QuizDraft{ID: newQuizID, OwnerID: ownerID, Title: title, Questions: cloneQuestions(sourceQuestions), OpensAt: opensAt, ClosesAt: closesAt, DurationMinutes: duration, MaxAttempts: maxAttempts, AllowLate: allowLate, LateGraceMinutes: lateGrace})
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
			v.Questions[i].Feedback = ""
		}
	}
	return &v, nil
}

func (m *MemoryStore) UpdateExamWindow(ctx context.Context, id string, title *string, window map[string]any) (*model.ExamMeta, error) {
	return withMemoryFence(m, ctx, func() (*model.ExamMeta, error) { return m.updateExamWindow(id, title, window) })
}

func (m *MemoryStore) updateExamWindow(id string, title *string, window map[string]any) (*model.ExamMeta, error) {
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
		case "owner_id":
			e.OwnerID = fmt.Sprint(val)
		case "status":
			e.Status = fmt.Sprint(val)
		case "max_attempts":
			e.MaxAttempts = toInt(val)
		case "allow_late":
			e.AllowLate = toBool(val)
		case "late_grace_minutes":
			e.LateGraceMinutes = toInt(val)
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

func (m *MemoryStore) DeleteExam(ctx context.Context, id string) (int, error) {
	return withMemoryFence(m, ctx, func() (int, error) { return m.deleteExam(id) })
}

func (m *MemoryStore) deleteExam(id string) (int, error) {
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
func (m *MemoryStore) RecordAttemptStart(ctx context.Context, e, id string) (*model.Attempt, error) {
	var result *model.Attempt
	err := m.withFence(ctx, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.attempts == nil {
			m.attempts = map[string]*model.Attempt{}
		}
		k := e + "/" + id
		if a := m.attempts[k]; a != nil {
			result = cloneAttempt(a)
			return nil
		}
		t := m.now()
		a := &model.Attempt{PK: "ATTEMPT#" + e, SK: "STUDENT#" + id, AttemptNumber: 1, StartedAt: t.UnixMilli(), StartedAtISO: iso(t)}
		m.attempts[k] = a
		result = cloneAttempt(a)
		return nil
	})
	return result, err
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

func (m *MemoryStore) GetStudentAttempt(_ context.Context, examID, studentID string) (*model.Attempt, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	attempt := m.attempts[examID+"/"+studentID]
	if attempt == nil {
		return nil, nil
	}
	return cloneAttempt(attempt), nil
}

func (m *MemoryStore) SaveAttempt(ctx context.Context, examID, studentID string, answers map[string]int, expectedVersion int, requestID string) (*model.Attempt, error) {
	if requestID == "" {
		return nil, fmt.Errorf("request_id is required")
	}
	var result *model.Attempt
	err := m.withFence(ctx, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.attempts == nil {
			m.attempts = map[string]*model.Attempt{}
		}
		key := examID + "/" + studentID
		attempt := m.attempts[key]
		if attempt == nil {
			now := m.now()
			attempt = &model.Attempt{PK: "ATTEMPT#" + examID, SK: "STUDENT#" + studentID, AttemptNumber: 1, StartedAt: now.UnixMilli(), StartedAtISO: iso(now)}
			m.attempts[key] = attempt
		}
		if attempt.LastRequestID == requestID {
			result = cloneAttempt(attempt)
			return nil
		}
		if _, exists := m.submissions[key]; exists {
			return ErrAttemptSubmitted
		}
		if attempt.Submitted {
			return ErrAttemptSubmitted
		}
		if attempt.Version != expectedVersion {
			return ErrAttemptConflict
		}
		attempt.Answers = cloneAnswers(answers)
		attempt.Version++
		attempt.LastRequestID = requestID
		attempt.UpdatedAt = m.now().UnixMilli()
		attempt.UpdatedAtISO = iso(m.now())
		if attempt.AttemptNumber <= 0 {
			attempt.AttemptNumber = 1
		}
		result = cloneAttempt(attempt)
		return nil
	})
	return result, err
}

func cloneAttempt(attempt *model.Attempt) *model.Attempt {
	if attempt == nil {
		return nil
	}
	value := *attempt
	value.Answers = cloneAnswers(attempt.Answers)
	return &value
}

func (m *MemoryStore) GradeAttempt(_ context.Context, e string, ans map[string]int) (int, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ex := m.exams[e]
	if ex == nil {
		return 0, 0, nil
	}
	score, total := 0, 0
	for _, q := range ex.Questions {
		points := effectivePoints(q.Points)
		if a, ok := ans[strconv.Itoa(q.Index)]; ok && a == q.Answer {
			score += points
		}
		total += points
	}
	return score, total, nil
}
func (m *MemoryStore) FindSubmissionRequest(_ context.Context, examID, studentID string, answers map[string]int, requestID string) (model.SubmissionResult, bool, error) {
	if strings.TrimSpace(requestID) == "" {
		return model.SubmissionResult{}, false, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	receipt, exists := m.submissionRequests[requestID]
	if !exists {
		return model.SubmissionResult{}, false, nil
	}
	if receipt.payloadHash != submissionPayloadHash(examID, studentID, answers) {
		return model.SubmissionResult{}, false, ErrSubmissionRequestConflict
	}
	result := receipt.result
	result.Status = "duplicate"
	return result, true, nil
}

func (m *MemoryStore) SubmitAttempt(ctx context.Context, e, id, name string, ans map[string]int, req string) (model.SubmissionResult, error) {
	if req == "" {
		req = newID()
	}
	payloadHash := submissionPayloadHash(e, id, ans)
	var result model.SubmissionResult
	err := m.withFence(ctx, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.submissions == nil {
			m.submissions = map[string]*model.Submission{}
		}
		if m.submissionRequests == nil {
			m.submissionRequests = map[string]memorySubmissionReceipt{}
		}
		k := e + "/" + id
		if previous, exists := m.submissionRequests[req]; exists {
			if previous.payloadHash != payloadHash {
				return ErrSubmissionRequestConflict
			}
			result = previous.result
			result.Status = "duplicate"
			return nil
		}
		if old := m.submissions[k]; old != nil {
			if old.RequestID == req {
				oldHash := old.PayloadHash
				if oldHash == "" {
					oldHash = submissionPayloadHash(e, id, old.Answers)
				}
				if oldHash == payloadHash {
					result = model.SubmissionResult{Status: "duplicate", RequestID: req, Version: old.Version, CommittedAt: old.UpdatedAt, Score: old.Score, Total: old.Total, AttemptNumber: old.AttemptNumber}
					return nil
				}
				return ErrSubmissionRequestConflict
			}
			return ErrSubmissionConflict
		}
		ex := m.exams[e]
		score, total := 0, 0
		attemptNumber := 1
		if ex != nil {
			for _, q := range ex.Questions {
				points := effectivePoints(q.Points)
				total += points
				if a, ok := ans[strconv.Itoa(q.Index)]; ok && a == q.Answer {
					score += points
				}
			}
		}
		attempt := m.attempts[k]
		if attempt != nil {
			if attempt.Submitted {
				return ErrAttemptSubmitted
			}
			attempt = cloneAttempt(attempt)
			if attempt.AttemptNumber > 0 {
				attemptNumber = attempt.AttemptNumber
			}
		} else {
			now := m.now()
			attempt = &model.Attempt{PK: "ATTEMPT#" + e, SK: "STUDENT#" + id, AttemptNumber: 1, StartedAt: now.UnixMilli(), StartedAtISO: iso(now)}
		}
		t := m.now()
		attempt.Answers = cloneAnswers(ans)
		attempt.Version++
		attempt.Submitted = true
		attempt.SubmittedAt = t.UnixMilli()
		attempt.UpdatedAt = t.UnixMilli()
		attempt.UpdatedAtISO = iso(t)
		m.attempts[k] = attempt
		sub := &model.Submission{PK: "SUBMISSION#" + e, SK: "STUDENT#" + id, RequestID: req, PayloadHash: payloadHash, Version: 1, UpdatedAt: t.UnixMilli(), UpdatedAtISO: iso(t), WrittenByRegion: m.region, Answers: cloneAnswers(ans), StudentName: name, AttemptNumber: attemptNumber, Score: score, Total: total}
		m.submissions[k] = sub
		m.outbox = append(m.outbox, model.OutboxEntry{PK: "JOURNAL#" + req, SK: "OUTBOX", TargetPK: sub.PK, TargetSK: sub.SK, Version: 1, RequestID: req, CreatedAt: t.UnixMilli(), Answers: cloneAnswers(ans), StudentName: name, AttemptNumber: attemptNumber, Score: score, Total: total, TTL: t.Unix() + 7*24*3600})
		result = model.SubmissionResult{Status: "committed", RequestID: req, Version: 1, CommittedAt: t.UnixMilli(), Score: score, Total: total, AttemptNumber: attemptNumber}
		m.submissionRequests[req] = memorySubmissionReceipt{payloadHash: payloadHash, examID: e, studentID: id, result: result}
		return nil
	})
	return result, err
}

// ReconcileOutbox creates one answer-free audit summary per committed request
// and marks its outbox entry processed under the same mutex. Missing or
// mismatched canonical submissions remain pending for later investigation.
func (m *MemoryStore) ReconcileOutbox(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.auditSummaries == nil {
		m.auditSummaries = map[string]model.AuditSummary{}
	}
	count := 0
	for index := range m.outbox {
		entry := &m.outbox[index]
		if entry.Processed {
			continue
		}
		if entry.RequestID == "" || entry.TargetPK == "" || entry.TargetSK == "" {
			continue
		}
		var canonical *model.Submission
		for _, submission := range m.submissions {
			if submission.PK == entry.TargetPK && submission.SK == entry.TargetSK {
				canonical = submission
				break
			}
		}
		if canonical == nil || canonical.RequestID != entry.RequestID {
			continue
		}
		if summary, exists := m.auditSummaries[entry.RequestID]; exists {
			if summary.TargetPK == entry.TargetPK && summary.TargetSK == entry.TargetSK {
				entry.Processed = true
			}
			continue
		}
		m.auditSummaries[entry.RequestID] = model.AuditSummary{
			RequestID: entry.RequestID, TargetPK: entry.TargetPK, TargetSK: entry.TargetSK,
			ProcessedAt: m.now().UTC().Format(time.RFC3339Nano), Score: entry.Score, Total: entry.Total,
		}
		entry.Processed = true
		count++
	}
	return count, nil
}

// ListAuditSummaries returns answer-free summaries created by ReconcileOutbox.
func (m *MemoryStore) ListAuditSummaries(_ context.Context, limit int) ([]model.AuditSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := make([]model.AuditSummary, 0, len(m.auditSummaries))
	for _, summary := range m.auditSummaries {
		rows = append(rows, summary)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ProcessedAt < rows[j].ProcessedAt })
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
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
