package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ayush/slate/internal/store"
)

type passwordResetHTTPStub struct {
	links []string
	err   error
}

func (m *passwordResetHTTPStub) SendPasswordReset(_ context.Context, _, _, link string) error {
	m.links = append(m.links, link)
	return m.err
}

type classroomInvitationHTTPStub struct {
	links []string
	err   error
}

func (m *classroomInvitationHTTPStub) SendClassroomInvitation(_ context.Context, _, _, _, link string) error {
	m.links = append(m.links, link)
	return m.err
}

func TestExamInfoDoesNotStartAttemptAndAssignedRoutesRequireMembership(t *testing.T) {
	server, handler := newTestServer(t, "active")
	teacher := registerTeacher(t, handler, "qol-teacher@example.com")
	student, studentBody := registerStudent(t, handler, "qol-student@example.com")
	studentID := studentBody["user"].(map[string]any)["user_id"].(string)
	stranger, _ := registerStudent(t, handler, "qol-stranger@example.com")
	otherTeacher := registerTeacher(t, handler, "qol-other-teacher@example.com")

	classroomResponse := call(t, handler, http.MethodPost, "/classrooms", map[string]any{"name": "Physics", "client_id": "qol-exam-class"}, teacher)
	if classroomResponse.Code != http.StatusCreated {
		t.Fatalf("create classroom: %d %s", classroomResponse.Code, classroomResponse.Body.String())
	}
	classroom := decodeBody(t, classroomResponse)
	classroomID := classroom["id"].(string)
	joined := call(t, handler, http.MethodPost, "/classrooms/join", map[string]any{"join_code": classroom["join_code"]}, student)
	if joined.Code != http.StatusOK {
		t.Fatalf("join classroom: %d %s", joined.Code, joined.Body.String())
	}

	created := call(t, handler, http.MethodPost, "/exam", map[string]any{
		"id": "qol-assigned", "title": "Assigned physics", "classroom_id": classroomID, "instructions": "Show your work.",
		"questions": []map[string]any{{"question": "Two plus two?", "options": []string{"3", "4"}, "answer": 1, "points": 4, "feedback": "Add two and two."}},
	}, teacher)
	if created.Code != http.StatusCreated {
		t.Fatalf("create assigned exam: %d %s", created.Code, created.Body.String())
	}

	info := call(t, handler, http.MethodGet, "/exam/qol-assigned/info", nil, student)
	infoBody := decodeBody(t, info)
	if info.Code != http.StatusOK || infoBody["attempt_status"] != "not_started" || infoBody["instructions"] != "Show your work." {
		t.Fatalf("safe initial exam info: %d %#v", info.Code, infoBody)
	}
	if _, exposed := infoBody["questions"]; exposed {
		t.Fatalf("exam info exposed question answers: %#v", infoBody)
	}
	backend := server.Store.(*store.MemoryStore)
	if attempt, err := backend.GetStudentAttempt(context.Background(), "qol-assigned", studentID); err != nil || attempt != nil {
		t.Fatalf("info read started attempt: %#v, %v", attempt, err)
	}
	for _, path := range []string{
		"/exam/qol-assigned/info", "/exam/qol-assigned", "/exam/qol-assigned/attempt", "/exam/qol-assigned/result",
	} {
		if response := call(t, handler, http.MethodGet, path, nil, stranger); response.Code != http.StatusForbidden {
			t.Fatalf("nonmember GET %s = %d %s", path, response.Code, response.Body.String())
		}
		if response := call(t, handler, http.MethodGet, path, nil, otherTeacher); response.Code != http.StatusForbidden {
			t.Fatalf("foreign teacher GET %s = %d %s", path, response.Code, response.Body.String())
		}
	}
	if response := call(t, handler, http.MethodPost, "/exam/qol-assigned/submit", map[string]any{"answers": map[string]int{"0": 1}, "request_id": "intruder-submit"}, stranger); response.Code != http.StatusForbidden {
		t.Fatalf("nonmember submission = %d %s", response.Code, response.Body.String())
	}
	if response := call(t, handler, http.MethodPut, "/exam/qol-assigned/attempt", map[string]any{"answers": map[string]int{"0": 1}, "expected_version": 0, "request_id": "intruder-save"}, stranger); response.Code != http.StatusForbidden {
		t.Fatalf("nonmember autosave = %d %s", response.Code, response.Body.String())
	}

	started := call(t, handler, http.MethodGet, "/exam/qol-assigned/attempt", nil, student)
	if started.Code != http.StatusOK {
		t.Fatalf("start attempt: %d %s", started.Code, started.Body.String())
	}
	info = call(t, handler, http.MethodGet, "/exam/qol-assigned/info", nil, student)
	if body := decodeBody(t, info); info.Code != http.StatusOK || body["attempt_status"] != "in_progress" || body["attempt_number"] != float64(1) {
		t.Fatalf("in-progress info: %d %#v", info.Code, body)
	}
	submitted := call(t, handler, http.MethodPost, "/exam/qol-assigned/submit", map[string]any{"answers": map[string]int{"0": 1}, "request_id": "student-submit"}, student)
	if submitted.Code != http.StatusOK {
		t.Fatalf("submit assigned exam: %d %s", submitted.Code, submitted.Body.String())
	}
	result := call(t, handler, http.MethodGet, "/exam/qol-assigned/result", nil, student)
	resultBody := decodeBody(t, result)
	if result.Code != http.StatusOK || resultBody["score"] != float64(4) || resultBody["total"] != float64(4) {
		t.Fatalf("student result: %d %#v", result.Code, resultBody)
	}
	review := resultBody["review"].([]any)[0].(map[string]any)
	if review["feedback"] != "Add two and two." || review["points"] != float64(4) {
		t.Fatalf("authored result review missing points/feedback: %#v", review)
	}
	examsResponse := call(t, handler, http.MethodGet, "/exams", nil, student)
	var examsList []map[string]any
	if err := json.Unmarshal(examsResponse.Body.Bytes(), &examsList); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range examsList {
		if row["id"] == "qol-assigned" {
			found = true
			if row["attempt_status"] != "submitted" || row["score"] != float64(4) || row["submitted_at"] == "" {
				t.Fatalf("submitted exam history fields: %#v", row)
			}
		}
	}
	if examsResponse.Code != http.StatusOK || !found {
		t.Fatalf("submitted class exam missing from history: %d %s", examsResponse.Code, examsResponse.Body.String())
	}
}

