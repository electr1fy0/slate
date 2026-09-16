package httpapi

import (
	"net/http"
	"testing"
)

func TestQuizDraftPreviewAndDurableAttemptLifecycle(t *testing.T) {
	_, handler := newTestServer(t, "active")
	teacher := registerTeacher(t, handler, "quiz-author@example.com")

	created := call(t, handler, http.MethodPost, "/quiz/drafts", map[string]any{
		"id":    "physics-draft",
		"title": "Physics checkpoint",
		"questions": []map[string]any{
			{"question": "What is force?", "options": []string{"Mass", "Mass times acceleration"}, "answer": 1, "points": 2, "feedback": "F = ma."},
			{"question": "What is RPO?", "options": []string{"Data loss window", "Downtime"}, "answer": 0, "points": 3},
		},
		"duration_minutes":   10,
		"allow_late":         true,
		"late_grace_minutes": 5,
	}, teacher)
	if created.Code != http.StatusCreated {
		t.Fatalf("create draft: %d %s", created.Code, created.Body.String())
	}
	if decodeBody(t, created)["status"] != "draft" {
		t.Fatalf("draft status: %s", created.Body.String())
	}

	list := call(t, handler, http.MethodGet, "/quiz/drafts", nil, teacher)
	if list.Code != http.StatusOK || len(decodeArray(t, list)) != 1 {
		t.Fatalf("list drafts: %d %s", list.Code, list.Body.String())
	}

	updated := call(t, handler, http.MethodPatch, "/quiz/drafts/physics-draft", map[string]any{
		"id": "physics-draft", "title": "Physics checkpoint updated",
		"questions": []map[string]any{{"question": "What is force?", "options": []string{"Mass", "Mass times acceleration"}, "answer": 1, "points": 2}},
	}, teacher)
	if updated.Code != http.StatusOK || decodeBody(t, updated)["title"] != "Physics checkpoint updated" {
		t.Fatalf("update draft: %d %s", updated.Code, updated.Body.String())
	}

	duplicate := call(t, handler, http.MethodPost, "/quiz/drafts/physics-draft/duplicate", map[string]any{"title": "Physics copy"}, teacher)
	if duplicate.Code != http.StatusCreated {
		t.Fatalf("duplicate draft: %d %s", duplicate.Code, duplicate.Body.String())
	}

	published := call(t, handler, http.MethodPost, "/quiz/drafts/physics-draft/publish", nil, teacher)
	if published.Code != http.StatusCreated {
		t.Fatalf("publish draft: %d %s", published.Code, published.Body.String())
	}

	preview := call(t, handler, http.MethodGet, "/exam/physics-draft/preview", nil, teacher)
	previewBody := decodeBody(t, preview)
	if preview.Code != http.StatusOK || len(previewBody["questions"].([]any)) != 1 {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	previewQuestion := previewBody["questions"].([]any)[0].(map[string]any)
	if previewQuestion["answer"] != float64(1) || previewQuestion["points"] != float64(2) {
		t.Fatalf("preview answer metadata: %#v", previewQuestion)
	}

	student, _ := registerStudent(t, handler, "quiz-student@example.com")
	started := call(t, handler, http.MethodGet, "/exam/physics-draft/attempt", nil, student)
	startedBody := decodeBody(t, started)
	if started.Code != http.StatusOK || len(startedBody["answers"].(map[string]any)) != 0 {
		t.Fatalf("start attempt: %d %s", started.Code, started.Body.String())
	}

	saved := call(t, handler, http.MethodPut, "/exam/physics-draft/attempt", map[string]any{
		"answers": map[string]int{"0": 1}, "expected_version": 0, "request_id": "autosave-1",
	}, student)
	savedBody := decodeBody(t, saved)
	if saved.Code != http.StatusOK || savedBody["status"] != "saved" || savedBody["version"] != float64(1) {
		t.Fatalf("save attempt: %d %s", saved.Code, saved.Body.String())
	}

	duplicateSave := call(t, handler, http.MethodPut, "/exam/physics-draft/attempt", map[string]any{
		"answers": map[string]int{"0": 1}, "expected_version": 0, "request_id": "autosave-1",
	}, student)
	if duplicateSave.Code != http.StatusOK || decodeBody(t, duplicateSave)["status"] != "duplicate" {
		t.Fatalf("duplicate save: %d %s", duplicateSave.Code, duplicateSave.Body.String())
	}

	submitted := call(t, handler, http.MethodPost, "/exam/physics-draft/attempt/submit", map[string]any{"request_id": "submit-1"}, student)
	submittedBody := decodeBody(t, submitted)
	if submitted.Code != http.StatusOK || submittedBody["status"] != "committed" || submittedBody["score"] != float64(2) || submittedBody["total"] != float64(2) {
		t.Fatalf("submit saved attempt: %d %s", submitted.Code, submitted.Body.String())
	}

	afterSubmit := call(t, handler, http.MethodPut, "/exam/physics-draft/attempt", map[string]any{
		"answers": map[string]int{"0": 1}, "expected_version": 1, "request_id": "autosave-2",
	}, student)
	if afterSubmit.Code != http.StatusConflict {
		t.Fatalf("autosave after submit: %d %s", afterSubmit.Code, afterSubmit.Body.String())
	}
}
