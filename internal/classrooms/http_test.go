package classrooms

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ayush/slate/internal/auth"
	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/model"
	"github.com/ayush/slate/internal/store"
)

func TestClassroomHTTPHandlerRoutesAndErrors(t *testing.T) {
	teacher := &model.User{UserID: "teacher-1", Name: "Teacher", Email: "teacher@example.com", Role: auth.RoleTeacher}
	student := &model.User{UserID: "student-1", Name: "Student", Email: "student@example.com", Role: auth.RoleStudent}
	deleted := &model.User{UserID: "deleted", Name: "Deleted", Role: auth.RoleStudent}
	authStore := &httpAuthStore{users: map[string]*model.User{
		teacher.UserID: teacher, student.UserID: student,
	}}
	authService := auth.New(authStore, config.Defaults())
	teacherToken := mustAccessToken(t, authService, teacher)
	studentToken := mustAccessToken(t, authService, student)
	deletedToken := mustAccessToken(t, authService, deleted)
	classroom := classroomFixture()
	updated := *classroom
	updated.Name = "Updated"
	storeStub := &classroomStoreStub{
		classroom:     classroom,
		ownerRows:     []model.Classroom{*classroom},
		memberRows:    []model.Classroom{*classroom},
		rosterRows:    []model.ClassroomMember{{ClassroomID: classroom.ID, UserID: student.UserID}},
		findClassroom: classroom,
		updated:       &updated,
		member:        true,
	}
	service := classroomService(storeStub)
	active := true
	handler := NewHTTPHandler(service, authService, func(w http.ResponseWriter, _ *http.Request) bool {
		if !active {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		return active
	})

	var nilHandler *HTTPHandler
	if response := serve(nilHandler, http.MethodGet, "/classrooms", "", ""); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil handler status = %d", response.Code)
	}
	if response := serve(NewHTTPHandler(nil, authService, nil), http.MethodGet, "/classrooms", "", teacherToken); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil service status = %d", response.Code)
	}
	if response := serve(handler, http.MethodGet, "/not-classrooms", "", teacherToken); response.Code != http.StatusNotFound {
		t.Fatalf("unknown path status = %d", response.Code)
	}
	if response := serve(handler, http.MethodGet, "/classrooms/one/two/three/four", "", teacherToken); response.Code != http.StatusNotFound {
		t.Fatalf("unknown route status = %d", response.Code)
	}

	if response := serve(handler, http.MethodPut, "/classrooms", "{}", teacherToken); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("collection method status = %d", response.Code)
	}
	if response := serve(handler, http.MethodGet, "/classrooms", "", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated collection status = %d", response.Code)
	}
	if response := serve(handler, http.MethodGet, "/classrooms", "", "Bearer garbage"); response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid collection status = %d", response.Code)
	}
	authStore.users[deleted.UserID] = nil
	if response := serve(handler, http.MethodGet, "/classrooms", "", deletedToken); response.Code != http.StatusUnauthorized {
		t.Fatalf("deleted account status = %d", response.Code)
	}
	delete(authStore.users, deleted.UserID)
	authStore.getErr = errors.New("auth storage down")
	if response := serve(handler, http.MethodGet, "/classrooms", "", studentToken); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("auth data failure status = %d", response.Code)
	}
	authStore.getErr = nil
	if response := serve(NewHTTPHandler(service, nil, nil), http.MethodGet, "/classrooms", "", teacherToken); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil auth status = %d", response.Code)
	}
	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/classrooms/class-1"},
		{http.MethodPatch, "/classrooms/class-1"},
		{http.MethodDelete, "/classrooms/class-1"},
		{http.MethodPost, "/classrooms/join"},
		{http.MethodGet, "/classrooms/class-1/members"},
		{http.MethodDelete, "/classrooms/class-1/members/student-1"},
	} {
		if response := serve(handler, request.method, request.path, "{}", ""); response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s %s status = %d", request.method, request.path, response.Code)
		}
	}

	response := serve(handler, http.MethodGet, "/api/classrooms/?limit=7", "", teacherToken)
	if response.Code != http.StatusOK {
		t.Fatalf("teacher collection status = %d body=%s", response.Code, response.Body.String())
	}
	storeStub.listErr = store.ErrNotFound
	if response := serve(handler, http.MethodGet, "/classrooms", "", teacherToken); response.Code != http.StatusNotFound {
		t.Fatalf("collection store error status = %d", response.Code)
	}
	storeStub.listErr = nil
	storeStub.ownerRows = nil
	if response := serve(handler, http.MethodGet, "/classrooms", "", teacherToken); response.Code != http.StatusOK || response.Body.String() == "null\n" {
		t.Fatalf("nil collection rows = %d %q", response.Code, response.Body.String())
	}
	storeStub.ownerRows = []model.Classroom{*classroom}
	response = serve(handler, http.MethodGet, "/classrooms", "", studentToken)
	if response.Code != http.StatusOK {
		t.Fatalf("student collection status = %d", response.Code)
	}
	var studentRows []model.Classroom
	if err := json.Unmarshal(response.Body.Bytes(), &studentRows); err != nil || len(studentRows) != 1 || studentRows[0].JoinCode != "" {
		t.Fatalf("student collection body = %s, %v", response.Body.String(), err)
	}

	active = false
	if response := serve(handler, http.MethodPost, "/classrooms", `{"name":"Blocked"}`, teacherToken); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("inactive create status = %d", response.Code)
	}
	active = true
	if response := serve(handler, http.MethodPost, "/classrooms", "not-json", teacherToken); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid create body status = %d", response.Code)
	}
	if response := serve(handler, http.MethodPost, "/classrooms", `{"name":"a"}{"name":"b"}`, teacherToken); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("extra create body status = %d", response.Code)
	}
	response = serve(handler, http.MethodPost, "/classrooms", `{"name":"New","description":"Subject"}`, teacherToken)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", response.Code, response.Body.String())
	}
	storeStub.createErrs = []error{store.ErrClassroomCodeTaken, store.ErrClassroomCodeTaken, store.ErrClassroomCodeTaken, store.ErrClassroomCodeTaken, store.ErrClassroomCodeTaken}
	if response := serve(handler, http.MethodPost, "/classrooms", `{"name":"Retry"}`, teacherToken); response.Code != http.StatusConflict {
		t.Fatalf("create conflict status = %d", response.Code)
	}

	if response := serve(handler, http.MethodGet, "/classrooms/class-1", "", teacherToken); response.Code != http.StatusOK {
		t.Fatalf("resource GET status = %d", response.Code)
	}
	storeStub.getErr = store.ErrNotFound
	if response := serve(handler, http.MethodGet, "/classrooms/class-1", "", teacherToken); response.Code != http.StatusNotFound {
		t.Fatalf("resource GET error status = %d", response.Code)
	}
	storeStub.getErr = nil
	storeStub.classroom = nil
	if response := serve(handler, http.MethodGet, "/classrooms/class-1", "", teacherToken); response.Code != http.StatusNotFound {
		t.Fatalf("resource missing status = %d", response.Code)
	}
	storeStub.classroom = classroom

	active = false
	if response := serve(handler, http.MethodPatch, "/classrooms/class-1", `{"name":"x"}`, teacherToken); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("inactive patch status = %d", response.Code)
	}
	active = true
	if response := serve(handler, http.MethodPatch, "/classrooms/class-1", "bad", teacherToken); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid patch body status = %d", response.Code)
	}
	if response := serve(handler, http.MethodPut, "/classrooms/class-1", `{"name":"a"}{"name":"b"}`, teacherToken); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("extra patch body status = %d", response.Code)
	}
	response = serve(handler, http.MethodPut, "/classrooms/class-1", `{"name":"Updated"}`, teacherToken)
	if response.Code != http.StatusOK {
		t.Fatalf("resource PUT status = %d", response.Code)
	}
	storeStub.updateErr = store.ErrNotFound
	if response := serve(handler, http.MethodPatch, "/classrooms/class-1", `{"description":"x"}`, teacherToken); response.Code != http.StatusNotFound {
		t.Fatalf("resource update error status = %d", response.Code)
	}
	storeStub.updateErr = nil
	storeStub.updated = nil
	if response := serve(handler, http.MethodPatch, "/classrooms/class-1", `{"description":"x"}`, teacherToken); response.Code != http.StatusNotFound {
		t.Fatalf("resource update missing status = %d", response.Code)
	}
	storeStub.updated = &updated

	active = false
	if response := serve(handler, http.MethodDelete, "/classrooms/class-1", "", teacherToken); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("inactive delete status = %d", response.Code)
	}
	active = true
	storeStub.deleteErr = store.ErrNotFound
	if response := serve(handler, http.MethodDelete, "/classrooms/class-1", "", teacherToken); response.Code != http.StatusNotFound {
		t.Fatalf("delete error status = %d", response.Code)
	}
	storeStub.deleteErr = nil
	if response := serve(handler, http.MethodDelete, "/classrooms/class-1", "", teacherToken); response.Code != http.StatusNoContent {
		t.Fatalf("delete success status = %d", response.Code)
	}
	if response := serve(handler, http.MethodOptions, "/classrooms/class-1", "", teacherToken); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("resource method status = %d", response.Code)
	}

	if response := serve(handler, http.MethodGet, "/classrooms/join", "", studentToken); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("join method status = %d", response.Code)
	}
	active = false
	if response := serve(handler, http.MethodPost, "/classrooms/join", `{"code":"ABC234"}`, studentToken); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("inactive join status = %d", response.Code)
	}
	active = true
	if response := serve(handler, http.MethodPost, "/classrooms/join", "bad", studentToken); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid join body status = %d", response.Code)
	}
	if response := serve(handler, http.MethodPost, "/classrooms/join", `{"code":"ABC234"}{"code":"DEF567"}`, studentToken); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("extra join body status = %d", response.Code)
	}
	storeStub.findErr = store.ErrNotFound
	if response := serve(handler, http.MethodPost, "/classrooms/join", `{"join_code":"ABC234"}`, studentToken); response.Code != http.StatusNotFound {
		t.Fatalf("join lookup error status = %d", response.Code)
	}
	storeStub.findErr = nil
	storeStub.findClassroom = classroom
	storeStub.addErr = store.ErrClassroomMember
	if response := serve(handler, http.MethodPost, "/api/classrooms/class-1/join", `{"join_code":"ABC234"}`, studentToken); response.Code != http.StatusConflict {
		t.Fatalf("join member error status = %d", response.Code)
	}
	storeStub.addErr = nil
	response = serve(handler, http.MethodPost, "/classrooms/class-1/join", `{"code":"ABC234"}`, studentToken)
	if response.Code != http.StatusOK {
		t.Fatalf("join success status = %d", response.Code)
	}
	storeStub.findClassroom = nil
	if response := serve(handler, http.MethodPost, "/classrooms/join", `{"code":"ABC234"}`, studentToken); response.Code != http.StatusNotFound {
		t.Fatalf("join missing status = %d", response.Code)
	}
	storeStub.findClassroom = classroom

	if response := serve(handler, http.MethodPost, "/classrooms/class-1/roster", "", teacherToken); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("roster method status = %d", response.Code)
	}
	storeStub.rosterRows = nil
	if response := serve(handler, http.MethodGet, "/classrooms/class-1/members", "", teacherToken); response.Code != http.StatusOK || response.Body.String() == "null\n" {
		t.Fatalf("nil roster = %d %q", response.Code, response.Body.String())
	}
	storeStub.rosterRows = []model.ClassroomMember{{ClassroomID: classroom.ID, UserID: student.UserID}}
	storeStub.listErr = store.ErrNotFound
	if response := serve(handler, http.MethodGet, "/classrooms/class-1/roster", "", teacherToken); response.Code != http.StatusNotFound {
		t.Fatalf("roster error status = %d", response.Code)
	}
	storeStub.listErr = nil

	if response := serve(handler, http.MethodGet, "/classrooms/class-1/members/student-1", "", teacherToken); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("member method status = %d", response.Code)
	}
	active = false
	if response := serve(handler, http.MethodDelete, "/classrooms/class-1/members/student-1", "", teacherToken); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("inactive member delete status = %d", response.Code)
	}
	active = true
	storeStub.removeErr = store.ErrClassroomMemberNotFound
	if response := serve(handler, http.MethodDelete, "/classrooms/class-1/members/student-1", "", teacherToken); response.Code != http.StatusNotFound {
		t.Fatalf("member delete error status = %d", response.Code)
	}
	storeStub.removeErr = nil
	if response := serve(handler, http.MethodDelete, "/classrooms/class-1/members/student-1", "", teacherToken); response.Code != http.StatusNoContent {
		t.Fatalf("member delete success status = %d", response.Code)
	}
}

func TestClassroomHTTPHelpersAndErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		path string
		ok   bool
		want []string
	}{
		{"/classrooms", true, nil},
		{"/classrooms/", true, nil},
		{"/classrooms//", true, nil},
		{"/api/classrooms", true, nil},
		{"/api/classrooms/class-1", true, []string{"class-1"}},
		{"/classrooms/class-1/members/user-1", true, []string{"class-1", "members", "user-1"}},
		{"/classrooms//members", false, nil},
		{"/other", false, nil},
	} {
		got, ok := classroomPath(tc.path)
		if ok != tc.ok || !sameStrings(got, tc.want) {
			t.Fatalf("classroomPath(%q) = %#v,%v", tc.path, got, ok)
		}
	}
	if got, ok := classroomPath(""); ok || got != nil {
		t.Fatalf("empty classroom path = %#v,%v", got, ok)
	}
	if parseLimitRequest("0") != 50 || parseLimitRequest("-1") != 50 || parseLimitRequest("101") != 100 || parseLimitRequest("7") != 7 || parseLimitRequest("bad") != 50 {
		t.Fatal("parseLimit edges failed")
	}

	validRecorder := httptest.NewRecorder()
	validRequest := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"ok"}`))
	var payload struct {
		Name string `json:"name"`
	}
	if !decode(validRecorder, validRequest, &payload) || payload.Name != "ok" {
		t.Fatal("valid body was rejected")
	}
	for _, body := range []string{"not-json", `{"name":"one"}{"name":"two"}`} {
		recorder := httptest.NewRecorder()
		if decode(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), &payload) {
			t.Fatalf("invalid body accepted: %q", body)
		}
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid body status = %d", recorder.Code)
		}
	}
	if _, err := io.ReadAll(strings.NewReader("body")); err != nil {
		t.Fatal(err)
	}

	service := classroomService(&classroomStoreStub{})
	handler := NewHTTPHandler(service, nil, nil)
	for _, tc := range []struct {
		err    error
		status int
	}{
		{auth.ErrAuthenticationRequired, http.StatusUnauthorized},
		{ErrTeacherRequired, http.StatusForbidden},
		{ErrStudentRequired, http.StatusForbidden},
		{ErrClassroomForbidden, http.StatusForbidden},
		{ErrClassroomNotFound, http.StatusNotFound},
		{ErrMemberNotFound, http.StatusNotFound},
		{ErrAlreadyMember, http.StatusConflict},
		{ErrJoinCodeTaken, http.StatusConflict},
		{ErrInvalidJoinCode, http.StatusUnprocessableEntity},
		{ErrInvalidInput, http.StatusUnprocessableEntity},
		{ErrStoreUnavailable, http.StatusServiceUnavailable},
		{errors.New("unknown"), http.StatusServiceUnavailable},
	} {
		recorder := httptest.NewRecorder()
		handler.writeError(recorder, tc.err)
		if recorder.Code != tc.status {
			t.Fatalf("writeError(%v) = %d, want %d", tc.err, recorder.Code, tc.status)
		}
	}
	if handler.active(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil)) != true {
		t.Fatal("nil active checker did not allow")
	}
	active := NewHTTPHandler(service, nil, func(http.ResponseWriter, *http.Request) bool { return false })
	if active.active(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil)) {
		t.Fatal("active checker ignored false result")
	}

	recorder := httptest.NewRecorder()
	methodNotAllowed(recorder, http.MethodGet, http.MethodPost)
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != "GET, POST" {
		t.Fatalf("methodNotAllowed = %d %v", recorder.Code, recorder.Header())
	}
	recorder = httptest.NewRecorder()
	writeJSON(recorder, http.StatusAccepted, map[string]string{"ok": "yes"})
	if recorder.Code != http.StatusAccepted || recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("writeJSON = %d %v", recorder.Code, recorder.Header())
	}
	recorder = httptest.NewRecorder()
	detail(recorder, http.StatusBadRequest, "bad")
	if recorder.Code != http.StatusBadRequest {
		t.Fatal("detail status failed")
	}
	recorder = httptest.NewRecorder()
	validation(recorder, "invalid")
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatal("validation status failed")
	}
}

type httpAuthStore struct {
	users  map[string]*model.User
	getErr error
}

func (s *httpAuthStore) GetUser(_ context.Context, id string) (*model.User, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.users[id], nil
}
func (s *httpAuthStore) FindUserIDByEmail(context.Context, string) (string, error) { return "", nil }
func (s *httpAuthStore) CreateUser(context.Context, *model.User, string) error     { return nil }

func mustAccessToken(t *testing.T, service *auth.Service, user *model.User) string {
	t.Helper()
	token, err := service.EncodeToken(user, "access", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func serve(handler http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func parseLimitRequest(raw string) int {
	request := httptest.NewRequest(http.MethodGet, "/?limit="+raw, nil)
	return parseLimit(request)
}