func TestExamRetakesAreIdempotentAttemptScopedAndLimited(t *testing.T) {
	_, handler := newTestServer(t, "active")
	teacher := registerTeacher(t, handler, "retake-teacher@example.com")
	student, _ := registerStudent(t, handler, "retake-student@example.com")
	classroomResponse := call(t, handler, http.MethodPost, "/classrooms", map[string]any{"name": "Retake class", "client_id": "retake-class"}, teacher)
	if classroomResponse.Code != http.StatusCreated {
		t.Fatalf("create class: %d %s", classroomResponse.Code, classroomResponse.Body.String())
	}
	classroom := decodeBody(t, classroomResponse)
	joined := call(t, handler, http.MethodPost, "/classrooms/join", map[string]any{"join_code": classroom["join_code"]}, student)
	if joined.Code != http.StatusOK {
		t.Fatalf("join class: %d %s", joined.Code, joined.Body.String())
	}
	examResponse := call(t, handler, http.MethodPost, "/exam", map[string]any{
		"id": "retake-quiz", "title": "Retake quiz", "classroom_id": classroom["id"], "max_attempts": 2,
		"questions": []map[string]any{{"question": "Pick the right answer", "options": []string{"wrong", "right"}, "answer": 1}},
	}, teacher)
	if examResponse.Code != http.StatusCreated {
		t.Fatalf("create quiz: %d %s", examResponse.Code, examResponse.Body.String())
	}
	if started := call(t, handler, http.MethodGet, "/exam/retake-quiz/attempt", nil, student); started.Code != http.StatusOK {
		t.Fatalf("start attempt 1: %d %s", started.Code, started.Body.String())
	}
	firstSubmit := call(t, handler, http.MethodPost, "/exam/retake-quiz/submit", map[string]any{
		"answers": map[string]int{"0": 1}, "request_id": "retake-submit-1", "attempt_number": 1,
	}, student)
	if firstSubmit.Code != http.StatusOK || decodeBody(t, firstSubmit)["attempt_number"] != float64(1) {
		t.Fatalf("submit attempt 1: %d %s", firstSubmit.Code, firstSubmit.Body.String())
	}
	retake := call(t, handler, http.MethodPost, "/exam/retake-quiz/retake", map[string]any{"expected_attempt_number": 1}, student)
	retakeBody := decodeBody(t, retake)
	if retake.Code != http.StatusOK || retakeBody["attempt_number"] != float64(2) {
		t.Fatalf("retake response: %d %#v", retake.Code, retakeBody)
	}
	retry := call(t, handler, http.MethodPost, "/exam/retake-quiz/retake", map[string]any{"expected_attempt_number": 1}, student)
	if body := decodeBody(t, retry); retry.Code != http.StatusOK || body["attempt_number"] != float64(2) || body["started_at"] != retakeBody["started_at"] {
		t.Fatalf("idempotent retake response: %d %#v (initial %#v)", retry.Code, body, retakeBody)
	}
	if staleSave := call(t, handler, http.MethodPut, "/exam/retake-quiz/attempt", map[string]any{
		"answers": map[string]int{"0": 0}, "expected_version": 0, "request_id": "stale-save", "attempt_number": 1,
	}, student); staleSave.Code != http.StatusConflict {
		t.Fatalf("stale attempt save: %d %s", staleSave.Code, staleSave.Body.String())
	}
	if staleSubmit := call(t, handler, http.MethodPost, "/exam/retake-quiz/submit", map[string]any{
		"answers": map[string]int{"0": 0}, "request_id": "stale-submit", "attempt_number": 1,
	}, student); staleSubmit.Code != http.StatusConflict {
		t.Fatalf("stale attempt submit: %d %s", staleSubmit.Code, staleSubmit.Body.String())
	}
	oldReceipt := call(t, handler, http.MethodPost, "/exam/retake-quiz/submit", map[string]any{
		"answers": map[string]int{"0": 1}, "request_id": "retake-submit-1", "attempt_number": 1,
	}, student)
	if body := decodeBody(t, oldReceipt); oldReceipt.Code != http.StatusOK || body["status"] != "duplicate" || body["attempt_number"] != float64(1) {
		t.Fatalf("old receipt retry after retake: %d %#v", oldReceipt.Code, body)
	}
	secondSubmit := call(t, handler, http.MethodPost, "/exam/retake-quiz/submit", map[string]any{
		"answers": map[string]int{"0": 0}, "request_id": "retake-submit-2", "attempt_number": 2,
	}, student)
	if body := decodeBody(t, secondSubmit); secondSubmit.Code != http.StatusOK || body["attempt_number"] != float64(2) {
		t.Fatalf("submit attempt 2: %d %#v", secondSubmit.Code, body)
	}
	result := call(t, handler, http.MethodGet, "/exam/retake-quiz/result", nil, student)
	if body := decodeBody(t, result); result.Code != http.StatusOK || body["attempt_number"] != float64(2) {
		t.Fatalf("latest result after retake: %d %#v", result.Code, body)
	}
	tooMany := call(t, handler, http.MethodPost, "/exam/retake-quiz/retake", map[string]any{"expected_attempt_number": 2}, student)
	if tooMany.Code != http.StatusConflict {
		t.Fatalf("max attempts ignored: %d %s", tooMany.Code, tooMany.Body.String())
	}
}

