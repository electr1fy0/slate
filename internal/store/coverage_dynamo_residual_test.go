package store

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/model"
)

func TestDynamoResidualValidationAndDecoding(t *testing.T) {
	ctx := context.Background()
	s := fixedDynamo()
	fake := s.Client.(*scriptedDynamo)
	q := model.Question{Question: "q", Options: []string{"a"}, Answer: 1}

	fake.putErrors = []error{errors.New("definition put failed")}
	if err := s.writeExamDefinition(ctx, "definition-error", model.ExamMeta{}, nil, false); err == nil {
		t.Fatal("definition put errors should be returned")
	}
	fake.putErrors = []error{nil, errors.New("definition question put failed")}
	if err := s.writeExamDefinition(ctx, "definition-question-error", model.ExamMeta{}, []model.Question{q}, false); err == nil {
		t.Fatal("definition question put errors should be returned")
	}

	tooMany := make([]model.Question, 101)
	if _, err := s.CreateQuizDraft(ctx, &model.QuizDraft{ID: "too-many", OwnerID: "owner", Title: "Too many", Questions: tooMany}); err == nil {
		t.Fatal("more than 100 draft questions should fail")
	}
	maxQuestions := make([]model.Question, 100)
	if _, err := s.CreateQuizDraft(ctx, &model.QuizDraft{ID: "too-large", OwnerID: "owner", Title: "Too large", Questions: maxQuestions}); err == nil {
		t.Fatal("a transaction larger than DynamoDB's limit should fail")
	}

	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{{"pk": &types.AttributeValueMemberS{Value: "QUIZ#bad-meta"}, "sk": &types.AttributeValueMemberS{Value: "META"}, "title": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}}}}}}
	if _, err := s.GetQuizDraft(ctx, "bad-meta"); err == nil {
		t.Fatal("bad draft metadata should fail to decode")
	}
	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{{"pk": &types.AttributeValueMemberS{Value: "QUIZ#bad-question"}, "sk": &types.AttributeValueMemberS{Value: "META"}, "title": &types.AttributeValueMemberS{Value: "Draft"}}, {"pk": &types.AttributeValueMemberS{Value: "QUIZ#bad-question"}, "sk": &types.AttributeValueMemberS{Value: "QUESTION#0"}, "question": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}}}}}}
	if _, err := s.GetQuizDraft(ctx, "bad-question"); err == nil {
		t.Fatal("bad draft question should fail to decode")
	}
	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{{"sk": &types.AttributeValueMemberS{Value: "META"}, "title": &types.AttributeValueMemberS{Value: "Fallback"}}}}}
	fallback, err := s.GetQuizDraft(ctx, "fallback")
	if err != nil || fallback == nil || fallback.ID != "fallback" || fallback.Status != "draft" {
		t.Fatalf("fallback draft = %#v, %v", fallback, err)
	}

	fake.scanPages = []*dynamodb.ScanOutput{{Items: []map[string]types.AttributeValue{
		scriptedAttributeMap(model.QuizDraft{PK: "QUIZ#same-b", SK: "META", OwnerID: "owner", Status: "draft", UpdatedAt: "same"}),
		scriptedAttributeMap(model.QuizDraft{PK: "QUIZ#same-a", SK: "META", OwnerID: "owner", Status: "draft", UpdatedAt: "same"}),
	}}}
	if rows, err := s.ListQuizDrafts(ctx, "owner", 1); err != nil || len(rows) != 1 || rows[0].ID != "same-a" {
		t.Fatalf("tie-sorted drafts = %#v, %v", rows, err)
	}
	fake.scanPages = []*dynamodb.ScanOutput{{Items: []map[string]types.AttributeValue{{"pk": &types.AttributeValueMemberS{Value: "QUIZ#bad-list"}, "sk": &types.AttributeValueMemberS{Value: "META"}, "title": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}}}}}}
	if _, err := s.ListQuizDrafts(ctx, "admin", 1); err == nil {
		t.Fatal("bad listed draft should fail to decode")
	}

	seedItem(fake, model.QuizDraft{PK: "QUIZ#delete-residual", SK: "META", OwnerID: "owner", Title: "Delete", Status: "draft"})
	if _, err := s.deleteByPK(ctx, "QUIZ#delete-residual"); err != nil {
		t.Fatal(err)
	}
	seedItem(fake, model.QuizDraft{PK: "QUIZ#delete-query-error", SK: "META", OwnerID: "owner", Title: "Delete", Status: "draft"})
	fake.queryErrors = []error{errors.New("delete by key query failed")}
	if _, err := s.deleteByPK(ctx, "QUIZ#delete-query-error"); err == nil {
		t.Fatal("deleteByPK query errors should be returned")
	}
	seedItem(fake, model.QuizDraft{PK: "QUIZ#delete-cancel", SK: "META", OwnerID: "owner", Title: "Delete", Status: "draft"})
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	fake.batchUnprocessed = [][]types.WriteRequest{{{DeleteRequest: &types.DeleteRequest{Key: key("QUIZ#delete-cancel", "META")}}}}
	if _, err := s.deleteByPK(cancelled, "QUIZ#delete-cancel"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled deleteByPK = %v", err)
	}

	seedItem(fake, model.QuizDraft{PK: "QUIZ#residual-update", SK: "META", OwnerID: "owner", Title: "Draft", Status: "draft"})
	if _, err := s.UpdateQuizDraft(ctx, "owner", &model.QuizDraft{ID: "residual-update", Title: ""}); err == nil {
		t.Fatal("empty updated draft title should fail")
	}
	if _, err := s.UpdateQuizDraft(ctx, "owner", &model.QuizDraft{ID: "residual-update", Title: "x", Questions: tooMany}); err == nil {
		t.Fatal("too many updated questions should fail")
	}
	seedItem(fake, model.QuizDraft{PK: "QUIZ#status-update", SK: "META", OwnerID: "owner", Title: "Published", Status: "published"})
	if _, err := s.UpdateQuizDraft(ctx, "owner", &model.QuizDraft{ID: "status-update", Title: "x"}); !errors.Is(err, ErrQuizConflict) {
		t.Fatalf("published draft update = %v", err)
	}
	seedItem(fake, model.QuizDraft{PK: "QUIZ#query-update", SK: "META", OwnerID: "owner", Title: "Draft", Status: "draft"})
	fake.queryErrors = []error{errors.New("update read failed")}
	if _, err := s.UpdateQuizDraft(ctx, "owner", &model.QuizDraft{ID: "query-update", Title: "x"}); err == nil {
		t.Fatal("update read errors should be returned")
	}
	seedItem(fake, model.QuizDraft{PK: "QUIZ#delete-update", SK: "META", OwnerID: "owner", Title: "Draft", Status: "draft"})
	fake.transactionErrors = []error{errors.New("draft update transaction failed")}
	if _, err := s.UpdateQuizDraft(ctx, "owner", &model.QuizDraft{ID: "delete-update", Title: "x"}); err == nil {
		t.Fatal("draft update transaction errors should be returned")
	}
	seedItem(fake, model.QuizDraft{PK: "QUIZ#put-update", SK: "META", OwnerID: "owner", Title: "Draft", Status: "draft"})
	fake.putErrors = []error{nil, errors.New("update question put failed")}
	if _, err := s.UpdateQuizDraft(ctx, "owner", &model.QuizDraft{ID: "put-update", Title: "x", Questions: []model.Question{q}}); err == nil {
		t.Fatal("update question put errors should be returned")
	}

	seedItem(fake, model.QuizDraft{PK: "QUIZ#publish-read-error", SK: "META", OwnerID: "owner", Title: "Draft", Status: "draft"})
	fake.queryErrors = []error{errors.New("publish draft read failed")}
	if _, err := s.PublishQuizDraft(ctx, "publish-read-error", "owner"); err == nil {
		t.Fatal("publish draft read errors should be returned")
	}
	seedItem(fake, model.QuizDraft{PK: "QUIZ#publish-meta-error", SK: "META", OwnerID: "owner", Title: "Draft", Status: "draft", Questions: []model.Question{q}})
	qItem, _ := quizQuestionItem("QUIZ#publish-meta-error", 0, q)
	fake.items[itemKey(qItem)] = qItem
	fake.getErrors = []error{errors.New("publish exam metadata read failed")}
	if _, err := s.PublishQuizDraft(ctx, "publish-meta-error", "owner"); err == nil {
		t.Fatal("publish metadata read errors should be returned")
	}

	fake.queryErrors = []error{errors.New("duplicate draft read failed")}
	if _, err := s.DuplicateQuiz(ctx, "duplicate-error", "owner", "copy", "copy"); err == nil {
		t.Fatal("duplicate draft read errors should be returned")
	}
	fake.queryErrors = []error{nil, errors.New("duplicate exam read failed")}
	if _, err := s.DuplicateQuiz(ctx, "duplicate-exam-error", "owner", "copy", "copy"); err == nil {
		t.Fatal("duplicate exam read errors should be returned")
	}

	seedItem(fake, model.ExamMeta{PK: "EXAM#window-bad", SK: "META", Title: "Window"})
	fake.updateAttributes = map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "EXAM#window-bad"}, "sk": &types.AttributeValueMemberS{Value: "META"}, "title": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}}}
	if _, err := s.UpdateExamWindow(ctx, "window-bad", aws.String("new"), nil); err == nil {
		t.Fatal("bad window response should fail to decode")
	}
	fake.updateAttributes = nil
}

