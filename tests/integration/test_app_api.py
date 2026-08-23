from pathlib import Path
from types import SimpleNamespace

import pytest
from fastapi.testclient import TestClient

import main
from chaos import chaos_state
from config import settings

EXAM = "cs101-midterm"


def auth():
    return {"x-admin-token": settings.admin_token}


def student_headers(client, email="stu@example.com"):
    resp = client.post("/auth/register", json={
        "email": email, "password": "hunter2hunter2", "name": "Stu Student"})
    assert resp.status_code == 201, resp.text
    return {"Authorization": f"Bearer {resp.json()['access_token']}"}


def teacher_headers(client, email="teach@example.com"):
    created = client.post("/admin/users", headers=auth(), json={
        "email": email, "password": "hunter2hunter2", "name": "Tea Cher",
        "role": "teacher"})
    assert created.status_code == 201, created.text
    tokens = client.post("/auth/login", json={
        "email": email, "password": "hunter2hunter2"}).json()
    return {"Authorization": f"Bearer {tokens['access_token']}"}


@pytest.fixture()
def client(make_table, tmp_path):
    make_table("continuum")
    settings.role_file = str(tmp_path / "role")
    with TestClient(main.app) as c:
        yield c


def standby(client):
    Path(settings.role_file).write_text("standby")


# ---------------------------------------------------------------- role helpers

def test_current_role_falls_back_to_app_role_when_file_missing(tmp_path):
    settings.app_role = "active"
    settings.role_file = str(tmp_path / "missing-dir" / "role")
    assert main.current_role() == "active"


def test_current_role_reads_file_content(tmp_path):
    settings.role_file = str(tmp_path / "role")
    Path(settings.role_file).write_text("standby\n")
    assert main.current_role() == "standby"


def test_current_role_blank_file_means_use_default(tmp_path):
    settings.app_role = "active"
    settings.role_file = str(tmp_path / "role")
    Path(settings.role_file).write_text("   ")
    assert main.current_role() == "active"


def test_require_active_blocks_standby(tmp_path):
    from fastapi import HTTPException

    settings.app_role = "standby"
    settings.role_file = str(tmp_path / "missing" / "role")
    with pytest.raises(HTTPException) as exc:
        main.require_active()
    assert exc.value.status_code == 503
    assert "read-only" in exc.value.detail


def test_startup_seed_failure_is_logged_not_fatal(make_table, tmp_path, capsys, monkeypatch):
    make_table("continuum")
    settings.role_file = str(tmp_path / "role")

    def boom(*args, **kwargs):
        raise RuntimeError("table gone")

    monkeypatch.setattr(main.db, "ensure_exam", boom)
    with TestClient(main.app):
        pass
    out = capsys.readouterr().out
    assert "startup_seed_skipped" in out


def test_startup_seeds_exam_once_client_starts(client, monkeypatch):
    headers = student_headers(client)
    meta = client.get(f"/exam/{EXAM}", headers=headers).json()
    assert meta["title"] == "CS101 Midterm Examination"


# ------------------------------------------------------------------- index etc

def test_index_active_copy(client):
    text = client.get("/").text
    assert "CONTINUUM EXAM PLATFORM" in text
    assert f"[{settings.region}]" in text
    assert "role=ACTIVE" in text


def test_index_standby_copy(client):
    standby(client)
    text = client.get("/").text
    assert "CONTINUUM STANDBY NODE" in text
    assert "Exam delivery unavailable" in text


def test_liveness_probe_shape(client):
    body = client.get("/health/live").json()
    assert body == {"status": "alive", "region": settings.region, "role": "active"}


# ------------------------------------------------------------------ deep probe

