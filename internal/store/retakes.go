package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/clusterstate"
	"github.com/ayush/slate/internal/model"
)

// Attempt-aware extension points preserve the original store method signatures
// for older callers while making a current attempt number mandatory after a
// student retakes an exam.
type attemptNumberSaver interface {
	SaveAttemptForAttempt(context.Context, string, string, map[string]int, int, string, int) (*model.Attempt, error)
}

type attemptNumberSubmitter interface {
	SubmitAttemptForAttempt(context.Context, string, string, string, map[string]int, string, int) (model.SubmissionResult, error)
}

type attemptNumberReceiptLookup interface {
	FindSubmissionRequestForAttempt(context.Context, string, string, map[string]int, string, int) (model.SubmissionResult, bool, error)
}

type submissionReceiptLookup interface {
	FindSubmissionReceiptByID(context.Context, string, string, string, int) (model.SubmissionResult, bool, error)
}

type retakeStore interface {
	RetakeAttempt(context.Context, string, string, int) (*model.Attempt, error)
}

type retakeMetaStore interface {
	RetakeAttemptWithMeta(context.Context, string, string, int, *model.ExamMeta) (*model.Attempt, error)
}

func attemptNumberOrOne(number int) int {
	if number > 0 {
		return number
	}
	return 1
}

func attemptSnapshotSK(studentID string, number int) string {
	return fmt.Sprintf("STUDENT#%s#ATTEMPT#%04d", strings.TrimSpace(studentID), number)
}

func submissionSnapshotSK(studentID string, number int) string {
	return attemptSnapshotSK(studentID, number)
}

func memoryAttemptSnapshotKey(examID, studentID string, number int) string {
	return examID + "/" + studentID + "#ATTEMPT#" + strconv.Itoa(number)
}

func memorySubmissionSnapshotKey(examID, studentID string, number int) string {
	return memoryAttemptSnapshotKey(examID, studentID, number)
}

func submissionPayloadHashForAttempt(examID, studentID string, answers map[string]int, attemptNumber int) string {
	if attemptNumber <= 0 {
		// This exact legacy hash shape is retained for clients that submitted
		// before attempt numbers were part of the request contract.
		return submissionPayloadHash(examID, studentID, answers)
	}
	payload, _ := json.Marshal(struct {
		ExamID        string         `json:"exam_id"`
		StudentID     string         `json:"student_id"`
		Answers       map[string]int `json:"answers"`
		AttemptNumber int            `json:"attempt_number"`
	}{ExamID: examID, StudentID: studentID, Answers: answers, AttemptNumber: attemptNumber})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// A retake is authorized against a particular exam window and assignment. Pin
// those settings in the transaction so a concurrent teacher edit cannot close
// or reassign an exam after validation but before the new attempt is written.
func retakeMetaSnapshotCondition(metaItem map[string]types.AttributeValue) (string, map[string]string, map[string]types.AttributeValue) {
	fields := []string{"max_attempts", "status", "opens_at", "closes_at", "duration_minutes", "allow_late", "late_grace_minutes", "owner_id", "classroom_id"}
	conditions := []string{"attribute_exists(pk)"}
	names := make(map[string]string, len(fields))
	values := make(map[string]types.AttributeValue, len(fields))
	for index, field := range fields {
		nameToken := "#retake_snapshot_" + strconv.Itoa(index)
		valueToken := ":retake_snapshot_" + strconv.Itoa(index)
		names[nameToken] = field
		if value, exists := metaItem[field]; exists {
			conditions = append(conditions, nameToken+" = "+valueToken)
			values[valueToken] = value
		} else {
			conditions = append(conditions, "attribute_not_exists("+nameToken+")")
		}
	}
	return strings.Join(conditions, " AND "), names, values
}

func sameRetakeMetaSnapshot(left, right *model.ExamMeta) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.MaxAttempts == right.MaxAttempts && left.Status == right.Status && left.OpensAt == right.OpensAt && left.ClosesAt == right.ClosesAt && left.DurationMinutes == right.DurationMinutes && left.AllowLate == right.AllowLate && left.LateGraceMinutes == right.LateGraceMinutes && left.OwnerID == right.OwnerID && left.ClassroomID == right.ClassroomID
}

func (s *Store) FindSubmissionRequest(ctx context.Context, examID, studentID string, answers map[string]int, requestID string) (model.SubmissionResult, bool, error) {
	return s.FindSubmissionRequestForAttempt(ctx, examID, studentID, answers, requestID, 0)
}

