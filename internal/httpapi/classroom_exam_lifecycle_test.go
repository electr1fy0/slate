package httpapi

import (
	"net/http"
	"testing"
)

func TestClassroomDeletionRetainsTeacherManagementAndRevokesLearnerAccess(t *testing.T) {
	_, handler := newTestServer(t, "active")
	teacher := registerTeacher(t, handler, "lifecycle-teacher@example.com")
	otherTeacher := registerTeacher(t, handler, "lifecycle-other@example.com")
	student, _ := registerStudent(t, handler, "lifecycle-student@example.com")
	createdClass := call(t, handler, http.MethodPost, "/classrooms", map[string]any{"name": "Lifecycle"}, teacher)
	if createdClass.Code != http.StatusCreated {
		t.Fatalf("create class: %d %s", createdClass.Code, createdClass.Body.String())
	}
	classroom := decodeBody(t, createdClass)
	classroomID := classroom["id"].(string)
	join := func() {
		t.Helper()
		if response := call(t, handler, http.MethodPost, "/classrooms/join", map[string]any{"join_code": classroom["join_code"]}, student); response.Code != http.StatusOK {
			t.Fatalf("join: %d %s", response.Code, response.Body.String())
		}
	}
	join()
	createdExam := call(t, handler, http.MethodPost, "/exam", map[string]any{
		"id": "classroom-lifecycle", "title": "Lifecycle assessment", "classroom_id": classroomID,
		"questions": []map[string]any{{"question": "Choose one", "options": []string{"A", "B"}, "answer": 0}},
	}, teacher)
	if createdExam.Code != http.StatusCreated {
		t.Fatalf("create exam: %d %s", createdExam.Code, createdExam.Body.String())
	}
	if response := call(t, handler, http.MethodPost, "/classrooms/"+classroomID+"/leave", nil, student); response.Code != http.StatusNoContent {
		t.Fatalf("leave: %d %s", response.Code, response.Body.String())
	}
	if response := call(t, handler, http.MethodGet, "/exam/classroom-lifecycle/info", nil, student); response.Code != http.StatusForbidden {
		t.Fatalf("former member access: %d %s", response.Code, response.Body.String())
	}
	join()
	if response := call(t, handler, http.MethodDelete, "/classrooms/"+classroomID, nil, teacher); response.Code != http.StatusNoContent {
		t.Fatalf("delete class: %d %s", response.Code, response.Body.String())
	}
	for _, route := range []string{"/exam/classroom-lifecycle/info", "/exam/classroom-lifecycle/preview", "/admin/exams/classroom-lifecycle/results"} {
		if response := call(t, handler, http.MethodGet, route, nil, teacher); response.Code != http.StatusOK {
			t.Fatalf("owner GET %s after class deletion: %d %s", route, response.Code, response.Body.String())
		}
		for _, token := range []map[string]string{student, otherTeacher} {
			if response := call(t, handler, http.MethodGet, route, nil, token); response.Code != http.StatusForbidden {
				t.Fatalf("non-owner GET %s after class deletion: %d %s", route, response.Code, response.Body.String())
			}
		}
	}
	if response := call(t, handler, http.MethodPatch, "/exam/classroom-lifecycle", map[string]any{"title": "Archived classroom assessment", "classroom_id": ""}, teacher); response.Code != http.StatusOK {
		t.Fatalf("owner reassign: %d %s", response.Code, response.Body.String())
	}
	if response := call(t, handler, http.MethodPost, "/exam/classroom-lifecycle/close", nil, teacher); response.Code != http.StatusOK {
		t.Fatalf("owner close: %d %s", response.Code, response.Body.String())
	}
	if response := call(t, handler, http.MethodDelete, "/exam/classroom-lifecycle", nil, teacher); response.Code != http.StatusNoContent {
		t.Fatalf("owner delete: %d %s", response.Code, response.Body.String())
	}
}