func TestDynamoFinalComparatorAndErrorBranches(t *testing.T) {
	ctx := context.Background()
	s := fixedDynamo()
	fake := s.Client.(*scriptedDynamo)

	// Two questions make the GetExam sort comparator execute.
	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{
		scriptedAttributeMap(model.ExamMeta{PK: "EXAM#sort", SK: "META", Title: "Sort"}),
		scriptedAttributeMap(questionItem{PK: "EXAM#sort", SK: "QUESTION#002", Question: "two"}),
		scriptedAttributeMap(questionItem{PK: "EXAM#sort", SK: "QUESTION#001", Question: "one"}),
	}}}
	if exam, err := s.GetExam(ctx, "sort", true); err != nil || exam == nil || len(exam.Questions) != 2 || exam.Questions[0].Index != 1 {
		t.Fatalf("sorted exam = %#v, %v", exam, err)
	}
	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{{"sk": &types.AttributeValueMemberS{Value: "QUESTION#bad"}}}}}
	if _, _, err := s.GradeAttempt(ctx, "bad-index", nil); err != nil {
		t.Fatal(err)
	}

	fake.putErrors = []error{&types.ConditionalCheckFailedException{}}
	fake.getErrors = []error{nil, nil, nil}
	fake.getResponses = []map[string]types.AttributeValue{nil, {"pk": &types.AttributeValueMemberS{Value: "ATTEMPT#bad-winner"}, "sk": &types.AttributeValueMemberS{Value: "STUDENT#student"}, "started_at": &types.AttributeValueMemberS{Value: "bad"}}}
	if _, err := s.RecordAttemptStart(ctx, "bad-winner", "student"); err == nil {
		t.Fatal("bad conditional attempt winner should fail to decode")
	}

	fake.scanPages = []*dynamodb.ScanOutput{{Items: []map[string]types.AttributeValue{
		scriptedAttributeMap(model.OutboxEntry{PK: "JOURNAL#1", SK: "OUTBOX", RequestID: "1", CreatedAt: 2}),
		scriptedAttributeMap(model.OutboxEntry{PK: "JOURNAL#2", SK: "OUTBOX", RequestID: "2", CreatedAt: 1}),
	}}}
	if rows, err := s.ListOutbox(ctx, 1); err != nil || len(rows) != 2 || rows[0].RequestID != "2" || rows[1].RequestID != "1" {
		t.Fatalf("sorted outbox = %#v, %v", rows, err)
	}
	fake.scanPages = []*dynamodb.ScanOutput{{Items: []map[string]types.AttributeValue{scriptedAttributeMap(model.Submission{PK: "SUBMISSION#limit", SK: "STUDENT#student", RequestID: "limit"})}}}
	if rows, err := s.ListSubmissions(ctx, "", 0); err != nil || len(rows) != 1 {
		t.Fatalf("default submission limit = %#v, %v", rows, err)
	}

	seedItem(fake, classroomIndexItem{PK: "CLASSROOM_OWNER#refs", SK: "BROKEN", ClassroomID: "refs"})
	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{{"pk": &types.AttributeValueMemberS{Value: "CLASSROOM_OWNER#refs"}, "sk": &types.AttributeValueMemberS{Value: "BROKEN"}, "classroom_id": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}}}}}}
	if _, err := s.ListClassroomsByOwner(ctx, "refs", 1); err == nil {
		t.Fatal("bad classroom reference should fail")
	}
	seedItem(fake, classroomIndexItem{PK: "CLASSROOM_OWNER#error", SK: "CLASSROOM#c", ClassroomID: "c"})
	fake.getErrors = []error{errors.New("referenced classroom read failed")}
	if _, err := s.ListClassroomsByOwner(ctx, "error", 1); err == nil {
		t.Fatal("referenced classroom read errors should be returned")
	}
	seedItem(fake, model.Classroom{PK: "CLASSROOM#early", SK: "META", OwnerID: "sort-owner", JoinCode: "EARLY", CreatedAt: "2026-01-01T00:00:00Z"})
	seedItem(fake, model.Classroom{PK: "CLASSROOM#late", SK: "META", OwnerID: "sort-owner", JoinCode: "LATE", CreatedAt: "2026-02-01T00:00:00Z"})
	seedItem(fake, classroomIndexItem{PK: "CLASSROOM_OWNER#sort-owner", SK: "CLASSROOM#early", ClassroomID: "early"})
	seedItem(fake, classroomIndexItem{PK: "CLASSROOM_OWNER#sort-owner", SK: "CLASSROOM#late", ClassroomID: "late"})
	if rows, err := s.ListClassroomsByOwner(ctx, "sort-owner", 1); err != nil || len(rows) != 1 {
		t.Fatalf("sorted classroom references = %#v, %v", rows, err)
	}
	seedItem(fake, model.Classroom{PK: "CLASSROOM#member-sort", SK: "META", ID: "member-sort", OwnerID: "owner", JoinCode: "MSORT"})
	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{
		scriptedAttributeMap(model.ClassroomMember{PK: "CLASSROOM#member-sort", SK: "MEMBER#b", UserID: "b", JoinedAt: "same"}),
		scriptedAttributeMap(model.ClassroomMember{PK: "CLASSROOM#member-sort", SK: "MEMBER#a", UserID: "a", JoinedAt: "same"}),
	}}}
	if rows, err := s.ListClassroomMembers(ctx, "member-sort"); err != nil || len(rows) != 2 || rows[0].UserID != "a" {
		t.Fatalf("sorted classroom members = %#v, %v", rows, err)
	}
	seedItem(fake, model.Classroom{PK: "CLASSROOM#delete-bad-key", SK: "META", ID: "delete-bad-key", OwnerID: "owner", JoinCode: "DBK"})
	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{{"junk": &types.AttributeValueMemberS{Value: "malformed"}}}}}
	if err := s.DeleteClassroom(ctx, "delete-bad-key", "owner"); err != nil {
		t.Fatalf("delete with malformed member key = %v", err)
	}
	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{{"pk": &types.AttributeValueMemberS{Value: "CLASSROOM_MEMBER#student"}, "sk": &types.AttributeValueMemberS{Value: "BROKEN"}, "classroom_id": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}}}}}}
	if _, err := s.ListClassroomsForMember(ctx, "student", 0); err == nil {
		t.Fatal("member reference errors should be returned")
	}
	fake.queryErrors = []error{errors.New("member classroom index query failed")}
	if _, err := s.ListClassroomsForMember(ctx, "student", 0); err == nil {
		t.Fatal("member classroom query errors should be returned")
	}

	seedItem(fake, map[string]any{"pk": "CLASSROOM_CODE#BADREF", "sk": "CLASSROOM", "classroom_id": map[string]any{"bad": true}})
	if _, err := s.FindClassroomByJoinCode(ctx, "badref"); err == nil {
		t.Fatal("bad classroom code reference should fail")
	}
	seedItem(fake, model.Classroom{PK: "CLASSROOM#update-read-error", SK: "META", OwnerID: "owner", JoinCode: "URE"})
	fake.getErrors = []error{errors.New("update classroom read failed")}
	if _, err := s.UpdateClassroom(ctx, "update-read-error", "owner", model.ClassroomPatch{Name: aws.String("x")}); err == nil {
		t.Fatal("update classroom read errors should be returned")
	}
	seedItem(fake, model.Classroom{PK: "CLASSROOM#member-read-error", SK: "META", OwnerID: "owner", JoinCode: "MRE"})
	fake.getErrors = []error{nil, errors.New("member existence read failed")}
	if err := s.AddClassroomMember(ctx, &model.ClassroomMember{ClassroomID: "member-read-error", UserID: "student"}); err == nil {
		t.Fatal("member existence read errors should be returned")
	}
	seedItem(fake, model.Classroom{PK: "CLASSROOM#remove-read-error", SK: "META", OwnerID: "owner", JoinCode: "RRE"})
	fake.getErrors = []error{errors.New("remove classroom read failed")}
	if err := s.RemoveClassroomMember(ctx, "remove-read-error", "owner", "student"); err == nil {
		t.Fatal("remove classroom read errors should be returned")
	}
	fake.getErrors = []error{nil, errors.New("remove member read failed")}
	if err := s.RemoveClassroomMember(ctx, "remove-read-error", "owner", "student"); err == nil {
		t.Fatal("remove member read errors should be returned")
	}
}