func (s *Store) FindSubmissionRequestForAttempt(ctx context.Context, examID, studentID string, answers map[string]int, requestID string, attemptNumber int) (model.SubmissionResult, bool, error) {
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
	if receipt.ExamID != examID || receipt.StudentID != studentID {
		return model.SubmissionResult{}, false, ErrSubmissionRequestConflict
	}
	if attemptNumber > 0 && attemptNumberOrOne(receipt.AttemptNumber) != attemptNumber {
		return model.SubmissionResult{}, false, ErrSubmissionRequestConflict
	}
	if receipt.PayloadHash != submissionPayloadHashForAttempt(examID, studentID, answers, attemptNumber) {
		// Attempt one may have an old immutable receipt written before the
		// attempt number was included in the hash.
		legacyAttemptOne := attemptNumber == 1 && attemptNumberOrOne(receipt.AttemptNumber) == 1 && receipt.PayloadHash == submissionPayloadHash(examID, studentID, answers)
		if !legacyAttemptOne {
			return model.SubmissionResult{}, false, ErrSubmissionRequestConflict
		}
	}
	return receipt.Result("duplicate"), true, nil
}

func (s *Store) FindSubmissionReceiptByID(ctx context.Context, examID, studentID, requestID string, attemptNumber int) (model.SubmissionResult, bool, error) {
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
	if receipt.ExamID != examID || receipt.StudentID != studentID || (attemptNumber > 0 && attemptNumberOrOne(receipt.AttemptNumber) != attemptNumber) {
		return model.SubmissionResult{}, false, ErrSubmissionRequestConflict
	}
	return receipt.Result("duplicate"), true, nil
}

func (s *Store) RetakeAttempt(ctx context.Context, examID, studentID string, expectedAttemptNumber int) (*model.Attempt, error) {
	return s.retakeAttempt(ctx, examID, studentID, expectedAttemptNumber, nil)
}

func (s *Store) RetakeAttemptWithMeta(ctx context.Context, examID, studentID string, expectedAttemptNumber int, expectedMeta *model.ExamMeta) (*model.Attempt, error) {
	if expectedMeta == nil {
		return nil, ErrAttemptConflict
	}
	return s.retakeAttempt(ctx, examID, studentID, expectedAttemptNumber, expectedMeta)
}