def test_deep_healthy_everything_green(client, monkeypatch):
    monkeypatch.setattr(main.shutil, "disk_usage",
                        lambda path: SimpleNamespace(total=100, used=40))
    monkeypatch.setattr(main.os, "getloadavg", lambda: (1.0, 1.0, 1.0))
    resp = client.get("/health/deep")
    assert resp.status_code == 200
    body = resp.json()
    assert body["healthy"] is True
    assert body["region"] == settings.region
    assert body["checks"]["ddb"]["ok"] is True
    assert body["checks"]["error_rate"]["ok"] is True
    assert body["checks"]["disk"]["ok"] is True
    assert body["checks"]["load"]["ok"] is True
    assert body["chaos"] == {"error_pct": 0, "latency_ms": 0, "db_stall": False}


def test_deep_db_stall_flag_fails_without_touching_ddb(client, monkeypatch):
    chaos_state.db_stall = True

    def unexpected(region):
        raise AssertionError("heartbeat must not run during stall")

    monkeypatch.setattr(main.db, "heartbeat_probe", unexpected)
    resp = client.get("/health/deep")
    assert resp.status_code == 503
    assert resp.json()["checks"]["ddb"] == {"ok": False, "reason": "db_stall chaos flag"}


def test_deep_probe_reporting_unhealthy(client, monkeypatch):
    monkeypatch.setattr(main.db, "heartbeat_probe", lambda region: {"ok": False, "latency_ms": 1})
    resp = client.get("/health/deep")
    assert resp.status_code == 503
    assert resp.json()["checks"]["ddb"]["ok"] is False


def test_deep_probe_raising_is_caught_and_reported(client, monkeypatch):
    def boom(region):
        raise TimeoutError("ddb unreachable")

    monkeypatch.setattr(main.db, "heartbeat_probe", boom)
    resp = client.get("/health/deep")
    assert resp.status_code == 503
    check = resp.json()["checks"]["ddb"]
    assert check["ok"] is False
    assert "ddb unreachable" in check["reason"]


def test_deep_error_rate_breach(client):
    for _ in range(6):
        chaos_state.status_codes.append(500)
    chaos_state.status_codes.append(200)
    resp = client.get("/health/deep")
    assert resp.status_code == 503
    err = resp.json()["checks"]["error_rate"]
    assert err["value"] > err["threshold"]
    assert err["ok"] is False


def test_deep_disk_breach(client, monkeypatch):
    monkeypatch.setattr(main.shutil, "disk_usage",
                        lambda path: SimpleNamespace(total=100, used=95))
    resp = client.get("/health/deep")
    assert resp.status_code == 503
    disk = resp.json()["checks"]["disk"]
    assert disk["used_pct"] == 95.0
    assert disk["ok"] is False


def test_deep_load_breach(client, monkeypatch):
    monkeypatch.setattr(main.os, "getloadavg", lambda: (9.9, 1.0, 0.5))
    resp = client.get("/health/deep")
    assert resp.status_code == 503
    load = resp.json()["checks"]["load"]
    assert load["loadavg"] == 9.9
    assert load["ok"] is False


# ---------------------------------------------------------------- exam serving

def test_get_exam_strips_answer_keys(client):
    headers = student_headers(client)
    questions = client.get(f"/exam/{EXAM}", headers=headers).json()["questions"]
    assert len(questions) == 5
    assert all("answer" not in q and q["options"] for q in questions)


def test_get_exam_requires_authentication(client):
    assert client.get(f"/exam/{EXAM}").status_code == 401


def test_get_exam_rejects_garbage_token(client):
    resp = client.get(f"/exam/{EXAM}", headers={"Authorization": "Bearer junk"})
    assert resp.status_code == 401


def test_get_exam_unknown_id_is_404(client):
    headers = student_headers(client)
    resp = client.get("/exam/does-not-exist", headers=headers)
    assert resp.status_code == 404
    assert resp.json()["detail"] == "exam not found"


def test_get_exam_blocked_on_standby(client):
    headers = student_headers(client)
    standby(client)
    resp = client.get(f"/exam/{EXAM}", headers=headers)
    assert resp.status_code == 503
    assert "read-only" in resp.json()["detail"]


