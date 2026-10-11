package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/clusterstate"
	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/model"
)

func TestMemoryRetakesAreAttemptScopedAndPreserveReceipts(t *testing.T) {
	ctx := context.Background()
	m := NewMemory("test")
	if err := m.EnsureExam(ctx, "exam", "Quiz", []model.Question{{Index: 0, Question: "q", Options: []string{"no", "yes"}, Answer: 1, Points: 2}}, map[string]any{"max_attempts": 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RecordAttemptStart(ctx, "exam", "student"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SaveAttemptForAttempt(ctx, "exam", "student", map[string]int{"0": 1}, 0, "save-1", 1); err != nil {
		t.Fatal(err)
	}
	first, err := m.SubmitAttemptForAttempt(ctx, "exam", "student", "Ada", map[string]int{"0": 1}, "submit-1", 1)
	if err != nil || first.Status != "committed" || first.AttemptNumber != 1 {
		t.Fatalf("attempt 1 submit = %#v, %v", first, err)
	}

	second, err := m.RetakeAttempt(ctx, "exam", "student", 1)
	if err != nil || second == nil || second.AttemptNumber != 2 || second.Submitted {
		t.Fatalf("retake = %#v, %v", second, err)
	}
	retry, err := m.RetakeAttempt(ctx, "exam", "student", 1)
	if err != nil || retry == nil || retry.StartedAt != second.StartedAt || retry.AttemptNumber != 2 {
		t.Fatalf("idempotent retake = %#v, %v", retry, err)
	}
	if _, err := m.SaveAttemptForAttempt(ctx, "exam", "student", map[string]int{"0": 0}, 0, "stale-save", 1); !errors.Is(err, ErrAttemptConflict) {
		t.Fatalf("stale attempt save = %v", err)
	}
	if _, err := m.SubmitAttemptForAttempt(ctx, "exam", "student", "Ada", map[string]int{"0": 0}, "stale-submit", 1); !errors.Is(err, ErrAttemptConflict) {
		t.Fatalf("stale attempt submit = %v", err)
	}
	duplicate, found, err := m.FindSubmissionRequestForAttempt(ctx, "exam", "student", map[string]int{"0": 1}, "submit-1", 1)
	if err != nil || !found || duplicate.Status != "duplicate" || duplicate.AttemptNumber != 1 {
		t.Fatalf("old receipt after retake = %#v found=%v err=%v", duplicate, found, err)
	}

	if _, err := m.SaveAttemptForAttempt(ctx, "exam", "student", map[string]int{"0": 0}, 0, "save-2", 2); err != nil {
		t.Fatal(err)
	}
	secondResult, err := m.SubmitAttemptForAttempt(ctx, "exam", "student", "Ada", map[string]int{"0": 0}, "submit-2", 2)
	if err != nil || secondResult.Status != "committed" || secondResult.AttemptNumber != 2 {
		t.Fatalf("attempt 2 submit = %#v, %v", secondResult, err)
	}
	latest, err := m.GetSubmission(ctx, "exam", "student")
	if err != nil || latest == nil || latest.AttemptNumber != 2 || latest.RequestID != "submit-2" {
		t.Fatalf("latest submission = %#v, %v", latest, err)
	}
	rows, err := m.ListSubmissions(ctx, "exam", 10)
	if err != nil || len(rows) != 1 || rows[0].AttemptNumber != 2 {
		t.Fatalf("analytics rows include only latest = %#v, %v", rows, err)
	}
	if m.submissions[memorySubmissionSnapshotKey("exam", "student", 1)] == nil || m.attempts[memoryAttemptSnapshotKey("exam", "student", 1)] == nil {
		t.Fatal("attempt 1 was not preserved as immutable history")
	}
	if len(m.outbox) != 2 || m.outbox[0].TargetSK != submissionSnapshotSK("student", 1) || m.outbox[1].TargetSK != submissionSnapshotSK("student", 2) {
		t.Fatalf("outbox targets were not pinned to attempt history: %#v", m.outbox)
	}
	if reconciled, err := m.ReconcileOutbox(ctx); err != nil || reconciled != 2 {
		t.Fatalf("reconcile history outbox = %d, %v", reconciled, err)
	}
	if _, err := m.RetakeAttempt(ctx, "exam", "student", 2); !errors.Is(err, ErrAttemptsExhausted) {
		t.Fatalf("third attempt with max=2 = %v", err)
	}
}

func TestMemoryZeroMaxAttemptsIsUnlimitedAndRetakeIsFenced(t *testing.T) {
	ctx := context.Background()
	m := NewMemory("test")
	if err := m.EnsureExam(ctx, "unlimited", "Quiz", []model.Question{{Index: 0, Question: "q", Options: []string{"a", "b"}, Answer: 1}}, map[string]any{"max_attempts": 0}); err != nil {
		t.Fatal(err)
	}
	guard := &memoryFenceStub{active: clusterstate.Writer{Backend: "A", Epoch: 7}}
	m.SetFenceGuard(guard)
	oldWriter := clusterstate.WithWriter(ctx, guard.active)
	if _, err := m.RecordAttemptStart(oldWriter, "unlimited", "student"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SubmitAttemptForAttempt(oldWriter, "unlimited", "student", "Ada", map[string]int{"0": 1}, "one", 1); err != nil {
		t.Fatal(err)
	}
	guard.active = clusterstate.Writer{Backend: "B", Epoch: 8}
	if _, err := m.RetakeAttempt(oldWriter, "unlimited", "student", 1); !errors.Is(err, clusterstate.ErrFenced) {
		t.Fatalf("stale retake writer = %v", err)
	}
	freshWriter := clusterstate.WithWriter(ctx, guard.active)
	second, err := m.RetakeAttempt(freshWriter, "unlimited", "student", 1)
	if err != nil || second == nil || second.AttemptNumber != 2 {
		t.Fatalf("unlimited retake = %#v, %v", second, err)
	}
}

func TestRetakeRejectsMetadataChangedAfterServiceValidation(t *testing.T) {
	ctx := context.Background()
	m := NewMemory("test")
	if err := m.EnsureExam(ctx, "exam", "Quiz", []model.Question{{Index: 0, Question: "q", Options: []string{"a", "b"}, Answer: 1}}, map[string]any{"status": "open", "max_attempts": 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RecordAttemptStart(ctx, "exam", "student"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SubmitAttemptForAttempt(ctx, "exam", "student", "Ada", map[string]int{"0": 1}, "submit-1", 1); err != nil {
		t.Fatal(err)
	}
	validated, err := m.GetExamMeta(ctx, "exam")
	if err != nil || validated == nil {
		t.Fatalf("read validated metadata: %#v, %v", validated, err)
	}
	if _, err := m.UpdateExamWindow(ctx, "exam", nil, map[string]any{"status": "closed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RetakeAttemptWithMeta(ctx, "exam", "student", 1, validated); !errors.Is(err, ErrAttemptConflict) {
		t.Fatalf("retake with stale validated metadata = %v", err)
	}
	active, err := m.GetStudentAttempt(ctx, "exam", "student")
	if err != nil || active == nil || active.AttemptNumber != 1 || !active.Submitted {
		t.Fatalf("stale metadata started a new attempt: %#v, %v", active, err)
	}
}

func TestDynamoRetakeTransactionPinsWindowAndWriter(t *testing.T) {
	cfg := config.Defaults()
	cfg.FencingEnabled = true
	s := NewWithClient(newScriptedDynamo(), cfg)
	fake := s.Client.(*scriptedDynamo)
	ctx := clusterstate.WithWriter(context.Background(), clusterstate.Writer{Backend: "A", Epoch: 31})
	seedItem(fake, model.ExamMeta{PK: "EXAM#exam", SK: "META", OwnerID: "teacher", ClassroomID: "class", Status: "open", OpensAt: "2026-10-01T00:00:00Z", ClosesAt: "2026-11-01T00:00:00Z", DurationMinutes: 30, MaxAttempts: 3, AllowLate: true})
	seedItem(fake, model.Attempt{PK: "ATTEMPT#exam", SK: "STUDENT#student", AttemptNumber: 1, StartedAt: 100, Submitted: true})
	seedItem(fake, model.Submission{PK: "SUBMISSION#exam", SK: "STUDENT#student", RequestID: "request-1", AttemptNumber: 1, Answers: map[string]int{"0": 1}})
	seedItem(fake, model.OutboxEntry{PK: "JOURNAL#request-1", SK: "OUTBOX", RequestID: "request-1", TargetPK: "SUBMISSION#exam", TargetSK: "STUDENT#student"})

	retake, err := s.RetakeAttempt(ctx, "exam", "student", 1)
	if err != nil || retake == nil || retake.AttemptNumber != 2 {
		t.Fatalf("Dynamo retake = %#v, %v", retake, err)
	}
	if len(fake.transactions) != 1 {
		t.Fatalf("retake transaction count = %d", len(fake.transactions))
	}
	tx := fake.transactions[0].TransactItems
	if len(tx) < 5 || tx[0].ConditionCheck == nil {
		t.Fatalf("retake transaction omitted meta snapshot check: %#v", tx)
	}
	metaCheck := tx[0].ConditionCheck
	if avString(metaCheck.Key, "pk") != "EXAM#exam" || avString(metaCheck.Key, "sk") != "META" {
		t.Fatalf("snapshot check key = %#v", metaCheck.Key)
	}
	for _, field := range []string{"max_attempts", "status", "opens_at", "closes_at", "duration_minutes", "allow_late", "late_grace_minutes", "owner_id", "classroom_id"} {
		found := false
		for _, name := range metaCheck.ExpressionAttributeNames {
			if name == field {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("retake metadata condition does not pin %q: %s", field, aws.ToString(metaCheck.ConditionExpression))
		}
	}
	if !strings.Contains(aws.ToString(metaCheck.ConditionExpression), "attribute_not_exists") {
		t.Fatalf("snapshot check does not pin absent fields: %s names=%#v", aws.ToString(metaCheck.ConditionExpression), metaCheck.ExpressionAttributeNames)
	}
	last := tx[len(tx)-1].ConditionCheck
	if last == nil || avString(last.Key, "pk") != clusterstate.ActivePK || avString(last.Key, "sk") != clusterstate.ActiveSK || last.ExpressionAttributeValues[":epoch"].(*types.AttributeValueMemberN).Value != "31" {
		t.Fatalf("retake transaction missing writer fence: %#v", tx[len(tx)-1])
	}
	foundSnapshot := false
	foundJournalMove := false
	for _, item := range tx {
		if item.Put != nil && avString(item.Put.Item, "pk") == "ATTEMPT#exam" && avString(item.Put.Item, "sk") == attemptSnapshotSK("student", 1) {
			foundSnapshot = true
		}
		if item.Update != nil && avString(item.Update.Key, "pk") == "JOURNAL#request-1" && item.Update.ExpressionAttributeValues[":target"].(*types.AttributeValueMemberS).Value == submissionSnapshotSK("student", 1) {
			foundJournalMove = true
		}
	}
	if !foundSnapshot || !foundJournalMove {
		t.Fatalf("retake omitted immutable snapshots or canonical journal move: %#v", tx)
	}
}

func TestDynamoRetakeRejectsMetadataChangedAfterServiceValidation(t *testing.T) {
	s := fixedDynamo()
	fake := s.Client.(*scriptedDynamo)
	seedItem(fake, model.ExamMeta{PK: "EXAM#exam", SK: "META", Status: "closed", MaxAttempts: 2})
	seedItem(fake, model.Attempt{PK: "ATTEMPT#exam", SK: "STUDENT#student", AttemptNumber: 1, StartedAt: 100, Submitted: true})
	seedItem(fake, model.Submission{PK: "SUBMISSION#exam", SK: "STUDENT#student", RequestID: "submit-1", AttemptNumber: 1})
	validated := &model.ExamMeta{PK: "EXAM#exam", SK: "META", Status: "open", MaxAttempts: 2}
	if _, err := s.RetakeAttemptWithMeta(context.Background(), "exam", "student", 1, validated); !errors.Is(err, ErrAttemptConflict) {
		t.Fatalf("retake with stale validated metadata = %v", err)
	}
	if len(fake.transactions) != 0 {
		t.Fatalf("stale metadata reached write transaction: %d", len(fake.transactions))
	}
}

func TestDynamoRetakeSnapshotPreservesExplicitZeroAndEmptyValues(t *testing.T) {
	s := fixedDynamo()
	fake := s.Client.(*scriptedDynamo)
	metaItem := scriptedAttributeMap(model.ExamMeta{PK: "EXAM#exam", SK: "META", Title: "Unlimited"})
	metaItem["max_attempts"] = &types.AttributeValueMemberN{Value: "0"}
	metaItem["closes_at"] = &types.AttributeValueMemberS{Value: ""}
	metaItem["duration_minutes"] = &types.AttributeValueMemberN{Value: "0"}
	metaItem["allow_late"] = &types.AttributeValueMemberBOOL{Value: false}
	metaItem["late_grace_minutes"] = &types.AttributeValueMemberN{Value: "0"}
	fake.items[itemKey(metaItem)] = metaItem
	seedItem(fake, model.Attempt{PK: "ATTEMPT#exam", SK: "STUDENT#student", AttemptNumber: 1, StartedAt: 100, Submitted: true})
	seedItem(fake, model.Submission{PK: "SUBMISSION#exam", SK: "STUDENT#student", RequestID: "submit-1", AttemptNumber: 1})
	validated, err := s.GetExamMeta(context.Background(), "exam")
	if err != nil || validated == nil {
		t.Fatalf("read zero-valued metadata: %#v, %v", validated, err)
	}
	if _, err := s.RetakeAttemptWithMeta(context.Background(), "exam", "student", 1, validated); err != nil {
		t.Fatalf("unlimited retake with explicit zero metadata: %v", err)
	}
	check := fake.transactions[0].TransactItems[0].ConditionCheck
	for token, value := range map[string]types.AttributeValue{
		":retake_snapshot_0": &types.AttributeValueMemberN{Value: "0"},
		":retake_snapshot_3": &types.AttributeValueMemberS{Value: ""},
		":retake_snapshot_4": &types.AttributeValueMemberN{Value: "0"},
		":retake_snapshot_5": &types.AttributeValueMemberBOOL{Value: false},
		":retake_snapshot_6": &types.AttributeValueMemberN{Value: "0"},
	} {
		if _, exists := check.ExpressionAttributeValues[token]; !exists {
			t.Errorf("CAS omitted explicitly stored value %q: %#v", token, check.ExpressionAttributeValues)
			continue
		}
		if !reflect.DeepEqual(check.ExpressionAttributeValues[token], value) {
			t.Errorf("CAS value %q = %#v, want %#v", token, check.ExpressionAttributeValues[token], value)
		}
	}
}

func TestDynamoAttemptOneCASAcceptsLegacyZeroAndMissingNumbers(t *testing.T) {
	ctx := context.Background()
	assertLegacyCAS := func(t *testing.T, transaction *dynamodb.TransactWriteItemsInput, expectedMissing bool) {
		t.Helper()
		var activePut *types.Put
		for _, item := range transaction.TransactItems {
			if item.Put != nil && avString(item.Put.Item, "pk") == "ATTEMPT#exam" && avString(item.Put.Item, "sk") == "STUDENT#student" {
				activePut = item.Put
				break
			}
		}
		if activePut == nil || activePut.ConditionExpression == nil || !strings.Contains(aws.ToString(activePut.ConditionExpression), "attempt_number = :zero") {
			t.Fatalf("attempt-one CAS does not accept persisted zero: %#v", transaction.TransactItems)
		}
		if activePut.ExpressionAttributeValues[":zero"].(*types.AttributeValueMemberN).Value != "0" {
			t.Fatalf("legacy zero CAS value = %#v", activePut.ExpressionAttributeValues)
		}
		if expectedMissing && !strings.Contains(aws.ToString(activePut.ConditionExpression), "attribute_not_exists(attempt_number)") {
			t.Fatalf("attempt-one CAS does not accept missing number: %s", aws.ToString(activePut.ConditionExpression))
		}
	}

	t.Run("save", func(t *testing.T) {
		s := fixedDynamo()
		fake := s.Client.(*scriptedDynamo)
		seedItem(fake, model.Attempt{PK: "ATTEMPT#exam", SK: "STUDENT#student", AttemptNumber: 0, StartedAt: 100})
		if _, err := s.SaveAttemptForAttempt(ctx, "exam", "student", map[string]int{"0": 1}, 0, "save", 1); err != nil {
			t.Fatal(err)
		}
		assertLegacyCAS(t, fake.transactions[0], true)
	})

	t.Run("submit", func(t *testing.T) {
		s := fixedDynamo()
		fake := s.Client.(*scriptedDynamo)
		seedItem(fake, model.ExamMeta{PK: "EXAM#exam", SK: "META", Title: "Quiz"})
		seedItem(fake, model.Attempt{PK: "ATTEMPT#exam", SK: "STUDENT#student", AttemptNumber: 0, StartedAt: 100})
		question, err := quizQuestionItem("EXAM#exam", 0, model.Question{Question: "q", Answer: 1, Options: []string{"a", "b"}})
		if err != nil {
			t.Fatal(err)
		}
		fake.items[itemKey(question)] = question
		if _, err := s.SubmitAttemptForAttempt(ctx, "exam", "student", "Ada", map[string]int{"0": 1}, "submit", 1); err != nil {
			t.Fatal(err)
		}
		assertLegacyCAS(t, fake.transactions[0], true)
	})

	t.Run("retake", func(t *testing.T) {
		s := fixedDynamo()
		fake := s.Client.(*scriptedDynamo)
		seedItem(fake, model.ExamMeta{PK: "EXAM#exam", SK: "META", MaxAttempts: 2})
		seedItem(fake, model.Attempt{PK: "ATTEMPT#exam", SK: "STUDENT#student", AttemptNumber: 0, StartedAt: 100, Submitted: true})
		seedItem(fake, model.Submission{PK: "SUBMISSION#exam", SK: "STUDENT#student", AttemptNumber: 0, RequestID: "submit", Answers: map[string]int{"0": 1}})
		if _, err := s.RetakeAttempt(ctx, "exam", "student", 1); err != nil {
			t.Fatal(err)
		}
		var activePut *types.Put
		for _, item := range fake.transactions[0].TransactItems {
			if item.Put != nil && avString(item.Put.Item, "pk") == "ATTEMPT#exam" && avString(item.Put.Item, "sk") == "STUDENT#student" {
				activePut = item.Put
				break
			}
		}
		if activePut == nil || activePut.ConditionExpression == nil || !strings.Contains(aws.ToString(activePut.ConditionExpression), "attempt_number = :zero") || activePut.ExpressionAttributeValues[":zero"].(*types.AttributeValueMemberN).Value != "0" {
			t.Fatalf("legacy-zero retake CAS = %#v", activePut)
		}
	})
}