func (s *Store) retakeAttempt(ctx context.Context, examID, studentID string, expectedAttemptNumber int, expectedMeta *model.ExamMeta) (*model.Attempt, error) {
	if _, err := s.requireMutationWriter(ctx); err != nil {
		return nil, err
	}
	if expectedAttemptNumber <= 0 {
		return nil, ErrAttemptConflict
	}
	metaItem, err := s.getConsistent(ctx, key("EXAM#"+examID, "META"))
	if err != nil {
		return nil, err
	}
	if metaItem == nil {
		return nil, ErrNotFound
	}
	var meta model.ExamMeta
	if err := unmarshal(metaItem, &meta); err != nil {
		return nil, err
	}
	if expectedMeta != nil && !sameRetakeMetaSnapshot(&meta, expectedMeta) {
		return nil, ErrAttemptConflict
	}
	// Keep the stored item's exact attribute presence in the transaction CAS.
	// A metadata patch can intentionally write zero/false/empty values, which
	// a model re-marshal with omitempty would incorrectly turn into absence.
	retakeMetaItem := metaItem
	metaForLimit := &meta
	if expectedMeta != nil {
		metaForLimit = expectedMeta
	}
	active, err := s.GetStudentAttempt(ctx, examID, studentID)
	if err != nil {
		return nil, err
	}
	if active == nil {
		return nil, ErrAttemptConflict
	}
	activeNumber := attemptNumberOrOne(active.AttemptNumber)
	if activeNumber == expectedAttemptNumber+1 {
		return active, nil
	}
	if activeNumber != expectedAttemptNumber || !active.Submitted {
		return nil, ErrAttemptConflict
	}
	if metaForLimit.MaxAttempts > 0 && activeNumber >= metaForLimit.MaxAttempts {
		return nil, ErrAttemptsExhausted
	}

	latestSubmission, err := s.GetSubmission(ctx, examID, studentID)
	if err != nil {
		return nil, err
	}
	if latestSubmission == nil || attemptNumberOrOne(latestSubmission.AttemptNumber) != activeNumber {
		return nil, ErrSubmissionConflict
	}
	archivedAttemptKey := key("ATTEMPT#"+examID, attemptSnapshotSK(studentID, activeNumber))
	archivedAttemptItem, err := s.getConsistent(ctx, archivedAttemptKey)
	if err != nil {
		return nil, err
	}
	if archivedAttemptItem != nil {
		var archived model.Attempt
		if err := unmarshal(archivedAttemptItem, &archived); err != nil {
			return nil, err
		}
		if archived.AttemptNumber != activeNumber || archived.StartedAt != active.StartedAt || !archived.Submitted {
			return nil, ErrAttemptConflict
		}
	}

	historySubmissionKey := key("SUBMISSION#"+examID, submissionSnapshotSK(studentID, activeNumber))
	historySubmissionItem, err := s.getConsistent(ctx, historySubmissionKey)
	if err != nil {
		return nil, err
	}
	if historySubmissionItem != nil {
		var archived model.Submission
		if err := unmarshal(historySubmissionItem, &archived); err != nil {
			return nil, err
		}
		if archived.RequestID != latestSubmission.RequestID || attemptNumberOrOne(archived.AttemptNumber) != activeNumber {
			return nil, ErrSubmissionConflict
		}
	}

	archivedAttempt := *active
	archivedAttempt.PK = "ATTEMPT#" + examID
	archivedAttempt.SK = attemptSnapshotSK(studentID, activeNumber)
	archivedAttempt.AttemptNumber = activeNumber
	archivedAttemptPutItem, err := marshal(archivedAttempt)
	if err != nil {
		return nil, err
	}
	archivedSubmission := *latestSubmission
	archivedSubmission.PK = "SUBMISSION#" + examID
	archivedSubmission.SK = submissionSnapshotSK(studentID, activeNumber)
	archivedSubmission.Answers = cloneAnswers(latestSubmission.Answers)
	archivedSubmission.AttemptNumber = activeNumber
	archivedSubmissionPutItem, err := marshal(archivedSubmission)
	if err != nil {
		return nil, err
	}

	now := s.now()
	next := model.Attempt{PK: "ATTEMPT#" + examID, SK: "STUDENT#" + studentID, AttemptNumber: activeNumber + 1, StartedAt: now.UnixMilli(), StartedAtISO: iso(now), Answers: map[string]int{}}
	nextItem, err := marshal(next)
	if err != nil {
		return nil, err
	}
	attemptCondition := "attribute_exists(pk) AND attempt_number = :expected AND submitted = :true"
	attemptConditionValues := map[string]types.AttributeValue{":expected": &types.AttributeValueMemberN{Value: strconv.Itoa(expectedAttemptNumber)}, ":true": &types.AttributeValueMemberBOOL{Value: true}}
	if expectedAttemptNumber == 1 {
		attemptCondition = "attribute_exists(pk) AND (attribute_not_exists(attempt_number) OR attempt_number = :zero OR attempt_number = :expected) AND submitted = :true"
		attemptConditionValues[":zero"] = &types.AttributeValueMemberN{Value: "0"}
	}

	metaCondition, metaNames, metaValues := retakeMetaSnapshotCondition(retakeMetaItem)
	items := []types.TransactWriteItem{{
		ConditionCheck: &types.ConditionCheck{TableName: aws.String(s.TableName), Key: key("EXAM#"+examID, "META"), ConditionExpression: aws.String(metaCondition), ExpressionAttributeNames: metaNames, ExpressionAttributeValues: metaValues},
	}, {
		Put: &types.Put{TableName: aws.String(s.TableName), Item: nextItem, ConditionExpression: aws.String(attemptCondition), ExpressionAttributeValues: attemptConditionValues},
	}}
	if archivedAttemptItem == nil {
		items = append(items, types.TransactWriteItem{Put: &types.Put{TableName: aws.String(s.TableName), Item: archivedAttemptPutItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}})
	}
	if historySubmissionItem == nil {
		items = append(items, types.TransactWriteItem{Put: &types.Put{TableName: aws.String(s.TableName), Item: archivedSubmissionPutItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}})
	}

	journalKey := key("JOURNAL#"+latestSubmission.RequestID, "OUTBOX")
	journalItem, err := s.getConsistent(ctx, journalKey)
	if err != nil {
		return nil, err
	}
	if journalItem != nil {
		currentTargetSK, _ := journalItem["target_sk"].(*types.AttributeValueMemberS)
		if currentTargetSK != nil && currentTargetSK.Value == latestSubmission.SK {
			items = append(items, types.TransactWriteItem{Update: &types.Update{
				TableName: aws.String(s.TableName), Key: journalKey,
				UpdateExpression:          aws.String("SET target_sk = :target"),
				ConditionExpression:       aws.String("attribute_exists(pk) AND request_id = :request"),
				ExpressionAttributeValues: map[string]types.AttributeValue{":target": &types.AttributeValueMemberS{Value: archivedSubmission.SK}, ":request": &types.AttributeValueMemberS{Value: latestSubmission.RequestID}},
			}})
		}
	}

	if err := s.transactWrite(ctx, items); err != nil {
		if isTransactionConditional(err) {
			current, readErr := s.GetStudentAttempt(ctx, examID, studentID)
			if readErr != nil {
				return nil, readErr
			}
			if current != nil && attemptNumberOrOne(current.AttemptNumber) == expectedAttemptNumber+1 {
				return current, nil
			}
			currentMeta, metaErr := s.GetExamMeta(ctx, examID)
			if metaErr != nil {
				return nil, metaErr
			}
			if currentMeta != nil && currentMeta.MaxAttempts > 0 && expectedAttemptNumber >= currentMeta.MaxAttempts {
				return nil, ErrAttemptsExhausted
			}
			return nil, ErrAttemptConflict
		}
		return nil, err
	}
	return &next, nil
}