def test_get_exam_data_layer_failure_is_503(client, monkeypatch):
    def boom(exam_id):
        raise RuntimeError("nope")

    monkeypatch.setattr(main.db, "get_exam", boom)
    headers = student_headers(client)
    resp = client.get(f"/exam/{EXAM}", headers=headers)
    assert resp.status_code == 503
    assert resp.json()["detail"] == "data layer unavailable"


# -------------------------------------------------------------- submissions

def test_submit_commits_attempt(client):
    headers = student_headers(client)
    resp = client.post(f"/exam/{EXAM}/submit",
                       headers=headers, json={"answers": {"0": 1}})
    body = resp.json()
    assert body["status"] == "committed"
    assert body["version"] == 1
    assert len(body["request_id"]) == 36


def test_submit_requires_authentication(client):
    assert client.post(f"/exam/{EXAM}/submit",
                       json={"answers": {"0": 1}}).status_code == 401


def test_submit_retries_with_same_request_id_are_duplicates(client):
    headers = student_headers(client)
    payload = {"answers": {"0": 1}, "request_id": "rid-42"}
    first = client.post(f"/exam/{EXAM}/submit", headers=headers, json=payload).json()
    again = client.post(f"/exam/{EXAM}/submit", headers=headers, json=payload).json()
    assert first["status"] == "committed"
    assert again["status"] == "duplicate"
    assert again["request_id"] == "rid-42"
    assert again["version"] == first["version"]


def test_submit_second_attempt_maps_to_409_conflict(client):
    headers = student_headers(client)
    first = client.post(f"/exam/{EXAM}/submit", headers=headers,
                        json={"answers": {"0": 1}, "request_id": "rid-a"})
    assert first.status_code == 200
    second = client.post(f"/exam/{EXAM}/submit", headers=headers,
                         json={"answers": {"0": 0}, "request_id": "rid-b"})
    assert second.status_code == 409
    assert second.json()["detail"] == "this student already submitted for this exam"


def test_submit_other_student_same_exam_is_independent(client):
    first = client.post(f"/exam/{EXAM}/submit",
                        headers=student_headers(client, "one@example.com"),
                        json={"answers": {"0": 1}})
    second = client.post(f"/exam/{EXAM}/submit",
                         headers=student_headers(client, "two@example.com"),
                         json={"answers": {"0": 1}})
    assert first.status_code == 200 and second.status_code == 200


def test_submit_records_authenticated_identity_not_client_claims(client):
    headers = student_headers(client, "named@example.com")
    resp = client.post(f"/exam/{EXAM}/submit", headers=headers,
                       json={"answers": {"0": 1}})
    assert resp.status_code == 200
    listed = client.get("/admin/submissions", headers=auth()).json()
    assert len(listed) == 1
    assert listed[0]["student_name"] == "Stu Student"
    assert listed[0]["sk"].startswith("STUDENT#")


def test_submit_other_data_failures_map_to_generic_detail(client, monkeypatch):
    def broken(*args, **kwargs):
        raise ValueError("serialization")

    monkeypatch.setattr(main.db, "submit_attempt", broken)
    headers = student_headers(client)
    resp = client.post(f"/exam/{EXAM}/submit",
                       headers=headers, json={"answers": {"0": 1}})
    assert resp.status_code == 503
    assert resp.json()["detail"] == "data layer unavailable"


def test_submit_blocked_on_standby(client):
    headers = student_headers(client)
    standby(client)
    resp = client.post(f"/exam/{EXAM}/submit",
                       headers=headers, json={"answers": {"0": 1}})
    assert resp.status_code == 503


# ------------------------------------------------------------ admin endpoints

ADMIN_ROUTES = [
    ("GET", "/admin/submissions"),
    ("GET", "/admin/status"),
    ("POST", "/admin/chaos"),
    ("POST", "/admin/chaos/reset"),
    ("POST", "/admin/promote"),
]