func TestZeroMaxAttemptsSurvivesExamAndDraftHTTPRoundTrip(t *testing.T) {
	_, handler := newTestServer(t, "active")
	teacher := registerTeacher(t, handler, "unlimited-teacher@example.com")
	question := []map[string]any{{"question": "q", "options": []string{"a", "b"}, "answer": 0}}
	exam := call(t, handler, http.MethodPost, "/exam", map[string]any{"id": "unlimited-exam", "title": "Unlimited", "max_attempts": 0, "questions": question}, teacher)
	if body := decodeBody(t, exam); exam.Code != http.StatusCreated || body["max_attempts"] != float64(0) {
		t.Fatalf("create unlimited exam: %d %#v", exam.Code, body)
	}
	var examsList []map[string]any
	list := call(t, handler, http.MethodGet, "/exams", nil, teacher)
	if err := json.Unmarshal(list.Body.Bytes(), &examsList); err != nil {
		t.Fatal(err)
	}
	foundExam := false
	for _, row := range examsList {
		if row["id"] == "unlimited-exam" {
			foundExam = true
			if row["max_attempts"] != float64(0) {
				t.Fatalf("exam list dropped unlimited value: %#v", row)
			}
		}
	}
	if list.Code != http.StatusOK || !foundExam {
		t.Fatalf("unlimited exam missing from list: %d %s", list.Code, list.Body.String())
	}
	draft := call(t, handler, http.MethodPost, "/quiz/drafts", map[string]any{"id": "unlimited-draft", "title": "Unlimited draft", "max_attempts": 0, "questions": question}, teacher)
	if body := decodeBody(t, draft); draft.Code != http.StatusCreated || body["max_attempts"] != float64(0) {
		t.Fatalf("create unlimited draft: %d %#v", draft.Code, body)
	}
	readDraft := call(t, handler, http.MethodGet, "/quiz/drafts/unlimited-draft", nil, teacher)
	if body := decodeBody(t, readDraft); readDraft.Code != http.StatusOK || body["max_attempts"] != float64(0) {
		t.Fatalf("read unlimited draft: %d %#v", readDraft.Code, body)
	}
	defaultExam := call(t, handler, http.MethodPost, "/exam", map[string]any{"id": "default-attempts-exam", "title": "Default", "questions": question}, teacher)
	if body := decodeBody(t, defaultExam); defaultExam.Code != http.StatusCreated || body["max_attempts"] != float64(1) {
		t.Fatalf("omitted exam attempt limit default: %d %#v", defaultExam.Code, body)
	}
	defaultDraft := call(t, handler, http.MethodPost, "/quiz/drafts", map[string]any{"id": "default-attempts-draft", "title": "Default draft", "questions": question}, teacher)
	if body := decodeBody(t, defaultDraft); defaultDraft.Code != http.StatusCreated || body["max_attempts"] != float64(1) {
		t.Fatalf("omitted draft attempt limit default: %d %#v", defaultDraft.Code, body)
	}
}

