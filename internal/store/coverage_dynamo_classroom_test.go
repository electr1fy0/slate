package store

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/model"
)

func TestDynamoClassroomLifecycleAndIndexes(t *testing.T) {
	ctx := context.Background()
	s := fixedDynamo()
	fake := s.Client.(*scriptedDynamo)

	if got := classroomMetaKey("c"); avString(got, "pk") != "CLASSROOM#c" || avString(got, "sk") != "META" {
		t.Fatalf("classroom meta key = %#v", got)
	}
	if got := classroomMemberKey("c", "u"); avString(got, "sk") != "MEMBER#u" {
		t.Fatalf("classroom member key = %#v", got)
	}
	if got := classroomCodeKey(" ab "); avString(got, "pk") != "CLASSROOM_CODE#AB" {
		t.Fatalf("classroom code key = %#v", got)
	}
	if got := classroomOwnerIndexKey("o", "c"); avString(got, "pk") != "CLASSROOM_OWNER#o" {
		t.Fatalf("classroom owner index key = %#v", got)
	}
	if got := classroomMemberIndexKey("u", "c"); avString(got, "pk") != "CLASSROOM_MEMBER#u" {
		t.Fatalf("classroom member index key = %#v", got)
	}

	if err := s.CreateClassroom(ctx, nil); err == nil {
		t.Fatal("nil classroom should fail")
	}
	if err := s.CreateClassroom(ctx, &model.Classroom{ID: "bad", OwnerID: "o"}); err == nil {
		t.Fatal("missing join code should fail")
	}
	classroom := &model.Classroom{ID: "c1", OwnerID: "owner", Name: "  Physics  ", JoinCode: " code-1 ", MemberCount: -1}
	if err := s.CreateClassroom(ctx, classroom); err != nil {
		t.Fatal(err)
	}
	if classroom.ID != "c1" || classroom.Name != "Physics" || classroom.JoinCode != "CODE-1" || classroom.MemberCount != 0 || classroom.PK != "CLASSROOM#c1" {
		t.Fatalf("created classroom = %#v", classroom)
	}
	if err := s.CreateClassroom(ctx, &model.Classroom{ID: "c2", OwnerID: "owner", JoinCode: "code-2"}); err != nil {
		t.Fatal(err)
	}
	fake.transactionErrors = []error{&types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}}
	if err := s.CreateClassroom(ctx, &model.Classroom{ID: "c3", OwnerID: "owner", JoinCode: "code-3"}); !errors.Is(err, ErrClassroomCodeTaken) {
		t.Fatalf("classroom transaction conflict = %v", err)
	}
	fake.transactionErrors = []error{errors.New("classroom transaction failed")}
	if err := s.CreateClassroom(ctx, &model.Classroom{ID: "c4", OwnerID: "owner", JoinCode: "code-4"}); err == nil {
		t.Fatal("classroom transaction error should be returned")
	}
	generated := &model.Classroom{OwnerID: "owner", JoinCode: "generated"}
	if err := s.CreateClassroom(ctx, generated); err != nil || generated.ID == "" {
		t.Fatalf("generated classroom = %#v, %v", generated, err)
	}

	if got, err := s.GetClassroom(ctx, "missing"); err != nil || got != nil {
		t.Fatalf("missing classroom = %#v, %v", got, err)
	}
	got, err := s.GetClassroom(ctx, " c1 ")
	if err != nil || got == nil || got.ID != "c1" || got.JoinCode != "CODE-1" {
		t.Fatalf("Dynamo classroom = %#v, %v", got, err)
	}
	fake.getErrors = []error{errors.New("classroom read failed")}
	if _, err := s.GetClassroom(ctx, "c1"); err == nil {
		t.Fatal("GetClassroom should return backend errors")
	}
	seedItem(fake, map[string]any{"pk": "CLASSROOM#bad", "sk": "META", "owner_id": map[string]any{"bad": true}})
	if _, err := s.GetClassroom(ctx, "bad"); err == nil {
		t.Fatal("bad classroom should fail to decode")
	}

	seedItem(fake, classroomIndexItem{PK: "CLASSROOM_OWNER#owner", SK: "CLASSROOM#c1", ClassroomID: "c1"})
	seedItem(fake, classroomIndexItem{PK: "CLASSROOM_OWNER#owner", SK: "CLASSROOM#missing", ClassroomID: "missing"})
	seedItem(fake, classroomIndexItem{PK: "CLASSROOM_OWNER#owner", SK: "CLASSROOM#empty", ClassroomID: ""})
	if rows, err := s.ListClassroomsByOwner(ctx, " owner ", 10); err != nil || len(rows) < 2 || !containsClassroom(rows, "c1") {
		t.Fatalf("owner classrooms = %#v, %v", rows, err)
	}
	seedItem(fake, classroomIndexItem{PK: "CLASSROOM_MEMBER#student", SK: "CLASSROOM#c1", ClassroomID: "c1"})
	if rows, err := s.ListClassroomsForMember(ctx, " student ", 0); err != nil || len(rows) != 1 {
		t.Fatalf("member classrooms = %#v, %v", rows, err)
	}
	fake.queryErrors = []error{errors.New("classroom index query failed")}
	if _, err := s.ListClassroomsByOwner(ctx, "owner", 0); err == nil {
		t.Fatal("owner listing query errors should be returned")
	}

	if found, err := s.FindClassroomByJoinCode(ctx, "missing"); err != nil || found != nil {
		t.Fatalf("missing join code = %#v, %v", found, err)
	}
	seedItem(fake, classroomIndexItem{PK: "CLASSROOM_CODE#JOIN", SK: "CLASSROOM", ClassroomID: "c1"})
	if found, err := s.FindClassroomByJoinCode(ctx, " join "); err != nil || found == nil || found.ID != "c1" {
		t.Fatalf("join code lookup = %#v, %v", found, err)
	}
	seedItem(fake, classroomIndexItem{PK: "CLASSROOM_CODE#EMPTY", SK: "CLASSROOM"})
	if found, err := s.FindClassroomByJoinCode(ctx, "empty"); err != nil || found != nil {
		t.Fatalf("empty join index = %#v, %v", found, err)
	}
	fake.getErrors = []error{errors.New("join code read failed")}
	if _, err := s.FindClassroomByJoinCode(ctx, "join"); err == nil {
		t.Fatal("join code errors should be returned")
	}

	if _, err := s.UpdateClassroom(ctx, "missing", "owner", model.ClassroomPatch{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing classroom update = %v", err)
	}
	if _, err := s.UpdateClassroom(ctx, "c1", "other", model.ClassroomPatch{}); !errors.Is(err, ErrClassroomForbidden) {
		t.Fatalf("forbidden classroom update = %v", err)
	}
	unchanged, err := s.UpdateClassroom(ctx, "c1", "owner", model.ClassroomPatch{})
	if err != nil || unchanged == nil || unchanged.Name != "Physics" {
		t.Fatalf("unchanged classroom update = %#v, %v", unchanged, err)
	}
	name, description := "  Chemistry ", "Experiments"
	updated, err := s.UpdateClassroom(ctx, "c1", "owner", model.ClassroomPatch{Name: &name, Description: &description})
	if err != nil || updated == nil || updated.Name != "Chemistry" || updated.Description != description {
		t.Fatalf("updated classroom = %#v, %v", updated, err)
	}
	fake.updateErrors = []error{&types.ConditionalCheckFailedException{}}
	if _, err := s.UpdateClassroom(ctx, "c1", "owner", model.ClassroomPatch{Name: &name}); !errors.Is(err, ErrClassroomForbidden) {
		t.Fatalf("conditional classroom update = %v", err)
	}
	fake.updateErrors = []error{errors.New("classroom update failed")}
	if _, err := s.UpdateClassroom(ctx, "c1", "owner", model.ClassroomPatch{Name: &name}); err == nil {
		t.Fatal("generic classroom update error should be returned")
	}
	fake.updateAttributes = map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "bad"}, "owner_id": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}}}
	if _, err := s.UpdateClassroom(ctx, "c1", "owner", model.ClassroomPatch{Name: &name}); err == nil {
		t.Fatal("bad classroom update response should fail to decode")
	}
	fake.updateAttributes = nil

	member := &model.ClassroomMember{ClassroomID: "c1", UserID: "student", Name: "Ada", Email: "ada@example.com"}
	if err := s.AddClassroomMember(ctx, nil); err == nil {
		t.Fatal("nil member should fail")
	}
	if err := s.AddClassroomMember(ctx, &model.ClassroomMember{ClassroomID: "missing", UserID: "student"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing classroom member = %v", err)
	}
	fake.getErrors = []error{errors.New("member classroom read failed")}
	if err := s.AddClassroomMember(ctx, &model.ClassroomMember{ClassroomID: "c1", UserID: "student-error"}); err == nil {
		t.Fatal("member classroom read errors should be returned")
	}
	if err := s.AddClassroomMember(ctx, &model.ClassroomMember{ClassroomID: "c1"}); err == nil {
		t.Fatal("member without user id should fail")
	}
	if err := s.AddClassroomMember(ctx, member); err != nil {
		t.Fatal(err)
	}
	if member.Role != "student" || member.PK != "CLASSROOM#c1" || member.SK != "MEMBER#student" {
		t.Fatalf("member defaults = %#v", member)
	}
	if err := s.AddClassroomMember(ctx, member); !errors.Is(err, ErrClassroomMember) {
		t.Fatalf("duplicate member = %v", err)
	}
	fake.transactionErrors = []error{&types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}}
	if err := s.AddClassroomMember(ctx, &model.ClassroomMember{ClassroomID: "c1", UserID: "student-2"}); !errors.Is(err, ErrClassroomMember) {
		t.Fatalf("conditional member add = %v", err)
	}
	fake.transactionErrors = []error{errors.New("member transaction failed")}
	if err := s.AddClassroomMember(ctx, &model.ClassroomMember{ClassroomID: "c1", UserID: "student-3"}); err == nil {
		t.Fatal("member transaction error should be returned")
	}

	if rows, err := s.ListClassroomMembers(ctx, "missing"); !errors.Is(err, ErrNotFound) || rows != nil {
		t.Fatalf("missing roster = %#v, %v", rows, err)
	}
	fake.getErrors = []error{errors.New("roster classroom read failed")}
	if _, err := s.ListClassroomMembers(ctx, "c1"); err == nil {
		t.Fatal("roster classroom read errors should be returned")
	}
	seedItem(fake, model.ClassroomMember{PK: "CLASSROOM#c1", SK: "MEMBER#student-2", ClassroomID: "c1", UserID: "student-2", JoinedAt: "2026-01-01T00:00:00Z"})
	if rows, err := s.ListClassroomMembers(ctx, "c1"); err != nil || len(rows) < 2 {
		t.Fatalf("roster = %#v, %v", rows, err)
	}
	fake.queryErrors = []error{errors.New("roster query failed")}
	if _, err := s.ListClassroomMembers(ctx, "c1"); err == nil {
		t.Fatal("roster query error should be returned")
	}
	fake.queryErrors = nil
	seedItem(fake, map[string]any{"pk": "CLASSROOM#c1", "sk": "MEMBER#bad", "user_id": map[string]any{"bad": true}})
	if _, err := s.ListClassroomMembers(ctx, "c1"); err == nil {
		t.Fatal("bad roster member should fail to decode")
	}

	if ok, err := s.IsClassroomMember(ctx, "c1", "student"); err != nil || !ok {
		t.Fatalf("membership = %v, %v", ok, err)
	}
	if ok, err := s.IsClassroomMember(ctx, "c1", "missing"); err != nil || ok {
		t.Fatalf("missing membership = %v, %v", ok, err)
	}
	fake.getErrors = []error{errors.New("membership read failed")}
	if _, err := s.IsClassroomMember(ctx, "c1", "student"); err == nil {
		t.Fatal("membership errors should be returned")
	}

	if err := s.RemoveClassroomMember(ctx, "missing", "owner", "student"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing classroom removal = %v", err)
	}
	if err := s.RemoveClassroomMember(ctx, "c1", "other", "student"); !errors.Is(err, ErrClassroomForbidden) {
		t.Fatalf("forbidden member removal = %v", err)
	}
	if err := s.RemoveClassroomMember(ctx, "c1", "owner", "missing"); !errors.Is(err, ErrClassroomMemberNotFound) {
		t.Fatalf("missing member removal = %v", err)
	}
	fake.transactionErrors = []error{&types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}}
	if err := s.RemoveClassroomMember(ctx, "c1", "owner", "student"); !errors.Is(err, ErrClassroomMemberNotFound) {
		t.Fatalf("conditional member removal = %v", err)
	}
	fake.transactionErrors = []error{errors.New("remove member transaction failed")}
	if err := s.RemoveClassroomMember(ctx, "c1", "owner", "student"); err == nil {
		t.Fatal("remove member transaction error should be returned")
	}
	if err := s.RemoveClassroomMember(ctx, "c1", "owner", "student"); err != nil {
		t.Fatal(err)
	}
}

