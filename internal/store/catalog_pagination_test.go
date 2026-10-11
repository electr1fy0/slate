package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/model"
)

func TestCatalogContinuesAfterFilteredEmptyDynamoPage(t *testing.T) {
	s := fixedDynamo()
	fake := s.Client.(*scriptedDynamo)
	row, err := marshal(model.ExamMeta{PK: "EXAM#later", SK: "META", Title: "Later page", CreatedAt: "2026-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	cursor := key("USER#unrelated", "PROFILE")
	fake.scanPages = []*dynamodb.ScanOutput{
		{LastEvaluatedKey: cursor},
		{Items: []map[string]types.AttributeValue{row}},
	}
	rows, err := s.ListExams(context.Background(), -1)
	if err != nil || len(rows) != 1 || rows[0].ID != "later" {
		t.Fatalf("catalog = %#v, %v", rows, err)
	}
	if len(fake.scans) != 2 || itemKey(fake.scans[1].ExclusiveStartKey) != itemKey(cursor) {
		t.Fatal("catalog did not follow the scan cursor")
	}
}

func TestFullCatalogAndDraftBrowseDoNotTruncateAtFifty(t *testing.T) {
	ctx := context.Background()
	memory := NewMemory("test")
	dynamo := fixedDynamo()
	fake := dynamo.Client.(*scriptedDynamo)
	examItems, draftItems := []map[string]types.AttributeValue{}, []map[string]types.AttributeValue{}
	for index := 0; index < 65; index++ {
		id := fmt.Sprintf("item-%02d", index)
		if err := memory.EnsureExam(ctx, id, id, []model.Question{{Question: "Q", Options: []string{"A", "B"}}}, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := memory.CreateQuizDraft(ctx, &model.QuizDraft{ID: "draft-" + id, OwnerID: "teacher", Title: id}); err != nil {
			t.Fatal(err)
		}
		examItem, _ := marshal(model.ExamMeta{PK: "EXAM#" + id, SK: "META", Title: id})
		draftItem, _ := marshal(model.QuizDraft{PK: "QUIZ#" + id, SK: "META", Title: id, OwnerID: "teacher", Status: "draft"})
		examItems = append(examItems, examItem)
		draftItems = append(draftItems, draftItem)
	}
	fake.scanPages = []*dynamodb.ScanOutput{{Items: examItems}}
	exams, err := dynamo.ListExams(ctx, -1)
	if err != nil || len(exams) != 65 {
		t.Fatalf("Dynamo catalog = %d, %v", len(exams), err)
	}
	fake.scanPages = []*dynamodb.ScanOutput{{Items: draftItems}}
	drafts, err := dynamo.ListQuizDrafts(ctx, "teacher", -1)
	if err != nil || len(drafts) != 65 {
		t.Fatalf("Dynamo drafts = %d, %v", len(drafts), err)
	}
	memoryExams, _ := memory.ListExams(ctx, -1)
	memoryDrafts, _ := memory.ListQuizDrafts(ctx, "teacher", -1)
	if len(memoryExams) != 65 || len(memoryDrafts) != 65 {
		t.Fatalf("Memory catalog/drafts = %d/%d", len(memoryExams), len(memoryDrafts))
	}
	limited, _ := memory.ListQuizDrafts(ctx, "teacher", 10)
	if len(limited) != 10 {
		t.Fatalf("explicit page size = %d", len(limited))
	}
}