func TestClassroomInvitationsAndCreateRetryAreDurableAndHonest(t *testing.T) {
	server, _ := newTestServer(t, "active")
	mailer := &classroomInvitationHTTPStub{}
	server.InvitationSender = mailer
	server.Settings.PublicBaseURL = "https://slate.example.test"
	handler := server.Handler()
	teacher := registerTeacher(t, handler, "invite-owner@example.com")
	student, _ := registerStudent(t, handler, "invite-student@example.com")
	otherTeacher := registerTeacher(t, handler, "invite-other@example.com")

	create := func(name string) *httptest.ResponseRecorder {
		return call(t, handler, http.MethodPost, "/classrooms", map[string]any{"name": name, "client_id": "stable-class-id"}, teacher)
	}
	created := create("Chemistry")
	if created.Code != http.StatusCreated {
		t.Fatalf("create classroom: %d %s", created.Code, created.Body.String())
	}
	classroom := decodeBody(t, created)
	retry := create("Chemistry")
	if retry.Code != http.StatusCreated || decodeBody(t, retry)["id"] != classroom["id"] {
		t.Fatalf("idempotent retry: %d %s", retry.Code, retry.Body.String())
	}
	if conflict := call(t, handler, http.MethodPost, "/classrooms", map[string]any{"name": "Different", "client_id": "stable-class-id"}, teacher); conflict.Code != http.StatusConflict {
		t.Fatalf("idempotency conflict: %d %s", conflict.Code, conflict.Body.String())
	}
	classroomID := classroom["id"].(string)
	invited := call(t, handler, http.MethodPost, "/classrooms/"+classroomID+"/invitations", map[string]any{"name": "Student", "email": "invite-student@example.com"}, teacher)
	if invited.Code != http.StatusCreated {
		t.Fatalf("create invitation: %d %s", invited.Code, invited.Body.String())
	}
	invitation := decodeBody(t, invited)
	if invitation["status"] != "pending" || invitation["delivery"] != "sent" || invitation["join_code"] == "" {
		t.Fatalf("invitation delivery contract: %#v", invitation)
	}
	inviteRetry := call(t, handler, http.MethodPost, "/classrooms/"+classroomID+"/invitations", map[string]any{"name": "Renamed student", "email": "INVITE-STUDENT@example.com"}, teacher)
	if inviteRetry.Code != http.StatusCreated {
		t.Fatalf("retry invitation: %d %s", inviteRetry.Code, inviteRetry.Body.String())
	}
	if retry := decodeBody(t, inviteRetry); retry["id"] != invitation["id"] || retry["join_code"] != invitation["join_code"] {
		t.Fatalf("invitation retry created a different invite: original=%#v retry=%#v", invitation, retry)
	}
	link, err := url.Parse(mailer.links[0])
	if err != nil || link.Path != "/" || link.Query().Get("join_class") != invitation["join_code"] {
		t.Fatalf("shareable invite link = %q (%v)", mailer.links[0], err)
	}
	listed := call(t, handler, http.MethodGet, "/classrooms/"+classroomID+"/invitations", nil, teacher)
	if listed.Code != http.StatusOK || len(decodeArray(t, listed)) != 1 {
		t.Fatalf("invitation list must be a plain array: %d %s", listed.Code, listed.Body.String())
	}
	if response := call(t, handler, http.MethodGet, "/classrooms/"+classroomID+"/invitations", nil, otherTeacher); response.Code != http.StatusForbidden {
		t.Fatalf("foreign teacher invitation list: %d %s", response.Code, response.Body.String())
	}
	joined := call(t, handler, http.MethodPost, "/classrooms/join", map[string]any{"join_code": invitation["join_code"]}, student)
	if joined.Code != http.StatusOK {
		t.Fatalf("matching invited student join: %d %s", joined.Code, joined.Body.String())
	}
	joinedAgain := call(t, handler, http.MethodPost, "/classrooms/join", map[string]any{"join_code": invitation["join_code"]}, student)
	if joinedAgain.Code != http.StatusOK || decodeBody(t, joinedAgain)["id"] != classroomID {
		t.Fatalf("lost join response retry: %d %s", joinedAgain.Code, joinedAgain.Body.String())
	}
	if updated := call(t, handler, http.MethodPatch, "/auth/me", map[string]any{"name": "Updated learner"}, student); updated.Code != http.StatusOK {
		t.Fatalf("update joined student name: %d %s", updated.Code, updated.Body.String())
	}
	roster := call(t, handler, http.MethodGet, "/classrooms/"+classroomID+"/roster", nil, teacher)
	if rows := decodeArray(t, roster); roster.Code != http.StatusOK || len(rows) != 1 || rows[0].(map[string]any)["name"] != "Updated learner" {
		t.Fatalf("roster should use current profile name: %d %#v", roster.Code, rows)
	}
	listed = call(t, handler, http.MethodGet, "/classrooms/"+classroomID+"/invitations", nil, teacher)
	if rows := decodeArray(t, listed); listed.Code != http.StatusOK || rows[0].(map[string]any)["status"] != "accepted" {
		t.Fatalf("invitation acceptance status: %d %#v", listed.Code, rows)
	}
	left := call(t, handler, http.MethodPost, "/classrooms/"+classroomID+"/leave", nil, student)
	if left.Code != http.StatusNoContent {
		t.Fatalf("student leave: %d %s", left.Code, left.Body.String())
	}
	if staleJoin := call(t, handler, http.MethodPost, "/classrooms/join", map[string]any{"join_code": invitation["join_code"]}, student); staleJoin.Code != http.StatusUnprocessableEntity {
		t.Fatalf("accepted invite must not re-add a student after leave: %d %s", staleJoin.Code, staleJoin.Body.String())
	}

	mailer.err = errors.New("smtp unavailable")
	failed := call(t, handler, http.MethodPost, "/classrooms/"+classroomID+"/invitations", map[string]any{"name": "Another student", "email": "another@example.com"}, teacher)
	if failed.Code != http.StatusCreated || decodeBody(t, failed)["delivery"] != "failed" {
		t.Fatalf("failed delivery status: %d %s", failed.Code, failed.Body.String())
	}
	failedID := decodeBody(t, failed)["id"].(string)
	deleted := call(t, handler, http.MethodDelete, "/classrooms/"+classroomID+"/invitations/"+failedID, nil, teacher)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete invitation: %d %s", deleted.Code, deleted.Body.String())
	}
}

