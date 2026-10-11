package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/clusterstate"
	"github.com/ayush/slate/internal/config"
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
	seedItem(fake, classroomIndexItem{PK: "CLASSROOM_CODE#DEL", SK: "CLASSROOM", ClassroomID: "delete"})
	seedItem(fake, classroomInvitationCodeRef{PK: "CLASSROOM_INVITE_CODE#DEL", SK: "INVITATION", ClassroomID: "other-classroom", InvitationID: "unrelated-invite"})
	seedItem(fake, classroomIndexItem{PK: "CLASSROOM_OWNER#owner", SK: "CLASSROOM#delete", ClassroomID: "delete"})
	seedItem(fake, model.ClassroomMember{PK: "CLASSROOM#delete", SK: "MEMBER#student", ClassroomID: "delete", UserID: "student", JoinedAt: "now"})
	seedItem(fake, classroomIndexItem{PK: "CLASSROOM_MEMBER#student", SK: "CLASSROOM#delete", ClassroomID: "delete"})
	seedItem(fake, model.ClassroomInvitation{PK: "CLASSROOM#delete", SK: "INVITATION#invite-1", ID: "invite-1", ClassroomID: "delete", Email: "student@example.test", JoinCode: "INVITE1", Status: "pending"})
	seedItem(fake, classroomInvitationCodeRef{PK: "CLASSROOM_INVITE_CODE#INVITE1", SK: "INVITATION", ClassroomID: "delete", InvitationID: "invite-1"})
	for index := 0; index < 26; index++ {
		seedItem(fake, map[string]any{"pk": "CLASSROOM#delete", "sk": fmt.Sprintf("CHILD#%02d", index)})
	}
	fake.items["CLASSROOM#delete\x00BROKEN"] = map[string]types.AttributeValue{
		"pk":      &types.AttributeValueMemberS{Value: "CLASSROOM#delete"},
		"sk":      &types.AttributeValueMemberS{Value: "BROKEN"},
		"user_id": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}},
	}
	// Lookup cleanup completes, then the first child chunk commits. The next
	// chunk fails, leaving META/owner intact and allowing a retry to resume.
	fake.batchErrors = []error{nil, nil, errors.New("second child chunk failed")}
	if err := s.DeleteClassroom(ctx, "delete", "owner"); err == nil {
		t.Fatal("partial classroom cleanup error was swallowed")
	}
	for _, key := range []map[string]types.AttributeValue{classroomMetaKey("delete"), classroomCodeKey("DEL"), classroomOwnerIndexKey("owner", "delete")} {
		if fake.items[itemKey(key)] == nil {
			t.Fatalf("root key removed before child cleanup completed: %s", itemKey(key))
		}
	}
	if fake.items[itemKey(classroomMemberIndexKey("student", "delete"))] != nil || fake.items[itemKey(invitationCodeKey("INVITE1"))] != nil {
		t.Fatal("member or invitation lookup remained after the lookup cleanup phase")
	}
	if fake.items[itemKey(invitationCodeKey("DEL"))] == nil {
		t.Fatal("classroom join code collision removed an unrelated invitation lookup")
	}
	if len(classroomChildren(fake, "delete")) == 0 {
		t.Fatal("child cleanup did not leave remaining rows for the retry")
	}
	if got, _ := s.GetClassroom(ctx, "delete"); got != nil {
		t.Fatal("deleting classroom remained visible to ordinary reads")
	}
	if rooms, err := s.ListClassroomsByOwner(ctx, "owner", 10); err != nil || containsClassroom(rooms, "delete") {
		t.Fatalf("deleting classroom remained in owner listing: %#v, %v", rooms, err)
	}
	if got, err := s.FindClassroomByJoinCode(ctx, "DEL"); err != nil || got != nil {
		t.Fatalf("deleting classroom remained joinable: %#v, %v", got, err)
	}
	if err := s.AddClassroomMember(ctx, &model.ClassroomMember{ClassroomID: "delete", UserID: "new-student"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("join raced into deleting classroom: %v", err)
	}
	if err := s.CreateClassroomInvitation(ctx, "owner", &model.ClassroomInvitation{ClassroomID: "delete", Email: "new@example.test", JoinCode: "NEWINVITE"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invitation was added to deleting classroom: %v", err)
	}
	if _, err := s.UpdateClassroom(ctx, "delete", "owner", model.ClassroomPatch{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting classroom remained editable: %v", err)
	}
	if err := s.AcceptClassroomInvitation(ctx, "delete", "invite-1", &model.ClassroomMember{UserID: "student", Email: "student@example.test"}); err == nil {
		t.Fatal("invitation acceptance succeeded after delete was fenced")
	}
	if err := s.DeleteClassroom(ctx, "delete", "owner"); err != nil {
		t.Fatalf("retry classroom delete = %v", err)
	}
	if fake.items[itemKey(classroomMetaKey("delete"))] != nil || fake.items[itemKey(classroomCodeKey("DEL"))] != nil || fake.items[itemKey(classroomOwnerIndexKey("owner", "delete"))] != nil {
		t.Fatal("successful retry did not remove the final root keys")
	}
	if len(classroomChildren(fake, "delete")) != 0 || fake.items[itemKey(classroomMemberIndexKey("student", "delete"))] != nil || fake.items[itemKey(invitationCodeKey("INVITE1"))] != nil {
		t.Fatal("successful retry left classroom children or lookup records")
	}
	if fake.items[itemKey(invitationCodeKey("DEL"))] == nil {
		t.Fatal("successful deletion removed unrelated invitation lookup")
	}

	seedItem(fake, model.Classroom{PK: "CLASSROOM#query-error", SK: "META", OwnerID: "owner", JoinCode: "QE"})
	fake.queryErrors = []error{errors.New("delete classroom query failed")}
	if err := s.DeleteClassroom(ctx, "query-error", "owner"); err == nil {
		t.Fatal("classroom delete query errors should be returned")
	}
	seedItem(fake, model.Classroom{PK: "CLASSROOM#batch-error", SK: "META", OwnerID: "owner", JoinCode: "BE"})
	seedItem(fake, map[string]any{"pk": "CLASSROOM#batch-error", "sk": "CHILD#1"})
	fake.batchErrors = []error{errors.New("classroom delete batch failed")}
	if err := s.DeleteClassroom(ctx, "batch-error", "owner"); err == nil {
		t.Fatal("classroom delete batch errors should be returned")
	}
	seedItem(fake, model.Classroom{PK: "CLASSROOM#cancel", SK: "META", OwnerID: "owner", JoinCode: "CANCEL"})
	seedItem(fake, model.ClassroomMember{PK: "CLASSROOM#cancel", SK: "MEMBER#student", ClassroomID: "cancel", UserID: "student"})
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	fake.batchUnprocessed = [][]types.WriteRequest{{{DeleteRequest: &types.DeleteRequest{Key: classroomMemberIndexKey("student", "cancel")}}}}
	if err := s.DeleteClassroom(cancelled, "cancel", "owner"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled classroom delete = %v", err)
	}
}

func classroomChildren(fake *scriptedDynamo, classroomID string) []string {
	prefix := "CLASSROOM#" + classroomID + "\x00"
	var children []string
	for id := range fake.items {
		if strings.HasPrefix(id, prefix) && !strings.HasSuffix(id, "\x00META") {
			children = append(children, id)
		}
	}
	return children
}

func TestDynamoDeleteClassroomCleanupPhasesAreFenced(t *testing.T) {
	cfg := config.Defaults()
	cfg.FencingEnabled = true
	s := NewWithClient(newScriptedDynamo(), cfg)
	s.Now = fixedDynamo().Now
	fake := s.Client.(*scriptedDynamo)
	seedItem(fake, model.Classroom{PK: "CLASSROOM#fenced-delete", SK: "META", OwnerID: "owner", JoinCode: "FENCED1"})
	seedItem(fake, model.ClassroomMember{PK: "CLASSROOM#fenced-delete", SK: "MEMBER#student", ClassroomID: "fenced-delete", UserID: "student"})
	seedItem(fake, classroomIndexItem{PK: "CLASSROOM_MEMBER#student", SK: "CLASSROOM#fenced-delete", ClassroomID: "fenced-delete"})
	ctx := clusterstate.WithWriter(context.Background(), clusterstate.Writer{Backend: "A", Epoch: 44})
	if err := s.DeleteClassroom(ctx, "fenced-delete", "owner"); err != nil {
		t.Fatal(err)
	}
	if len(fake.transactions) < 4 {
		t.Fatalf("cleanup should fence tombstone, lookups, children, and final delete; transactions=%d", len(fake.transactions))
	}
	for index, tx := range fake.transactions {
		last := tx.TransactItems[len(tx.TransactItems)-1].ConditionCheck
		if last == nil || avString(last.Key, "pk") != clusterstate.ActivePK || last.ExpressionAttributeValues[":epoch"].(*types.AttributeValueMemberN).Value != "44" {
			t.Fatalf("cleanup transaction %d omitted the writer fence: %#v", index, tx.TransactItems)
		}
	}
}

func TestDynamoClassroomWritersConditionOnNotDeleting(t *testing.T) {
	ctx := context.Background()
	checkCondition := func(t *testing.T, condition *string, operation string) {
		t.Helper()
		if condition == nil || !strings.Contains(*condition, "attribute_not_exists(deleting)") {
			t.Fatalf("%s transaction is not fenced against classroom deletion: %v", operation, condition)
		}
	}

	t.Run("member join", func(t *testing.T) {
		s := fixedDynamo()
		fake := s.Client.(*scriptedDynamo)
		seedItem(fake, model.Classroom{PK: "CLASSROOM#race", SK: "META", OwnerID: "owner", JoinCode: "RACE123"})
		activeRead := scriptedAttributeMap(model.Classroom{PK: "CLASSROOM#race", SK: "META", OwnerID: "owner", JoinCode: "RACE123"})
		fake.items[itemKey(classroomMetaKey("race"))]["deleting"] = &types.AttributeValueMemberBOOL{Value: true}
		fake.getResponses = []map[string]types.AttributeValue{activeRead}
		fake.transactionErrors = []error{&types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}}
		if err := s.AddClassroomMember(ctx, &model.ClassroomMember{ClassroomID: "race", UserID: "student"}); !errors.Is(err, ErrClassroomMember) {
			t.Fatalf("concurrent join = %v", err)
		}
		if len(fake.transactions) != 1 {
			t.Fatalf("join transaction count = %d", len(fake.transactions))
		}
		checkCondition(t, fake.transactions[0].TransactItems[2].Update.ConditionExpression, "member join")
		if fake.items[itemKey(classroomMemberKey("race", "student"))] != nil {
			t.Fatal("concurrent join wrote a member after deletion started")
		}
	})

	t.Run("invitation create", func(t *testing.T) {
		s := fixedDynamo()
		fake := s.Client.(*scriptedDynamo)
		seedItem(fake, model.Classroom{PK: "CLASSROOM#race", SK: "META", OwnerID: "owner", JoinCode: "RACE123"})
		activeRead := scriptedAttributeMap(model.Classroom{PK: "CLASSROOM#race", SK: "META", OwnerID: "owner", JoinCode: "RACE123"})
		fake.items[itemKey(classroomMetaKey("race"))]["deleting"] = &types.AttributeValueMemberBOOL{Value: true}
		fake.getResponses = []map[string]types.AttributeValue{activeRead}
		fake.transactionErrors = []error{&types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}}
		invitation := &model.ClassroomInvitation{ClassroomID: "race", Email: "student@example.test", JoinCode: "INVITE1"}
		if err := s.CreateClassroomInvitation(ctx, "owner", invitation); !errors.Is(err, ErrClassroomCodeTaken) {
			t.Fatalf("concurrent invite create = %v", err)
		}
		checkCondition(t, fake.transactions[0].TransactItems[0].ConditionCheck.ConditionExpression, "invitation create")
		if fake.items[itemKey(classroomInvitationKey("race", invitation.ID))] != nil {
			t.Fatal("concurrent invite create wrote after deletion started")
		}
	})

	t.Run("invitation accept", func(t *testing.T) {
		s := fixedDynamo()
		fake := s.Client.(*scriptedDynamo)
		seedItem(fake, model.Classroom{PK: "CLASSROOM#race", SK: "META", OwnerID: "owner", JoinCode: "RACE123"})
		invitation := model.ClassroomInvitation{PK: "CLASSROOM#race", SK: "INVITATION#invite", ID: "invite", ClassroomID: "race", Email: "student@example.test", JoinCode: "INVITE1", Status: "pending"}
		seedItem(fake, invitation)
		activeRead := scriptedAttributeMap(model.Classroom{PK: "CLASSROOM#race", SK: "META", OwnerID: "owner", JoinCode: "RACE123"})
		fake.items[itemKey(classroomMetaKey("race"))]["deleting"] = &types.AttributeValueMemberBOOL{Value: true}
		fake.getResponses = []map[string]types.AttributeValue{scriptedAttributeMap(invitation), activeRead}
		fake.transactionErrors = []error{&types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}}
		if err := s.AcceptClassroomInvitation(ctx, "race", "invite", &model.ClassroomMember{UserID: "student", Email: "student@example.test"}); !errors.Is(err, ErrClassroomMember) {
			t.Fatalf("concurrent invite acceptance = %v", err)
		}
		checkCondition(t, fake.transactions[0].TransactItems[2].Update.ConditionExpression, "invitation acceptance")
		if fake.items[itemKey(classroomMemberKey("race", "student"))] != nil {
			t.Fatal("invite acceptance wrote membership after deletion started")
		}
	})

	t.Run("classroom update", func(t *testing.T) {
		s := fixedDynamo()
		fake := s.Client.(*scriptedDynamo)
		seedItem(fake, model.Classroom{PK: "CLASSROOM#race", SK: "META", OwnerID: "owner", JoinCode: "RACE123"})
		activeRead := scriptedAttributeMap(model.Classroom{PK: "CLASSROOM#race", SK: "META", OwnerID: "owner", JoinCode: "RACE123"})
		fake.items[itemKey(classroomMetaKey("race"))]["deleting"] = &types.AttributeValueMemberBOOL{Value: true}
		fake.getResponses = []map[string]types.AttributeValue{activeRead}
		fake.updateErrors = []error{&types.ConditionalCheckFailedException{}}
		name := "renamed"
		if _, err := s.UpdateClassroom(ctx, "race", "owner", model.ClassroomPatch{Name: &name}); !errors.Is(err, ErrClassroomForbidden) {
			t.Fatalf("concurrent class update = %v", err)
		}
		if len(fake.updates) != 1 {
			t.Fatalf("update request count = %d", len(fake.updates))
		}
		checkCondition(t, fake.updates[0].ConditionExpression, "classroom update")
	})
}