func (s *Store) SaveAttemptForAttempt(ctx context.Context, examID, studentID string, answers map[string]int, expectedVersion int, requestID string, requestedAttempt int) (*model.Attempt, error) {
	if _, err := s.requireMutationWriter(ctx); err != nil {
		return nil, err
	}
	if requestID == "" {
		return nil, fmt.Errorf("request_id is required")
	}
	active, err := s.GetStudentAttempt(ctx, examID, studentID)
	if err != nil {
		return nil, err
	}
	if active == nil {
		previous, err := s.GetSubmission(ctx, examID, studentID)
		if err != nil {
			return nil, err
		}
		if previous != nil {
			return nil, ErrAttemptSubmitted
		}
		if requestedAttempt > 1 {
			return nil, ErrAttemptConflict
		}
		active, err = s.RecordAttemptStart(ctx, examID, studentID)
		if err != nil {
			return nil, err
		}
	}
	currentNumber := attemptNumberOrOne(active.AttemptNumber)
	if requestedAttempt > 0 && requestedAttempt != currentNumber || requestedAttempt == 0 && currentNumber > 1 {
		return nil, ErrAttemptConflict
	}
	if active.LastRequestID == requestID {
		return active, nil
	}
	latestSubmission, err := s.GetSubmission(ctx, examID, studentID)
	if err != nil {
		return nil, err
	}
	if latestSubmission != nil && attemptNumberOrOne(latestSubmission.AttemptNumber) >= currentNumber {
		return nil, ErrAttemptSubmitted
	}
	if active.Submitted {
		return nil, ErrAttemptSubmitted
	}
	if active.Version != expectedVersion {
		return nil, ErrAttemptConflict
	}
	next := *active
	next.Answers = cloneAnswers(answers)
	next.Version++
	next.LastRequestID = requestID
	next.UpdatedAt = s.nowMS()
	next.UpdatedAtISO = iso(s.now())
	next.AttemptNumber = currentNumber
	item, err := marshal(next)
	if err != nil {
		return nil, err
	}
	condition := "version = :version AND attempt_number = :attempt AND (attribute_not_exists(submitted) OR submitted = :false)"
	values := map[string]types.AttributeValue{":version": &types.AttributeValueMemberN{Value: strconv.Itoa(active.Version)}, ":attempt": &types.AttributeValueMemberN{Value: strconv.Itoa(currentNumber)}, ":false": &types.AttributeValueMemberBOOL{Value: false}}
	if active.AttemptNumber <= 0 {
		values[":zero"] = &types.AttributeValueMemberN{Value: "0"}
		condition = "version = :version AND (attribute_not_exists(attempt_number) OR attempt_number = :zero OR attempt_number = :attempt) AND (attribute_not_exists(submitted) OR submitted = :false)"
	}
	write := []types.TransactWriteItem{{Put: &types.Put{TableName: aws.String(s.TableName), Item: item,
		ConditionExpression:       aws.String(condition),
		ExpressionAttributeValues: values,
	}}}
	if err := s.transactWrite(ctx, write); err != nil {
		if isTransactionConditional(err) {
			current, readErr := s.GetStudentAttempt(ctx, examID, studentID)
			if readErr != nil {
				return nil, readErr
			}
			if current != nil && current.LastRequestID == requestID && attemptNumberOrOne(current.AttemptNumber) == currentNumber {
				return current, nil
			}
			return nil, ErrAttemptConflict
		}
		return nil, err
	}
	return &next, nil
}

// SaveAttempt preserves the original API for legacy callers. The attempt-aware
// implementation treats an omitted attempt number as valid only for attempt 1.
func (s *Store) SaveAttempt(ctx context.Context, examID, studentID string, answers map[string]int, expectedVersion int, requestID string) (*model.Attempt, error) {
	return s.SaveAttemptForAttempt(ctx, examID, studentID, answers, expectedVersion, requestID, 0)
}

func (m *MemoryStore) FindSubmissionRequest(ctx context.Context, examID, studentID string, answers map[string]int, requestID string) (model.SubmissionResult, bool, error) {
	return m.FindSubmissionRequestForAttempt(ctx, examID, studentID, answers, requestID, 0)
}

