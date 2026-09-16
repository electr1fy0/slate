package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ayush/slate/internal/config"
)

func registerTeacher(t *testing.T, handler http.Handler, email string) map[string]string {
	t.Helper()
	admin := map[string]string{"x-admin-token": config.Defaults().AdminToken}
	created := call(t, handler, http.MethodPost, "/admin/users", map[string]any{
		"email": email, "password": "teacher-password", "name": "Teacher", "role": "teacher",
	}, admin)
	if created.Code != http.StatusCreated {
		t.Fatalf("create teacher: %d %s", created.Code, created.Body.String())
	}
	login := call(t, handler, http.MethodPost, "/auth/login", map[string]any{
		"email": email, "password": "teacher-password",
	}, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login teacher: %d %s", login.Code, login.Body.String())
	}
	body := decodeBody(t, login)
	return map[string]string{"Authorization": "Bearer " + body["access_token"].(string)}
}

func TestClassroomRESTLifecycleAndPermissions(t *testing.T) {
	_, handler := newTestServer(t, "active")
	teacher := registerTeacher(t, handler, "teacher@classroom.example")
	otherTeacher := registerTeacher(t, handler, "other@classroom.example")
	student, _ := registerStudent(t, handler, "student@classroom.example")

	studentCreate := call(t, handler, http.MethodPost, "/api/classrooms", map[string]any{"name": "Physics"}, student)
	if studentCreate.Code != http.StatusForbidden {
		t.Fatalf("student create: %d %s", studentCreate.Code, studentCreate.Body.String())
	}

	created := call(t, handler, http.MethodPost, "/api/classrooms", map[string]any{
		"name": "Physics", "description": "Wednesday section",
	}, teacher)
	if created.Code != http.StatusCreated {
		t.Fatalf("teacher create: %d %s", created.Code, created.Body.String())
	}
	createdBody := decodeBody(t, created)
	classroomID := createdBody["id"].(string)
	joinCode := createdBody["join_code"].(string)
	if joinCode == "" || createdBody["member_count"] != float64(0) {
		t.Fatalf("create response = %#v", createdBody)
	}

	teacherList := call(t, handler, http.MethodGet, "/classrooms", nil, teacher)
	if teacherList.Code != http.StatusOK || len(decodeArray(t, teacherList)) != 1 {
		t.Fatalf("teacher list: %d %s", teacherList.Code, teacherList.Body.String())
	}
	studentList := call(t, handler, http.MethodGet, "/classrooms", nil, student)
	if studentList.Code != http.StatusOK || len(decodeArray(t, studentList)) != 0 {
		t.Fatalf("student pre-join list: %d %s", studentList.Code, studentList.Body.String())
	}

	joined := call(t, handler, http.MethodPost, "/api/classrooms/join", map[string]any{"join_code": joinCode}, student)
	if joined.Code != http.StatusOK || joinedBodyHasCode(t, joined) {
		t.Fatalf("join: %d %s", joined.Code, joined.Body.String())
	}
	joinedAgain := call(t, handler, http.MethodPost, "/api/classrooms/join", map[string]any{"join_code": joinCode}, student)
	if joinedAgain.Code != http.StatusConflict {
		t.Fatalf("duplicate join: %d %s", joinedAgain.Code, joinedAgain.Body.String())
	}

	studentList = call(t, handler, http.MethodGet, "/classrooms", nil, student)
	if studentList.Code != http.StatusOK || len(decodeArray(t, studentList)) != 1 {
		t.Fatalf("student post-join list: %d %s", studentList.Code, studentList.Body.String())
	}
	studentDetail := call(t, handler, http.MethodGet, "/api/classrooms/"+classroomID, nil, student)
	if studentDetail.Code != http.StatusOK || joinedBodyHasCode(t, studentDetail) {
		t.Fatalf("student detail: %d %s", studentDetail.Code, studentDetail.Body.String())
	}
	studentRoster := call(t, handler, http.MethodGet, "/api/classrooms/"+classroomID+"/members", nil, student)
	if studentRoster.Code != http.StatusForbidden {
		t.Fatalf("student roster: %d %s", studentRoster.Code, studentRoster.Body.String())
	}

	roster := call(t, handler, http.MethodGet, "/api/classrooms/"+classroomID+"/roster", nil, teacher)
	if roster.Code != http.StatusOK || len(decodeArray(t, roster)) != 1 {
		t.Fatalf("teacher roster: %d %s", roster.Code, roster.Body.String())
	}
	updated := call(t, handler, http.MethodPatch, "/api/classrooms/"+classroomID, map[string]any{"name": "Advanced Physics"}, otherTeacher)
	if updated.Code != http.StatusForbidden {
		t.Fatalf("other teacher update: %d %s", updated.Code, updated.Body.String())
	}
	updated = call(t, handler, http.MethodPatch, "/api/classrooms/"+classroomID, map[string]any{"name": "Advanced Physics"}, teacher)
	if updated.Code != http.StatusOK || decodeBody(t, updated)["name"] != "Advanced Physics" {
		t.Fatalf("owner update: %d %s", updated.Code, updated.Body.String())
	}

	memberID := decodeObjectArray(t, roster)[0]["user_id"].(string)
	removed := call(t, handler, http.MethodDelete, "/api/classrooms/"+classroomID+"/members/"+memberID, nil, teacher)
	if removed.Code != http.StatusNoContent {
		t.Fatalf("remove member: %d %s", removed.Code, removed.Body.String())
	}
	studentList = call(t, handler, http.MethodGet, "/classrooms", nil, student)
	if studentList.Code != http.StatusOK || len(decodeArray(t, studentList)) != 0 {
		t.Fatalf("student list after removal: %d %s", studentList.Code, studentList.Body.String())
	}

	deletedByOther := call(t, handler, http.MethodDelete, "/api/classrooms/"+classroomID, nil, otherTeacher)
	if deletedByOther.Code != http.StatusForbidden {
		t.Fatalf("other teacher delete: %d %s", deletedByOther.Code, deletedByOther.Body.String())
	}
	deleted := call(t, handler, http.MethodDelete, "/api/classrooms/"+classroomID, nil, teacher)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("owner delete: %d %s", deleted.Code, deleted.Body.String())
	}
	missing := call(t, handler, http.MethodGet, "/api/classrooms/"+classroomID, nil, teacher)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("deleted detail: %d %s", missing.Code, missing.Body.String())
	}
}

func decodeArray(t *testing.T, response *httptest.ResponseRecorder) []any {
	t.Helper()
	var rows []any
	if err := json.Unmarshal(response.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode array %q: %v", response.Body.String(), err)
	}
	return rows
}

func decodeObjectArray(t *testing.T, response *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode object array %q: %v", response.Body.String(), err)
	}
	return rows
}

func joinedBodyHasCode(t *testing.T, response *httptest.ResponseRecorder) bool {
	t.Helper()
	body := decodeBody(t, response)
	_, ok := body["join_code"]
	return ok
}
