package classrooms

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ayush/slate/internal/auth"
	"github.com/ayush/slate/internal/model"
	"github.com/ayush/slate/internal/store"
)

func TestServiceEnforcesRolesAndRedactsJoinCodes(t *testing.T) {
	service := New(store.NewMemory("test"))
	service.Now = func() time.Time { return time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC) }
	teacher := &model.User{UserID: "teacher-1", Name: "Teacher", Role: auth.RoleTeacher}
	student := &model.User{UserID: "student-1", Name: "Student", Email: "student@example.com", Role: auth.RoleStudent}
	otherStudent := &model.User{UserID: "student-2", Name: "Other", Role: auth.RoleStudent}

	if _, err := service.Create(context.Background(), student, CreateInput{Name: "Physics"}); !errors.Is(err, ErrTeacherRequired) {
		t.Fatalf("student create error = %v", err)
	}
	created, err := service.Create(context.Background(), teacher, CreateInput{Name: "Physics"})
	if err != nil {
		t.Fatal(err)
	}
	if created.JoinCode == "" || created.OwnerID != teacher.UserID {
		t.Fatalf("created classroom = %#v", created)
	}
	if _, err := service.Join(context.Background(), teacher, created.JoinCode, ""); !errors.Is(err, ErrStudentRequired) {
		t.Fatalf("teacher join error = %v", err)
	}

	joined, err := service.Join(context.Background(), student, created.JoinCode, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if joined.JoinCode != "" {
		t.Fatalf("student join leaked code: %#v", joined)
	}
	studentView, err := service.GetForUser(context.Background(), student, created.ID)
	if err != nil || studentView.JoinCode != "" {
		t.Fatalf("student view = %#v, %v", studentView, err)
	}
	teacherView, err := service.GetForUser(context.Background(), teacher, created.ID)
	if err != nil || teacherView.JoinCode == "" {
		t.Fatalf("teacher view = %#v, %v", teacherView, err)
	}
	if _, err := service.GetForUser(context.Background(), otherStudent, created.ID); !errors.Is(err, ErrClassroomForbidden) {
		t.Fatalf("outsider view error = %v", err)
	}
}