func TestQuestionBankIsTeacherOwnedAndPersistent(t *testing.T) {
	_, handler := newTestServer(t, "active")
	teacher := registerTeacher(t, handler, "bank-owner@example.com")
	otherTeacher := registerTeacher(t, handler, "bank-other@example.com")
	student, _ := registerStudent(t, handler, "bank-student@example.com")
	created := call(t, handler, http.MethodPost, "/question-bank", map[string]any{
		"question": "What is RPO?", "options": []string{"Recovery point objective", "Recovery time objective"}, "answer": 0, "points": 3, "feedback": "How much data can be lost?",
	}, teacher)
	if created.Code != http.StatusCreated {
		t.Fatalf("save bank question: %d %s", created.Code, created.Body.String())
	}
	item := decodeBody(t, created)
	if item["id"] == "" || item["saved_at"] == "" || item["points"] != float64(3) || item["feedback"] != "How much data can be lost?" {
		t.Fatalf("saved question shape: %#v", item)
	}
	listed := call(t, handler, http.MethodGet, "/question-bank", nil, teacher)
	if listed.Code != http.StatusOK || len(decodeArray(t, listed)) != 1 {
		t.Fatalf("bank list: %d %s", listed.Code, listed.Body.String())
	}
	if foreign := call(t, handler, http.MethodGet, "/question-bank", nil, otherTeacher); foreign.Code != http.StatusOK || len(decodeArray(t, foreign)) != 0 {
		t.Fatalf("foreign bank list: %d %s", foreign.Code, foreign.Body.String())
	}
	if denied := call(t, handler, http.MethodGet, "/question-bank", nil, student); denied.Code != http.StatusForbidden {
		t.Fatalf("student bank access: %d %s", denied.Code, denied.Body.String())
	}
	if denied := call(t, handler, http.MethodDelete, "/question-bank/"+item["id"].(string), nil, otherTeacher); denied.Code != http.StatusNotFound {
		t.Fatalf("foreign bank delete: %d %s", denied.Code, denied.Body.String())
	}
	if deleted := call(t, handler, http.MethodDelete, "/question-bank/"+item["id"].(string), nil, teacher); deleted.Code != http.StatusNoContent {
		t.Fatalf("delete bank item: %d %s", deleted.Code, deleted.Body.String())
	}
}