func (m *MemoryStore) FindSubmissionRequestForAttempt(_ context.Context, examID, studentID string, answers map[string]int, requestID string, attemptNumber int) (model.SubmissionResult, bool, error) {
	if strings.TrimSpace(requestID) == "" {
		return model.SubmissionResult{}, false, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	receipt, exists := m.submissionRequests[requestID]
	if !exists {
		return model.SubmissionResult{}, false, nil
	}
	if receipt.examID != examID || receipt.studentID != studentID || (attemptNumber > 0 && attemptNumberOrOne(receipt.result.AttemptNumber) != attemptNumber) {
		return model.SubmissionResult{}, false, ErrSubmissionRequestConflict
	}
	if receipt.payloadHash != submissionPayloadHashForAttempt(examID, studentID, answers, attemptNumber) {
		legacyAttemptOne := attemptNumber == 1 && attemptNumberOrOne(receipt.result.AttemptNumber) == 1 && receipt.payloadHash == submissionPayloadHash(examID, studentID, answers)
		if !legacyAttemptOne {
			return model.SubmissionResult{}, false, ErrSubmissionRequestConflict
		}
	}
	result := receipt.result
	result.Status = "duplicate"
	return result, true, nil
}

func (m *MemoryStore) FindSubmissionReceiptByID(_ context.Context, examID, studentID, requestID string, attemptNumber int) (model.SubmissionResult, bool, error) {
	if strings.TrimSpace(requestID) == "" {
		return model.SubmissionResult{}, false, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	receipt, exists := m.submissionRequests[requestID]
	if !exists {
		return model.SubmissionResult{}, false, nil
	}
	if receipt.examID != examID || receipt.studentID != studentID || (attemptNumber > 0 && attemptNumberOrOne(receipt.result.AttemptNumber) != attemptNumber) {
		return model.SubmissionResult{}, false, ErrSubmissionRequestConflict
	}
	result := receipt.result
	result.Status = "duplicate"
	return result, true, nil
}

func (m *MemoryStore) RetakeAttempt(ctx context.Context, examID, studentID string, expectedAttemptNumber int) (*model.Attempt, error) {
	return m.retakeAttempt(ctx, examID, studentID, expectedAttemptNumber, nil)
}

func (m *MemoryStore) RetakeAttemptWithMeta(ctx context.Context, examID, studentID string, expectedAttemptNumber int, expectedMeta *model.ExamMeta) (*model.Attempt, error) {
	if expectedMeta == nil {
		return nil, ErrAttemptConflict
	}
	return m.retakeAttempt(ctx, examID, studentID, expectedAttemptNumber, expectedMeta)
}

func (m *MemoryStore) retakeAttempt(ctx context.Context, examID, studentID string, expectedAttemptNumber int, expectedMeta *model.ExamMeta) (*model.Attempt, error) {
	if expectedAttemptNumber <= 0 {
		return nil, ErrAttemptConflict
	}
	var result *model.Attempt
	err := m.withFence(ctx, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		activeKey := examID + "/" + studentID
		active := m.attempts[activeKey]
		if active == nil {
			return ErrAttemptConflict
		}
		currentNumber := attemptNumberOrOne(active.AttemptNumber)
		if currentNumber == expectedAttemptNumber+1 {
			result = cloneAttempt(active)
			return nil
		}
		if currentNumber != expectedAttemptNumber || !active.Submitted {
			return ErrAttemptConflict
		}
		meta := m.exams[examID]
		if meta == nil {
			return ErrNotFound
		}
		if expectedMeta != nil && !sameRetakeMetaSnapshot(&meta.ExamMeta, expectedMeta) {
			return ErrAttemptConflict
		}
		metaForLimit := &meta.ExamMeta
		if expectedMeta != nil {
			metaForLimit = expectedMeta
		}
		if metaForLimit.MaxAttempts > 0 && currentNumber >= metaForLimit.MaxAttempts {
			return ErrAttemptsExhausted
		}
		submission := m.submissions[activeKey]
		if submission == nil || attemptNumberOrOne(submission.AttemptNumber) != currentNumber {
			return ErrSubmissionConflict
		}
		attemptHistoryKey := memoryAttemptSnapshotKey(examID, studentID, currentNumber)
		if existing := m.attempts[attemptHistoryKey]; existing != nil && (existing.AttemptNumber != currentNumber || existing.StartedAt != active.StartedAt || !existing.Submitted) {
			return ErrAttemptConflict
		}
		submissionHistoryKey := memorySubmissionSnapshotKey(examID, studentID, currentNumber)
		if existing := m.submissions[submissionHistoryKey]; existing != nil && (existing.RequestID != submission.RequestID || attemptNumberOrOne(existing.AttemptNumber) != currentNumber) {
			return ErrSubmissionConflict
		}
		archivedAttempt := cloneAttempt(active)
		archivedAttempt.SK = attemptSnapshotSK(studentID, currentNumber)
		m.attempts[attemptHistoryKey] = archivedAttempt
		if m.submissions[submissionHistoryKey] == nil {
			archivedSubmission := *submission
			archivedSubmission.SK = submissionSnapshotSK(studentID, currentNumber)
			archivedSubmission.Answers = cloneAnswers(submission.Answers)
			m.submissions[submissionHistoryKey] = &archivedSubmission
		}
		for index := range m.outbox {
			if m.outbox[index].RequestID == submission.RequestID && m.outbox[index].TargetPK == submission.PK && m.outbox[index].TargetSK == submission.SK {
				m.outbox[index].TargetSK = submissionSnapshotSK(studentID, currentNumber)
			}
		}
		now := m.now()
		next := &model.Attempt{PK: "ATTEMPT#" + examID, SK: "STUDENT#" + studentID, AttemptNumber: currentNumber + 1, StartedAt: now.UnixMilli(), StartedAtISO: iso(now), Answers: map[string]int{}}
		m.attempts[activeKey] = next
		result = cloneAttempt(next)
		return nil
	})
	return result, err
}

func (m *MemoryStore) SaveAttempt(ctx context.Context, examID, studentID string, answers map[string]int, expectedVersion int, requestID string) (*model.Attempt, error) {
	return m.SaveAttemptForAttempt(ctx, examID, studentID, answers, expectedVersion, requestID, 0)
}

func (m *MemoryStore) SaveAttemptForAttempt(ctx context.Context, examID, studentID string, answers map[string]int, expectedVersion int, requestID string, requestedAttempt int) (*model.Attempt, error) {
	if requestID == "" {
		return nil, fmt.Errorf("request_id is required")
	}
	var result *model.Attempt
	err := m.withFence(ctx, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.attempts == nil {
			m.attempts = make(map[string]*model.Attempt)
		}
		key := examID + "/" + studentID
		attempt := m.attempts[key]
		if attempt == nil {
			if m.submissions[key] != nil {
				return ErrAttemptSubmitted
			}
			if requestedAttempt > 1 {
				return ErrAttemptConflict
			}
			now := m.now()
			attempt = &model.Attempt{PK: "ATTEMPT#" + examID, SK: "STUDENT#" + studentID, AttemptNumber: 1, StartedAt: now.UnixMilli(), StartedAtISO: iso(now)}
			m.attempts[key] = attempt
		}
		currentNumber := attemptNumberOrOne(attempt.AttemptNumber)
		if requestedAttempt > 0 && requestedAttempt != currentNumber || requestedAttempt == 0 && currentNumber > 1 {
			return ErrAttemptConflict
		}
		if attempt.LastRequestID == requestID {
			result = cloneAttempt(attempt)
			return nil
		}
		if latest := m.submissions[key]; latest != nil && attemptNumberOrOne(latest.AttemptNumber) >= currentNumber {
			return ErrAttemptSubmitted
		}
		if attempt.Submitted {
			return ErrAttemptSubmitted
		}
		if attempt.Version != expectedVersion {
			return ErrAttemptConflict
		}
		next := cloneAttempt(attempt)
		next.Answers = cloneAnswers(answers)
		next.Version++
		next.LastRequestID = requestID
		next.UpdatedAt = m.now().UnixMilli()
		next.UpdatedAtISO = iso(m.now())
		next.AttemptNumber = currentNumber
		m.attempts[key] = next
		result = cloneAttempt(next)
		return nil
	})
	return result, err
}