@pytest.mark.parametrize("method,path", ADMIN_ROUTES)
def test_admin_routes_reject_missing_or_wrong_token(client, method, path):
    kwargs = {} if method == "GET" else {"json": {}}
    assert client.request(method, path, **kwargs).status_code == 403
    assert client.request(method, path, headers={"x-admin-token": "wrong"},
                          **kwargs).status_code == 403


def test_admin_submissions_lists_attempts_with_limit(client):
    for email in ("a@example.com", "b@example.com", "c@example.com"):
        client.post(f"/exam/{EXAM}/submit",
                    headers=student_headers(client, email), json={"answers": {"0": 1}})
    listed = client.get("/admin/submissions", headers=auth(), params={"limit": 100}).json()
    assert len(listed) == 3
    assert all(item["sk"].startswith("STUDENT#") for item in listed)


def test_admin_status_reports_region_role_chaos(client):
    chaos_state.error_pct = 10
    body = client.get("/admin/status", headers=auth()).json()
    assert body["region"] == settings.region
    assert body["role"] == "active"
    assert body["chaos"]["error_pct"] == 10


def test_set_chaos_clamps_values(client):
    out = client.post("/admin/chaos", headers=auth(),
                      json={"error_pct": 150, "latency_ms": -5}).json()["chaos"]
    assert out["error_pct"] == 100
    assert out["latency_ms"] == 0
    out = client.post("/admin/chaos", headers=auth(),
                      json={"error_pct": -3, "latency_ms": 40, "db_stall": True}).json()["chaos"]
    assert (out["error_pct"], out["latency_ms"], out["db_stall"]) == (0, 40, True)


def test_set_chaos_empty_payload_changes_nothing(client):
    chaos_state.error_pct = 33
    out = client.post("/admin/chaos", headers=auth(), json={}).json()["chaos"]
    assert out["error_pct"] == 33


def test_reset_chaos_endpoint_clears_flags(client):
    chaos_state.error_pct = 80
    chaos_state.latency_ms = 500
    chaos_state.db_stall = True
    out = client.post("/admin/chaos/reset", headers=auth())
    assert out.json() == {"chaos": {"error_pct": 0, "latency_ms": 0, "db_stall": False}}
    assert (chaos_state.error_pct, chaos_state.latency_ms, chaos_state.db_stall) == (0, 0, False)


# -------------------------------------------------------------------- promote

def test_promote_flips_standby_role_file_to_active(client):
    standby(client)
    body = client.post("/admin/promote", headers=auth()).json()
    assert body["role"] == "active"
    assert Path(settings.role_file).read_text() == "active"


def test_promote_when_already_active_writes_nothing(client):
    body = client.post("/admin/promote", headers=auth()).json()
    assert body["role"] == "active"
    assert not Path(settings.role_file).exists()


def test_promote_without_role_dir_still_activates_via_settings(client, tmp_path):
    settings.role_file = str(tmp_path / "nodir" / "role")
    settings.app_role = "standby"
    body = client.post("/admin/promote", headers=auth()).json()
    assert body["role"] == "active"
    assert not Path(settings.role_file).exists()


# ------------------------------------------------------------- quiz authoring

def make_payload(**overrides):
    payload = {"title": "Physics Quiz", "questions": [
        {"question": "What is 9.81 m/s^2?", "options": ["gravity", "speed"], "answer": 0}]}
    payload.update(overrides)
    return payload


def test_create_exam_with_explicit_id_persists_and_serves_sanitised(client):
    resp = client.post("/exam", headers=auth(), json={**make_payload(), "id": "Phys 101"})
    assert resp.status_code == 201
    assert resp.json() == {"id": "phys-101", "title": "Physics Quiz", "question_count": 1}
    served = client.get("/exam/phys-101", headers=student_headers(client)).json()
    assert served["question_count"] == 1
    assert all("answer" not in q for q in served["questions"])