func TestAccountSettingsPasswordAndResetRoutes(t *testing.T) {
	server, _ := newTestServer(t, "active")
	mailer := &passwordResetHTTPStub{}
	server.Auth.ResetMailer = mailer
	server.Settings.PublicBaseURL = "https://slate.example.test"
	handler := server.Handler()
	student, registered := registerStudent(t, handler, "settings-user@example.com")
	user := registered["user"].(map[string]any)
	updated := call(t, handler, http.MethodPatch, "/auth/me", map[string]any{"name": "Updated name"}, student)
	updatedBody := decodeBody(t, updated)
	if updated.Code != http.StatusOK || updatedBody["name"] != "Updated name" || updatedBody["password_hash"] != nil {
		t.Fatalf("profile patch safe user: %d %#v", updated.Code, updatedBody)
	}
	if wrong := call(t, handler, http.MethodPost, "/auth/change-password", map[string]any{"current_password": "wrong-password", "password": "new-password"}, student); wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current password: %d %s", wrong.Code, wrong.Body.String())
	}
	changed := call(t, handler, http.MethodPost, "/auth/change-password", map[string]any{"current_password": "hunter2hunter2", "password": "new-password"}, student)
	if changed.Code != http.StatusOK {
		t.Fatalf("change password: %d %s", changed.Code, changed.Body.String())
	}
	if oldSession := call(t, handler, http.MethodGet, "/auth/me", nil, student); oldSession.Code != http.StatusUnauthorized {
		t.Fatalf("password change left access token valid: %d %s", oldSession.Code, oldSession.Body.String())
	}
	login := call(t, handler, http.MethodPost, "/auth/login", map[string]any{"email": "settings-user@example.com", "password": "new-password"}, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login after password change: %d %s", login.Code, login.Body.String())
	}
	active := map[string]string{"Authorization": "Bearer " + decodeBody(t, login)["access_token"].(string)}

	unknown := call(t, handler, http.MethodPost, "/auth/forgot-password", map[string]any{"email": "unknown@example.com"}, nil)
	known := call(t, handler, http.MethodPost, "/auth/forgot-password", map[string]any{"email": "settings-user@example.com"}, nil)
	if unknown.Code != http.StatusOK || known.Code != http.StatusOK || unknown.Body.String() != known.Body.String() || len(mailer.links) != 1 {
		t.Fatalf("forgot-password enumeration response: unknown=%d %s known=%d %s links=%v", unknown.Code, unknown.Body.String(), known.Code, known.Body.String(), mailer.links)
	}
	resetURL, err := url.Parse(mailer.links[0])
	if err != nil || resetURL.Path != "/login" || resetURL.Query().Get("token") == "" {
		t.Fatalf("reset URL = %q (%v)", mailer.links[0], err)
	}
	token := resetURL.Query().Get("token")
	reset := call(t, handler, http.MethodPost, "/auth/reset-password", map[string]any{"token": token, "password": "reset-password"}, nil)
	if reset.Code != http.StatusOK {
		t.Fatalf("reset password: %d %s", reset.Code, reset.Body.String())
	}
	if oldSession := call(t, handler, http.MethodGet, "/auth/me", nil, active); oldSession.Code != http.StatusUnauthorized {
		t.Fatalf("password reset left access token valid: %d %s", oldSession.Code, oldSession.Body.String())
	}
	if reused := call(t, handler, http.MethodPost, "/auth/reset-password", map[string]any{"token": token, "password": "third-password"}, nil); reused.Code != http.StatusBadRequest {
		t.Fatalf("reused reset token: %d %s", reused.Code, reused.Body.String())
	}
	if safeUser := user["user_id"].(string); safeUser == "" {
		t.Fatal("registration did not include user id")
	}
}