func containsClassroom(rows []model.Classroom, id string) bool {
	for _, row := range rows {
		if row.ID == id {
			return true
		}
	}
	return false
}

func TestDynamoDeleteClassroomAndBatchCancellation(t *testing.T) {
	ctx := context.Background()
	s := fixedDynamo()
	fake := s.Client.(*scriptedDynamo)
	if err := s.DeleteClassroom(ctx, "missing", "owner"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing classroom delete = %v", err)
	}
	seedItem(fake, model.Classroom{PK: "CLASSROOM#delete", SK: "META", OwnerID: "owner", Name: "Delete", JoinCode: "DEL"})
	if err := s.DeleteClassroom(ctx, "delete", "other"); !errors.Is(err, ErrClassroomForbidden) {
		t.Fatalf("forbidden classroom delete = %v", err)
	}
	fake.getErrors = []error{errors.New("delete classroom read failed")}
	if err := s.DeleteClassroom(ctx, "delete", "owner"); err == nil {
		t.Fatal("classroom delete read errors should be returned")
	}
	seedItem(fake, model.ClassroomMember{PK: "CLASSROOM#delete", SK: "MEMBER#student", ClassroomID: "delete", UserID: "student", JoinedAt: "now"})
	fake.items["CLASSROOM#delete\x00BROKEN"] = map[string]types.AttributeValue{
		"pk":      &types.AttributeValueMemberS{Value: "CLASSROOM#delete"},
		"sk":      &types.AttributeValueMemberS{Value: "BROKEN"},
		"user_id": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}},
	}
	fake.batchUnprocessed = [][]types.WriteRequest{{{DeleteRequest: &types.DeleteRequest{Key: classroomMetaKey("delete")}}}}
	if err := s.DeleteClassroom(ctx, "delete", "owner"); err != nil {
		t.Fatalf("classroom delete = %v", err)
	}
	if got, _ := s.GetClassroom(ctx, "delete"); got != nil {
		t.Fatal("deleted classroom still exists")
	}

	seedItem(fake, model.Classroom{PK: "CLASSROOM#query-error", SK: "META", OwnerID: "owner", JoinCode: "QE"})
	fake.queryErrors = []error{errors.New("delete classroom query failed")}
	if err := s.DeleteClassroom(ctx, "query-error", "owner"); err == nil {
		t.Fatal("classroom delete query errors should be returned")
	}
	seedItem(fake, model.Classroom{PK: "CLASSROOM#batch-error", SK: "META", OwnerID: "owner", JoinCode: "BE"})
	fake.batchErrors = []error{errors.New("classroom delete batch failed")}
	if err := s.DeleteClassroom(ctx, "batch-error", "owner"); err == nil {
		t.Fatal("classroom delete batch errors should be returned")
	}
	seedItem(fake, model.Classroom{PK: "CLASSROOM#cancel", SK: "META", OwnerID: "owner", JoinCode: "CANCEL"})
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	fake.batchUnprocessed = [][]types.WriteRequest{{{DeleteRequest: &types.DeleteRequest{Key: classroomMetaKey("cancel")}}}}
	if err := s.DeleteClassroom(cancelled, "cancel", "owner"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled classroom delete = %v", err)
	}
}