def test_create_exam_via_teacher_jwt_and_via_admin_token_both_allowed(client):
    teacher = teacher_headers(client)
    via_teacher = client.post("/exam", headers=teacher, json=make_payload(title="T Quiz"))
    assert via_teacher.status_code == 201
    via_admin = client.post("/exam", headers=auth(), json=make_payload(title="A Quiz"))
    assert via_admin.status_code == 201


def test_create_exam_rejects_anonymous_with_401(client):
    assert client.post("/exam", json=make_payload()).status_code == 401


def test_create_exam_rejects_wrong_admin_token_without_bearer(client):
    resp = client.post("/exam", headers={"x-admin-token": "wrong"}, json=make_payload())
    assert resp.status_code == 403


def test_create_exam_rejects_student_role(client):
    student = student_headers(client)
    assert client.post("/exam", headers=student, json=make_payload()).status_code == 403


def test_create_exam_blocked_on_standby(client):
    standby(client)
    assert client.post("/exam", headers=auth(), json=make_payload()).status_code == 503


@pytest.mark.parametrize("payload,fragment", [
    ({"title": "   "}, "title must not be empty"),
    ({"questions": []}, "at least one question"),
    ({"questions": [{"question": "  ", "options": ["a", "b"], "answer": 0}]}, "question 0 needs text"),
    ({"questions": [{"question": "q", "options": ["only"], "answer": 0}]}, "question 0 needs text"),
    ({"questions": [{"question": "q", "options": ["a", "   "], "answer": 0}]}, "question 0 needs text"),
    ({"questions": [{"question": "q", "options": ["a", "b"], "answer": -1}]}, "answer index out of range"),
    ({"questions": [{"question": "q", "options": ["a", "b"], "answer": 2}]}, "answer index out of range"),
])
def test_create_exam_validation_errors_are_actionable(client, payload, fragment):
    resp = client.post("/exam", headers=auth(), json=make_payload(**payload))
    assert resp.status_code == 400
    assert fragment in resp.json()["detail"]


def test_create_exam_rejects_duplicate_explicit_id(client):
    body = {**make_payload(), "id": "dupe"}
    assert client.post("/exam", headers=auth(), json=body).status_code == 201
    conflict = client.post("/exam", headers=auth(), json=body)
    assert conflict.status_code == 409
    assert "dupe" in conflict.json()["detail"]


def test_create_exam_generates_slug_then_numbered_suffix_on_collision(client):
    first = client.post("/exam", headers=auth(), json=make_payload(title="Fun Quiz!")).json()
    second = client.post("/exam", headers=auth(), json=make_payload(title="Fun Quiz!")).json()
    assert (first["id"], second["id"]) == ("fun-quiz", "fun-quiz-2")


def test_create_exam_falls_back_to_random_id_after_saturation(client, monkeypatch):
    monkeypatch.setattr(main.db, "get_exam", lambda exam_id: {"some": "exam"})
    created = client.post("/exam", headers=auth(), json=make_payload())
    assert created.status_code == 201
    assert created.json()["id"].startswith("quiz-")


def test_list_exams_is_public_includes_seeded_and_created(client):
    client.post("/exam", headers=auth(), json=make_payload(title="Zebra Quiz"))
    listed = client.get("/exams").json()
    titles = {item["title"] for item in listed}
    assert titles == {"CS101 Midterm Examination", "Zebra Quiz"}
    assert all(set(item) == {"id", "title", "question_count", "created_at"} for item in listed)


def test_list_exams_allowed_on_standby(client):
    standby(client)
    assert client.get("/exams").status_code == 200


# ---------------------------------------------------------------- graded submits

def test_submit_reports_score_against_answer_key(client):
    body = client.post(f"/exam/{EXAM}/submit",
                       headers=student_headers(client),
                       json={"answers": {"0": 1, "1": 1, "2": 0}}).json()
    assert (body["score"], body["total"]) == (1, 5)


