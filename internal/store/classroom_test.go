package store

import (
	"context"
	"errors"
	"testing"

	"github.com/ayush/slate/internal/model"
)

func TestMemoryClassroomLifecycleAndRosterAuthorization(t *testing.T) {
	ctx := context.Background()
	memory := NewMemory("test")
	classroom := &model.Classroom{ID: "class-1", OwnerID: "teacher-1", Name: "Physics", JoinCode: "ab2cd3ef"}
	if err := memory.CreateClassroom(ctx, classroom); err != nil {
		t.Fatal(err)
	}
	if classroom.PK != "CLASSROOM#class-1" || classroom.SK != "META" || classroom.JoinCode != "AB2CD3EF" {
		t.Fatalf("classroom keys/code were not normalized: %#v", classroom)
	}

	rows, err := memory.ListClassroomsByOwner(ctx, "teacher-1", 10)
	if err != nil || len(rows) != 1 || rows[0].ID != "class-1" {
		t.Fatalf("owner listing = %#v, %v", rows, err)
	}
	if _, err := memory.FindClassroomByJoinCode(ctx, " ab2cd3ef "); err != nil {
		t.Fatal(err)
	}

	member := &model.ClassroomMember{ClassroomID: "class-1", UserID: "student-1", Name: "Ada", Email: "ada@example.com"}
	if err := memory.AddClassroomMember(ctx, member); err != nil {
		t.Fatal(err)
	}
	if err := memory.AddClassroomMember(ctx, member); !errors.Is(err, ErrClassroomMember) {
		t.Fatalf("duplicate join error = %v", err)
	}
	joined, err := memory.ListClassroomsForMember(ctx, "student-1", 10)
	if err != nil || len(joined) != 1 || joined[0].MemberCount != 1 {
		t.Fatalf("member listing = %#v, %v", joined, err)
	}
	roster, err := memory.ListClassroomMembers(ctx, "class-1")
	if err != nil || len(roster) != 1 || roster[0].UserID != "student-1" {
		t.Fatalf("roster = %#v, %v", roster, err)
	}

	newName := "Advanced Physics"
	if _, err := memory.UpdateClassroom(ctx, "class-1", "other-teacher", model.ClassroomPatch{Name: &newName}); !errors.Is(err, ErrClassroomForbidden) {
		t.Fatalf("wrong-owner update error = %v", err)
	}
	if err := memory.RemoveClassroomMember(ctx, "class-1", "other-teacher", "student-1"); !errors.Is(err, ErrClassroomForbidden) {
		t.Fatalf("wrong-owner removal error = %v", err)
	}
	if err := memory.RemoveClassroomMember(ctx, "class-1", "teacher-1", "student-1"); err != nil {
		t.Fatal(err)
	}
	if ok, err := memory.IsClassroomMember(ctx, "class-1", "student-1"); err != nil || ok {
		t.Fatalf("membership after removal = %v, %v", ok, err)
	}

	if err := memory.DeleteClassroom(ctx, "class-1", "other-teacher"); !errors.Is(err, ErrClassroomForbidden) {
		t.Fatalf("wrong-owner delete error = %v", err)
	}
	if err := memory.DeleteClassroom(ctx, "class-1", "teacher-1"); err != nil {
		t.Fatal(err)
	}
	if found, err := memory.GetClassroom(ctx, "class-1"); err != nil || found != nil {
		t.Fatalf("deleted classroom = %#v, %v", found, err)
	}
}