func (s *Store) SubmitAttempt(ctx context.Context, examID, studentID, studentName string, answers map[string]int, requestID string) (model.SubmissionResult, error) {
	return s.SubmitAttemptForAttempt(ctx, examID, studentID, studentName, answers, requestID, 0)
}

func (s *Store) SubmitAttemptForAttempt(ctx context.Context, examID, studentID, studentName string, answers map[string]int, requestID string, requestedAttempt int) (model.SubmissionResult, error) {
	if _, err := s.requireMutationWriter(ctx); err != nil {
		return model.SubmissionResult{}, err
	}
	if requestID == "" {
		requestID = newID()
	}
	payloadHash := submissionPayloadHashForAttempt(examID, studentID, answers, requestedAttempt)
	if duplicate, found, err := s.FindSubmissionRequestForAttempt(ctx, examID, studentID, answers, requestID, requestedAttempt); err != nil || found {
		return duplicate, err
	}
	active, err := s.GetStudentAttempt(ctx, examID, studentID)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	if active == nil {
		previous, err := s.GetSubmission(ctx, examID, studentID)
		if err != nil {
			return model.SubmissionResult{}, err
		}
		if previous != nil {
			return model.SubmissionResult{}, ErrSubmissionConflict
		}
		active, err = s.RecordAttemptStart(ctx, examID, studentID)
		if err != nil {
			return model.SubmissionResult{}, err
		}
	}
	activeNumber := attemptNumberOrOne(active.AttemptNumber)
	if requestedAttempt > 0 && requestedAttempt != activeNumber || requestedAttempt == 0 && activeNumber > 1 {
		return model.SubmissionResult{}, ErrAttemptConflict
	}
	submissionKey := key("SUBMISSION#"+examID, "STUDENT#"+studentID)
	existingItem, err := s.getConsistent(ctx, submissionKey)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	if existingItem != nil {
		var old model.Submission
		if err := unmarshal(existingItem, &old); err != nil {
			return model.SubmissionResult{}, err
		}
		if attemptNumberOrOne(old.AttemptNumber) >= activeNumber {
			return model.SubmissionResult{}, ErrSubmissionConflict
		}
	}
	if active.Submitted {
		return model.SubmissionResult{}, ErrAttemptSubmitted
	}
	score, total, err := s.GradeAttempt(ctx, examID, answers)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	if active.Version < 0 {
		return model.SubmissionResult{}, ErrAttemptConflict
	}
	now := s.now()
	committedAt := now.UnixMilli()
	updatedAttempt := *active
	updatedAttempt.Answers = cloneAnswers(answers)
	updatedAttempt.AttemptNumber = activeNumber
	updatedAttempt.Version++
	updatedAttempt.Submitted = true
	updatedAttempt.SubmittedAt = committedAt
	updatedAttempt.UpdatedAt = committedAt
	updatedAttempt.UpdatedAtISO = iso(now)
	attemptItem, err := marshal(updatedAttempt)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	historySK := submissionSnapshotSK(studentID, activeNumber)
	historySubmission := model.Submission{PK: "SUBMISSION#" + examID, SK: historySK, Answers: cloneAnswers(answers), Version: 1, RequestID: requestID, PayloadHash: payloadHash, UpdatedAt: committedAt, UpdatedAtISO: iso(now), WrittenByRegion: s.Region, StudentName: studentName, AttemptNumber: activeNumber, Score: score, Total: total}
	historyItem, err := marshal(historySubmission)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	latestSubmission := historySubmission
	latestSubmission.SK = "STUDENT#" + studentID
	latestItem, err := marshal(latestSubmission)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	requestKey := submissionRequestKey(requestID)
	requestPK := requestKey["pk"].(*types.AttributeValueMemberS).Value
	receipt := model.SubmissionRequest{PK: requestPK, SK: "RECEIPT", RequestID: requestID, PayloadHash: payloadHash, ExamID: examID, StudentID: studentID, Version: 1, CommittedAt: committedAt, Score: score, Total: total, AttemptNumber: activeNumber}
	receiptItem, err := marshal(receipt)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	journal := model.OutboxEntry{PK: "JOURNAL#" + requestID, SK: "OUTBOX", TargetPK: historySubmission.PK, TargetSK: historySubmission.SK, Version: 1, RequestID: requestID, CreatedAt: committedAt, Answers: cloneAnswers(answers), StudentName: studentName, AttemptNumber: activeNumber, Score: score, Total: total, TTL: now.Unix() + 7*24*3600, Processed: false}
	journalItem, err := marshal(journal)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	condition := "version = :version AND attempt_number = :attempt AND (attribute_not_exists(submitted) OR submitted = :false)"
	values := map[string]types.AttributeValue{":version": &types.AttributeValueMemberN{Value: strconv.Itoa(active.Version)}, ":attempt": &types.AttributeValueMemberN{Value: strconv.Itoa(activeNumber)}, ":false": &types.AttributeValueMemberBOOL{Value: false}}
	transaction := []types.TransactWriteItem{
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: historyItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: latestItem}},
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: receiptItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: journalItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: attemptItem, ConditionExpression: aws.String(condition), ExpressionAttributeValues: values}},
	}
	if active.AttemptNumber <= 0 {
		values[":attempt"] = &types.AttributeValueMemberN{Value: "1"}
		values[":zero"] = &types.AttributeValueMemberN{Value: "0"}
		condition = "version = :version AND (attribute_not_exists(attempt_number) OR attempt_number = :zero OR attempt_number = :attempt) AND (attribute_not_exists(submitted) OR submitted = :false)"
		transaction[4].Put.ConditionExpression = aws.String(condition)
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
			if duplicate, found, readErr := s.FindSubmissionRequestForAttempt(ctx, examID, studentID, answers, requestID, requestedAttempt); readErr != nil || found {
				return duplicate, readErr
			}
			current, readErr := s.GetStudentAttempt(ctx, examID, studentID)
			if readErr != nil {
				return model.SubmissionResult{}, readErr
			}
			if current != nil {
				if attemptNumberOrOne(current.AttemptNumber) != activeNumber {
					return model.SubmissionResult{}, ErrAttemptConflict
				}
				if current.Submitted {
					return model.SubmissionResult{}, ErrAttemptSubmitted
				}
			}
			return model.SubmissionResult{}, ErrAttemptConflict
		}
		return model.SubmissionResult{}, err
	}
	return model.SubmissionResult{Status: "committed", RequestID: requestID, Version: 1, CommittedAt: committedAt, Score: score, Total: total, AttemptNumber: activeNumber}, nil
}