def test_submit_duplicate_response_also_carries_grade(client):
    headers = student_headers(client)
    payload = {"answers": {"0": 1}, "request_id": "rid-grade"}
    first = client.post(f"/exam/{EXAM}/submit", headers=headers, json=payload).json()
    again = client.post(f"/exam/{EXAM}/submit", headers=headers, json=payload).json()
    assert first["status"] == "committed" and again["status"] == "duplicate"
    assert (first["score"], again["total"]) == (1, 5)


# ------------------------------------------------------------------------ CORS

def test_preflight_from_configured_origin_is_accepted(client):
    resp = client.options("/exams", headers={
        "Origin": "http://localhost:3000",
        "Access-Control-Request-Method": "GET"})
    assert resp.status_code == 200
    assert resp.headers["access-control-allow-origin"] == "http://localhost:3000"


def test_preflight_from_unknown_origin_has_no_allow_header(client):
    resp = client.options("/exams", headers={
        "Origin": "https://evil.example",
        "Access-Control-Request-Method": "GET"})
    assert "access-control-allow-origin" not in resp.headers


# ------------------------------------------------------------------ request id

def test_request_id_echoed_on_every_response(client):
    first = client.get("/health/live")
    second = client.get("/exams")
    assert first.headers["x-request-id"]
    assert len(first.headers["x-request-id"]) == 16
    assert first.headers["x-request-id"] != second.headers["x-request-id"]


def test_request_id_honours_incoming_correlation_header(client):
    resp = client.get("/health/live", headers={"x-request-id": "corr-123"})
    assert resp.headers["x-request-id"] == "corr-123"


# ------------------------------------------------------------------ auth flows

REGISTER = {"email": "new@example.com", "password": "hunter2hunter2", "name": "New Student"}


def test_register_login_me_roundtrip(client):
    registered = client.post("/auth/register", json=REGISTER)
    assert registered.status_code == 201
    body = registered.json()
    assert body["user"]["role"] == "student"
    assert "password_hash" not in body["user"]
    assert body["token_type"] == "bearer"

    me = client.get("/auth/me", headers={
        "Authorization": f"Bearer {body['access_token']}"})
    assert me.status_code == 200
    assert me.json()["user"]["email"] == "new@example.com"


def test_register_rejects_duplicate_email(client):
    assert client.post("/auth/register", json=REGISTER).status_code == 201
    conflict = client.post("/auth/register", json={**REGISTER, "name": "Again"})
    assert conflict.status_code == 409
    assert conflict.json()["detail"] == "email already registered"


@pytest.mark.parametrize("payload", [
    {"email": "not-an-email", "password": "hunter2hunter2"},
    {"email": "no-password@example.com"},
    {"email": "short-pw@example.com", "password": "short"},
])
def test_register_validation_rejects_malformed_payloads(client, payload):
    resp = client.post("/auth/register", json=payload)
    assert resp.status_code == 422


def test_login_rejects_unknown_email_and_wrong_password(client):
    client.post("/auth/register", json=REGISTER)
    unknown = client.post("/auth/login",
                          json={"email": "ghost@example.com", "password": "hunter2hunter2"})
    wrong = client.post("/auth/login",
                        json={"email": "new@example.com", "password": "wrong-password"})
    assert unknown.status_code == 401 and wrong.status_code == 401
    assert wrong.json()["detail"] == "invalid email or password"


def test_refresh_issues_new_usable_tokens(client):
    tokens = client.post("/auth/register", json=REGISTER).json()
    refreshed = client.post("/auth/refresh",
                            json={"refresh_token": tokens["refresh_token"]})
    assert refreshed.status_code == 200
    fresh = refreshed.json()
    assert fresh["access_token"] != tokens["access_token"]
    me = client.get("/auth/me", headers={
        "Authorization": f"Bearer {fresh['access_token']}"})
    assert me.status_code == 200


