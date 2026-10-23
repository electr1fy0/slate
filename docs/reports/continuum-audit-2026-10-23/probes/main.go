// Run from the repository root: go run ./docs/reports/continuum-audit-2026-10-23/probes
// These probes use an isolated in-memory API. No network or AWS resources are used.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/httpapi"
	"github.com/ayush/slate/internal/middleware"
	"github.com/ayush/slate/internal/model"
	"github.com/ayush/slate/internal/store"
)

type observation struct {
	Probe    string `json:"probe"`
	Observed any    `json:"observed"`
}

func main() {
	ctx := context.Background()
	cfg := config.Defaults()
	cfg.LoginRateLimit, cfg.RegisterRateLimit = 100, 100
	cfg.RoleFile = "/nonexistent/continuum-audit-role"
	db := store.NewMemory(cfg.Region)
	s := httpapi.New(cfg, db)
	defer s.Boards.FlushAll(ctx)
	h := s.Handler()
	tokens := map[string]string{}
	for _, u := range []model.User{
		{UserID: "audit-student", Email: "student@audit.test", Name: "Audit learner", Role: "student"},
		{UserID: "audit-owner", Email: "owner@audit.test", Name: "Audit owner", Role: "teacher"},
		{UserID: "audit-other", Email: "other@audit.test", Name: "Other teacher", Role: "teacher"},
	} {
		if err := db.CreateUser(ctx, &u, "unused-in-audit"); err != nil {
			panic(err)
		}
		token, err := s.Auth.EncodeToken(&u, "access", time.Hour)
		if err != nil {
			panic(err)
		}
		tokens[u.UserID] = token
	}
	call := func(method, path, body, user string, admin bool) (int, any) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if user != "" {
			r.Header.Set("Authorization", "Bearer "+tokens[user])
		}
		if admin {
			r.Header.Set("x-admin-token", cfg.AdminToken)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var result any
		if json.Unmarshal(w.Body.Bytes(), &result) != nil {
			result = w.Body.String()
		}
		return w.Code, result
	}
	rows := []observation{}
	add := func(name string, status int, body any) {
		rows = append(rows, observation{name, map[string]any{"http_status": status, "body": body}})
	}
	qs := []model.Question{{Index: 0, Question: "One?", Options: []string{"yes", "no"}, Answer: 0}}
	seed := func(id string, window map[string]any) {
		if err := db.EnsureExam(ctx, id, "Audit assessment", qs, window); err != nil {
			panic(err)
		}
	}
	seed("timed-direct", map[string]any{"duration_minutes": 1, "max_attempts": 1})
	code, body := call("POST", "/exam/timed-direct/submit", `{"answers":{"0":0},"request_id":"audit-direct"}`, "audit-student", false)
	add("timed_submission_without_start", code, body)
	seed("alias-index", nil)
	code, body = call("POST", "/exam/alias-index/submit", `{"answers":{"00":0},"request_id":"audit-alias"}`, "audit-student", false)
	add("noncanonical_question_index", code, body)
	seed("bad-answer", nil)
	code, body = call("POST", "/exam/bad-answer/submit", `{"answers":{"0":99}}`, "audit-student", false)
	add("invalid_answer_status", code, body)
	seed("staff-submit", nil)
	code, body = call("POST", "/exam/staff-submit/submit", `{"answers":{"0":0},"request_id":"audit-teacher"}`, "audit-owner", false)
	add("teacher_submission", code, body)
	seed("mutable-clock", map[string]any{"duration_minutes": 15})
	_, before := call("GET", "/exam/mutable-clock", "", "audit-student", false)
	_, _ = call("PATCH", "/exam/mutable-clock", `{"duration_minutes":1}`, "", true)
	_, after := call("GET", "/exam/mutable-clock", "", "audit-student", false)
	rows = append(rows, observation{"running_deadline_changes", map[string]any{"before": before, "after": after}})
	seed("save-dedupe", nil)
	_, _ = call("GET", "/exam/save-dedupe/attempt", "", "audit-student", false)
	_, _ = call("PUT", "/exam/save-dedupe/attempt", `{"answers":{"0":0},"expected_version":0,"request_id":"audit-same-save"}`, "audit-student", false)
	code, body = call("PUT", "/exam/save-dedupe/attempt", `{"answers":{"0":1},"expected_version":1,"request_id":"audit-same-save"}`, "audit-student", false)
	add("autosave_request_payload_mismatch", code, body)
	seed("late-close", map[string]any{"allow_late": true})
	_, _ = call("POST", "/exam/late-close/close", "", "", true)
	code, body = call("GET", "/exam/late-close", "", "audit-student", false)
	add("close_still_accepts_late_work", code, body)
	code, body = call("POST", "/exam", `{"id":"invalid-date","title":"Date probe","questions":[{"question":"Q","options":["A","B"],"answer":0}],"opens_at":"garbage"}`, "", true)
	add("single_invalid_availability_boundary", code, body)
	code, body = call("POST", "/auth/register", `{"email":" student2@audit.test ","password":"audit-password","name":"Learner"}`, "", false)
	add("registration_email_whitespace", code, body)
	code, body = call("POST", "/auth/register", `{"email":"unicode@audit.test","password":"audit-password","name":"`+strings.Repeat("界", 21)+`"}`, "", false)
	add("registration_name_byte_limit", code, body)
	s.Boards.Absorb("private-audit-room", []map[string]any{{"id": "secret-shape", "version": 1, "type": "rectangle", "text": "other class content"}})
	if err := s.Boards.Flush(ctx, "private-audit-room"); err != nil {
		panic(err)
	}
	code, body = call("GET", "/api/boards/private-audit-room/export", "", "audit-student", false)
	add("unrelated_learner_board_export", code, body)
	code, body = call("GET", "/api/boards/private-audit-room/history", "", "audit-student", false)
	add("unrelated_learner_board_history", code, body)
	code, body = call("POST", "/api/boards/private-audit-room/restore", `{"version":1}`, "audit-other", false)
	add("unrelated_teacher_board_restore", code, body)
	code, body = call("GET", "/boards/private-audit-room/history", "", "audit-owner", false)
	add("frontend_board_history_route", code, body)
	panicMessage := "none"
	func() {
		defer func() {
			if v := recover(); v != nil {
				panicMessage = fmt.Sprint(v)
			}
		}()
		middleware.NewRateLimiter(map[string]middleware.Limit{"POST /auth/login": {MaxHits: 0, Window: time.Minute}}).Allow("POST", "/auth/login", "local")
	}()
	rows = append(rows, observation{"zero_rate_limit_panic", panicMessage})
	loaded, err := config.LoadFrom(func(k string) string {
		if k == "LOGIN_RATE_LIMIT" {
			return "-1"
		}
		return ""
	})
	rows = append(rows, observation{"negative_rate_limit_configuration", map[string]any{"accepted": err == nil, "login_limit": loaded.LoginRateLimit}})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	r.Header.Set("Authorization", "Bearer "+tokens["audit-student"])
	h.ServeHTTP(w, r)
	rows = append(rows, observation{"sensitive_response_cache_control", w.Header().Get("Cache-Control")})
	encoded, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		panic(err)
	}
	fmt.Println(string(encoded))
}