func (m *MemoryStore) SubmitAttempt(ctx context.Context, examID, studentID, studentName string, answers map[string]int, requestID string) (model.SubmissionResult, error) {
	return m.SubmitAttemptForAttempt(ctx, examID, studentID, studentName, answers, requestID, 0)
}

func (m *MemoryStore) SubmitAttemptForAttempt(ctx context.Context, examID, studentID, studentName string, answers map[string]int, requestID string, requestedAttempt int) (model.SubmissionResult, error) {
	if requestID == "" {
		requestID = newID()
	}
	payloadHash := submissionPayloadHashForAttempt(examID, studentID, answers, requestedAttempt)
	var result model.SubmissionResult
	err := m.withFence(ctx, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.submissions == nil {
			m.submissions = map[string]*model.Submission{}
		}
		if m.attempts == nil {
			m.attempts = map[string]*model.Attempt{}
		}
		if m.submissionRequests == nil {
			m.submissionRequests = map[string]memorySubmissionReceipt{}
		}
		if previous, exists := m.submissionRequests[requestID]; exists {
			if previous.examID != examID || previous.studentID != studentID || (requestedAttempt > 0 && attemptNumberOrOne(previous.result.AttemptNumber) != requestedAttempt) {
				return ErrSubmissionRequestConflict
			}
			if previous.payloadHash != payloadHash {
				legacyAttemptOne := requestedAttempt == 1 && attemptNumberOrOne(previous.result.AttemptNumber) == 1 && previous.payloadHash == submissionPayloadHash(examID, studentID, answers)
				if !legacyAttemptOne {
					return ErrSubmissionRequestConflict
				}
			}
			result = previous.result
			result.Status = "duplicate"
			return nil
		}
		key := examID + "/" + studentID
		active := m.attempts[key]
		if active == nil {
			if m.submissions[key] != nil {
				return ErrSubmissionConflict
			}
			now := m.now()
			active = &model.Attempt{PK: "ATTEMPT#" + examID, SK: "STUDENT#" + studentID, AttemptNumber: 1, StartedAt: now.UnixMilli(), StartedAtISO: iso(now)}
			m.attempts[key] = active
		}
		activeNumber := attemptNumberOrOne(active.AttemptNumber)
		if requestedAttempt > 0 && requestedAttempt != activeNumber || requestedAttempt == 0 && activeNumber > 1 {
			return ErrAttemptConflict
		}
		latestKey := examID + "/" + studentID
		if old := m.submissions[latestKey]; old != nil && attemptNumberOrOne(old.AttemptNumber) >= activeNumber {
			return ErrSubmissionConflict
		}
		if active.Submitted {
			return ErrAttemptSubmitted
		}
		score, total := 0, 0
		if meta := m.exams[examID]; meta != nil {
			for _, question := range meta.Questions {
				points := effectivePoints(question.Points)
				total += points
				if selected, ok := answers[strconv.Itoa(question.Index)]; ok && selected == question.Answer {
					score += points
				}
			}
		}
		t := m.now()
		committedAt := t.UnixMilli()
		updatedAttempt := cloneAttempt(active)
		updatedAttempt.Answers = cloneAnswers(answers)
		updatedAttempt.AttemptNumber = activeNumber
		updatedAttempt.Version++
		updatedAttempt.Submitted = true
		updatedAttempt.SubmittedAt = committedAt
		updatedAttempt.UpdatedAt = committedAt
		updatedAttempt.UpdatedAtISO = iso(t)
		m.attempts[key] = updatedAttempt
		historySK := submissionSnapshotSK(studentID, activeNumber)
		historyKey := memorySubmissionSnapshotKey(examID, studentID, activeNumber)
		history := &model.Submission{PK: "SUBMISSION#" + examID, SK: historySK, RequestID: requestID, PayloadHash: payloadHash, Version: 1, UpdatedAt: committedAt, UpdatedAtISO: iso(t), WrittenByRegion: m.region, Answers: cloneAnswers(answers), StudentName: studentName, AttemptNumber: activeNumber, Score: score, Total: total}
		latest := *history
		latest.SK = "STUDENT#" + studentID
		m.submissions[historyKey] = history
		m.submissions[latestKey] = &latest
		m.outbox = append(m.outbox, model.OutboxEntry{PK: "JOURNAL#" + requestID, SK: "OUTBOX", TargetPK: history.PK, TargetSK: history.SK, Version: 1, RequestID: requestID, CreatedAt: committedAt, Answers: cloneAnswers(answers), StudentName: studentName, AttemptNumber: activeNumber, Score: score, Total: total, TTL: t.Unix() + 7*24*3600})
		result = model.SubmissionResult{Status: "committed", RequestID: requestID, Version: 1, CommittedAt: committedAt, Score: score, Total: total, AttemptNumber: activeNumber}
		m.submissionRequests[requestID] = memorySubmissionReceipt{payloadHash: payloadHash, examID: examID, studentID: studentID, result: result}
		return nil
	})
	return result, err
}

var _ attemptNumberSaver = (*Store)(nil)
var _ attemptNumberSaver = (*MemoryStore)(nil)
var _ attemptNumberSubmitter = (*Store)(nil)
var _ attemptNumberSubmitter = (*MemoryStore)(nil)
var _ retakeStore = (*Store)(nil)
var _ retakeStore = (*MemoryStore)(nil)
var _ retakeMetaStore = (*Store)(nil)
var _ retakeMetaStore = (*MemoryStore)(nil)
var _ attemptNumberReceiptLookup = (*Store)(nil)
var _ attemptNumberReceiptLookup = (*MemoryStore)(nil)
var _ submissionReceiptLookup = (*Store)(nil)
var _ submissionReceiptLookup = (*MemoryStore)(nil)
