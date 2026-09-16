package control

import (
	"bytes"
	"context"
	"errors"
	"testing"

	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type edgeReconcilerStore struct {
	pages    []ScanPage
	scanErr  error
	getErr   error
	current  map[string]dynamodbtypes.AttributeValue
	putErr   error
	putItems []map[string]dynamodbtypes.AttributeValue
}

func (s *edgeReconcilerStore) Scan(context.Context, map[string]dynamodbtypes.AttributeValue, int64) (ScanPage, error) {
	if s.scanErr != nil {
		return ScanPage{}, s.scanErr
	}
	if len(s.pages) == 0 {
		return ScanPage{}, nil
	}
	page := s.pages[0]
	s.pages = s.pages[1:]
	return page, nil
}

func (s *edgeReconcilerStore) GetItem(context.Context, map[string]dynamodbtypes.AttributeValue) (map[string]dynamodbtypes.AttributeValue, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.current, nil
}

func (s *edgeReconcilerStore) PutReconciled(context.Context, map[string]dynamodbtypes.AttributeValue, int) error {
	if s.putErr != nil {
		return s.putErr
	}
	s.putItems = append(s.putItems, map[string]dynamodbtypes.AttributeValue{})
	return nil
}

type batchMetrics struct {
	putCalls   int
	batchCalls int
	err        error
}

func (m *batchMetrics) Put(context.Context, string, Metric) error {
	m.putCalls++
	return m.err
}

func (m *batchMetrics) PutBatch(context.Context, string, []Metric) error {
	m.batchCalls++
	return m.err
}

func validJournalEntry() map[string]dynamodbtypes.AttributeValue {
	return map[string]dynamodbtypes.AttributeValue{
		"target_pk":    &dynamodbtypes.AttributeValueMemberS{Value: "SUBMISSION#e"},
		"target_sk":    &dynamodbtypes.AttributeValueMemberS{Value: "STUDENT#s"},
		"request_id":   &dynamodbtypes.AttributeValueMemberS{Value: "request"},
		"version":      &dynamodbtypes.AttributeValueMemberN{Value: "2"},
		"created_at":   &dynamodbtypes.AttributeValueMemberN{Value: "100"},
		"answers":      &dynamodbtypes.AttributeValueMemberM{},
		"student_name": &dynamodbtypes.AttributeValueMemberS{Value: "Student"},
		"score":        &dynamodbtypes.AttributeValueMemberN{Value: "1"},
		"total":        &dynamodbtypes.AttributeValueMemberN{Value: "2"},
	}
}

func TestReconcilerCoverage(t *testing.T) {
	scanErr := errors.New("scan failed")
	if _, err := NewReconciler(ReconcilerConfig{}, &edgeReconcilerStore{scanErr: scanErr}, nil).Handle(context.Background(), nil); !errors.Is(err, scanErr) {
		t.Fatalf("scan error = %v", err)
	}

	missingFields := []string{"target_pk", "target_sk", "request_id", "version", "created_at"}
	for _, field := range missingFields {
		attrs := validJournalEntry()
		delete(attrs, field)
		if _, err := decodeJournalEntry(attrs); err == nil {
			t.Fatalf("missing journal field %s accepted", field)
		}
	}
	if _, err := NewReconciler(ReconcilerConfig{}, &edgeReconcilerStore{pages: []ScanPage{{Items: []map[string]dynamodbtypes.AttributeValue{{}}}}}, nil).Handle(context.Background(), nil); err == nil {
		t.Fatal("invalid journal entry was not rejected by Handle")
	}
	invalidVersion := validJournalEntry()
	invalidVersion["version"] = &dynamodbtypes.AttributeValueMemberS{Value: "2"}
	if _, err := decodeJournalEntry(invalidVersion); err == nil {
		t.Fatal("invalid journal version accepted")
	}
	invalidCreated := validJournalEntry()
	invalidCreated["created_at"] = &dynamodbtypes.AttributeValueMemberN{Value: "bad"}
	if _, err := decodeJournalEntry(invalidCreated); err == nil {
		t.Fatal("invalid journal timestamp accepted")
	}
	if _, ok := attributeInt64(&dynamodbtypes.AttributeValueMemberN{Value: "bad"}); ok {
		t.Fatal("invalid int64 accepted")
	}

	getErr := errors.New("get failed")
	getStore := &edgeReconcilerStore{pages: []ScanPage{{Items: []map[string]dynamodbtypes.AttributeValue{validJournalEntry()}}}, getErr: getErr}
	if _, err := NewReconciler(ReconcilerConfig{}, getStore, nil).Handle(context.Background(), nil); !errors.Is(err, getErr) {
		t.Fatalf("get error = %v", err)
	}

	already := validJournalEntry()
	alreadyStore := &edgeReconcilerStore{pages: []ScanPage{{Items: []map[string]dynamodbtypes.AttributeValue{already}}}, current: map[string]dynamodbtypes.AttributeValue{
		"request_id": &dynamodbtypes.AttributeValueMemberS{Value: "request"},
	}}
	alreadyResult, err := NewReconciler(ReconcilerConfig{}, alreadyStore, nil).Handle(context.Background(), nil)
	if err != nil || alreadyResult.AlreadyReplicated != 1 {
		t.Fatalf("already replicated = %#v %v", alreadyResult, err)
	}

	badCurrent := validJournalEntry()
	badCurrentStore := &edgeReconcilerStore{pages: []ScanPage{{Items: []map[string]dynamodbtypes.AttributeValue{badCurrent}}}, current: map[string]dynamodbtypes.AttributeValue{
		"version": &dynamodbtypes.AttributeValueMemberS{Value: "bad"},
	}}
	if _, err := NewReconciler(ReconcilerConfig{}, badCurrentStore, nil).Handle(context.Background(), nil); err == nil {
		t.Fatal("invalid current version accepted")
	}

	putErr := errors.New("put failed")
	putStore := &edgeReconcilerStore{pages: []ScanPage{{Items: []map[string]dynamodbtypes.AttributeValue{validJournalEntry()}}}, putErr: putErr}
	if _, err := NewReconciler(ReconcilerConfig{}, putStore, nil).Handle(context.Background(), nil); !errors.Is(err, putErr) {
		t.Fatalf("put error = %v", err)
	}

	batch := &batchMetrics{}
	batchStore := &edgeReconcilerStore{pages: []ScanPage{{}}}
	reconciler := NewReconciler(ReconcilerConfig{MetricNamespace: "Ops"}, batchStore, batch)
	reconciler.Log = nil
	if result, err := reconciler.Handle(context.Background(), nil); err != nil || result.Scanned != 0 || batch.batchCalls != 1 || batch.putCalls != 0 {
		t.Fatalf("batch metrics result = %#v %v calls=%d/%d", result, err, batch.batchCalls, batch.putCalls)
	}
	batch.err = errors.New("batch failed")
	batchStore.pages = []ScanPage{{}}
	if _, err := reconciler.Handle(context.Background(), nil); !errors.Is(err, batch.err) {
		t.Fatalf("batch error = %v", err)
	}

	r := NewReconciler(ReconcilerConfig{}, &edgeReconcilerStore{pages: []ScanPage{{}}}, nil)
	r.Log = nil
	r.printJSON(map[string]any{"ok": true})
	r.Log = &bytes.Buffer{}
	r.printJSON(func() {})
}