def test_refresh_rejects_access_tokens_and_garbage(client):
    tokens = client.post("/auth/register", json=REGISTER).json()
    as_refresh = client.post("/auth/refresh",
                             json={"refresh_token": tokens["access_token"]})
    garbage = client.post("/auth/refresh", json={"refresh_token": "not.a.token"})
    assert as_refresh.status_code == 401
    assert garbage.status_code == 401


def test_me_requires_authentication(client):
    assert client.get("/auth/me").status_code == 401


def test_admin_can_create_teacher_accounts(client):
    created = client.post("/admin/users", headers=auth(), json={
        "email": "teacher2@example.com", "password": "hunter2hunter2",
        "name": "Second Teacher", "role": "teacher"})
    assert created.status_code == 201
    login = client.post("/auth/login", json={
        "email": "teacher2@example.com", "password": "hunter2hunter2"})
    assert login.json()["user"]["role"] == "teacher"
    # And that teacher can author quizzes.
    publish = client.post("/exam", headers={
        "Authorization": f"Bearer {login.json()['access_token']}"},
        json=make_payload(title="From Teacher Two"))
    assert publish.status_code == 201


def test_admin_user_creation_requires_admin_token_and_valid_role(client):
    assert client.post("/admin/users", json={
        "email": "x@example.com", "password": "hunter2hunter2"}).status_code == 403
    bad_role = client.post("/admin/users", headers=auth(), json={
        "email": "x@example.com", "password": "hunter2hunter2", "role": "wizard"})
    assert bad_role.status_code == 422
    # Register the email first, then expect the admin route to conflict too.
    client.post("/auth/register", json=REGISTER)
    duplicate = client.post("/admin/users", headers=auth(), json={
        "email": REGISTER["email"], "password": "hunter2hunter2"})
    assert duplicate.status_code == 409


# --------------------------------------------------------- auth failure paths

def test_register_data_layer_failure_maps_to_503(client, monkeypatch):
    def boom(*args, **kwargs):
        raise RuntimeError("table gone")

    monkeypatch.setattr(main.auth, "create_user", boom)
    resp = client.post("/auth/register", json=REGISTER)
    assert resp.status_code == 503
    assert resp.json()["detail"] == "data layer unavailable"


def test_login_data_layer_failure_maps_to_503(client, monkeypatch):
    def boom(*args, **kwargs):
        raise RuntimeError("table gone")

    monkeypatch.setattr(main.auth, "authenticate", boom)
    resp = client.post("/auth/login", json={"email": REGISTER["email"],
                                            "password": "hunter2hunter2"})
    assert resp.status_code == 503
    assert resp.json()["detail"] == "data layer unavailable"


def test_refresh_after_account_deletion_is_401(client):
    tokens = client.post("/auth/register", json=REGISTER).json()
    user_id = tokens["user"]["user_id"]
    import db

    db.table().delete_item(Key={"pk": f"USER#{user_id}", "sk": "PROFILE"})
    resp = client.post("/auth/refresh", json={"refresh_token": tokens["refresh_token"]})
    assert resp.status_code == 401
    assert "no longer exists" in resp.json()["detail"]


def test_bootstrap_teacher_seeded_from_settings(make_table, tmp_path, monkeypatch,
                                                capsys):
    make_table("continuum")
    import config

    monkeypatch.setattr(config.settings, "bootstrap_teacher_email",
                        "seeded.teacher@example.com")
    monkeypatch.setattr(config.settings, "bootstrap_teacher_password",
                        "hunter2hunter2")
    config.settings.role_file = str(tmp_path / "role")
    with TestClient(main.app) as client:
        out = capsys.readouterr().out
        assert "bootstrap_teacher_seeded" in out
        login = client.post("/auth/login", json={
            "email": "seeded.teacher@example.com", "password": "hunter2hunter2"})
        assert login.status_code == 200
        assert login.json()["user"]["role"] == "teacher"
    # A second boot finds the account and stays quiet about seeding.
    with TestClient(main.app) as client:
        assert "bootstrap_teacher_seeded" not in capsys.readouterr().out
