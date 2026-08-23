"""Exam scheduling windows, timed attempts, results and management."""
from datetime import datetime, timedelta, timezone

import pytest

import db
from config import settings

EXAM = "cs101-midterm"


def auth():
    return {"x-admin-token": settings.admin_token}


def iso(offset_seconds):
    return (datetime.now(timezone.utc) + timedelta(seconds=offset_seconds)).isoformat()


@pytest.fixture()
def client(make_table, tmp_path):
    make_table("continuum")
    from pathlib import Path

    settings.role_file = str(tmp_path / "role")
    from fastapi.testclient import TestClient
    import main

    with TestClient(main.app) as c:
        yield c


def student_headers(client, email="rules-student@example.com"):
    resp = client.post("/auth/register", json={
        "email": email, "password": "hunter2hunter2", "name": "Rules Student"})
    return {"Authorization": f"Bearer {resp.json()['access_token']}"}


def teacher_headers(client):
    client.post("/admin/users", headers=auth(), json={
        "email": "rules-teach@example.com", "password": "hunter2hunter2",
        "name": "Rules Teacher", "role": "teacher"})
    tokens = client.post("/auth/login", json={
        "email": "rules-teach@example.com", "password": "hunter2hunter2"}).json()
    return {"Authorization": f"Bearer {tokens['access_token']}"}


# ------------------------------------------------------------------- windows

def test_student_blocked_before_window_opens(client):
    headers = student_headers(client)
    created = client.post("/exam", headers=auth(), json={
        "title": "Future Quiz",
        "questions": [{"question": "q?", "options": ["a", "b"], "answer": 0}],
        "opens_at": iso(3600), "closes_at": iso(7200)})
    assert created.status_code == 201
    body = created.json()
    assert "opens_at" in body and "duration" not in body

    blocked = client.get("/exam/future-quiz", headers=headers)
    assert blocked.status_code == 403
    detail = blocked.json()["detail"]
    assert detail["code"] == "EXAM_NOT_OPEN"
    assert "not open yet" in detail["message"]


def test_student_blocked_after_window_closes(client):
    headers = student_headers(client)
    client.post("/exam", headers=auth(), json={
        "title": "Past Quiz",
        "questions": [{"question": "q?", "options": ["a", "b"], "answer": 0}],
        "opens_at": iso(-7200), "closes_at": iso(-60)})
    blocked = client.get("/exam/past-quiz", headers=headers)
    assert blocked.status_code == 403
    assert blocked.json()["detail"]["code"] == "EXAM_CLOSED"


def test_submit_rejects_closed_window_with_structured_detail(client):
    headers = student_headers(client)
    client.post("/exam", headers=auth(), json={
        "title": "Closed Submit",
        "questions": [{"question": "q?", "options": ["a", "b"], "answer": 0}],
        "closes_at": iso(-30)})
    resp = client.post("/exam/closed-submit/submit", headers=headers,
                       json={"answers": {"0": 0}})
    assert resp.status_code == 403
    assert resp.json()["detail"]["code"] == "EXAM_CLOSED"


def test_teacher_bypasses_window_for_preview(client):
    headers = teacher_headers(client)
    open_view = client.get("/exam/cs101-midterm", headers=headers)
    assert open_view.status_code == 200
    assert "attempt_started_at" not in open_view.json()

    client.post("/exam", headers=auth(), json={
        "title": "Staff Preview",
        "questions": [{"question": "q?", "options": ["a", "b"], "answer": 0}],
        "opens_at": iso(3600)})
    preview = client.get("/exam/staff-preview", headers=headers)
    assert preview.status_code == 200


def test_corrupt_window_timestamps_are_tolerated(client):
    """A malformed stored boundary must not crash serving."""
    table = None  # noqa: F841 - fixture wiring reads clearly below
    del table
    headers = student_headers(client)
    client.post("/exam", headers=auth(), json={"title": "Broken Window",
                                               "questions": [
                                                   {"question": "q?",
                                                    "options": ["a", "b"], "answer": 0}]})
    import boto3

    boto3.resource("dynamodb", region_name=settings.region).Table("continuum").update_item(
        Key={"pk": "EXAM#broken-window", "sk": "META"},
        UpdateExpression="SET opens_at = :bad",
        ExpressionAttributeValues={":bad": "not-a-date"})
    resp = client.get("/exam/broken-window", headers=headers)
    assert resp.status_code == 200


def test_create_exam_validates_window_ordering(client):
    resp = client.post("/exam", headers=auth(), json={
        "title": "Backwards",
        "questions": [{"question": "q?", "options": ["a", "b"], "answer": 0}],
        "opens_at": iso(3600), "closes_at": iso(60)})
    assert resp.status_code == 422


def test_create_exam_rejects_nonsense_duration(client):
    resp = client.post("/exam", headers=auth(), json={
        "title": "Zero Minutes",
        "questions": [{"question": "q?", "options": ["a", "b"], "answer": 0}],
        "duration_minutes": 0})
    assert resp.status_code == 422


# ------------------------------------------------------------------- attempts

def test_fetch_starts_attempt_clock_and_reports_deadline(client):
    headers = student_headers(client)
    client.post("/exam", headers=auth(), json={
        "title": "Timed Quiz",
        "questions": [{"question": "q?", "options": ["a", "b"], "answer": 0}],
        "duration_minutes": 45})
    first = client.get("/exam/timed-quiz", headers=headers).json()
    second = client.get("/exam/timed-quiz", headers=headers).json()
    assert first["attempt_started_at"] == second["attempt_started_at"]
    expected = first["attempt_started_at"] + 45 * 60_000
    assert first["deadline_ms"] == expected == second["deadline_ms"]


def test_untimed_exams_have_no_deadline(client):
    headers = student_headers(client)
    body = client.get("/exam/cs101-midterm", headers=headers).json()
    assert "deadline_ms" not in body


def test_late_submission_past_duration_is_rejected(client):
    headers = student_headers(client)
    client.post("/exam", headers=auth(), json={
        "title": "Speed Quiz",
        "questions": [{"question": "q?", "options": ["a", "b"], "answer": 0}],
        "duration_minutes": 5})
    client.get("/exam/speed-quiz", headers=headers)  # starts the clock
    # Age the attempt far beyond its five-minute budget.
    import boto3

    table = boto3.resource("dynamodb", region_name=settings.region).Table("continuum")
    table.update_item(
        Key={"pk": "ATTEMPT#speed-quiz", "sk": f"STUDENT#{__user_id(client, headers)}"},
        UpdateExpression="SET started_at = :old",
        ExpressionAttributeValues={":old": int((datetime.now(timezone.utc).timestamp()
                                                - 3600) * 1000)})
    resp = client.post("/exam/speed-quiz/submit", headers=headers,
                       json={"answers": {"0": 0}})
    assert resp.status_code == 403
    assert resp.json()["detail"]["code"] == "ATTEMPT_EXPIRED"


def __user_id(client, headers):
    me = client.get("/auth/me", headers=headers).json()
    return me["user"]["user_id"]


def test_grace_period_allows_in_flight_submissions(client):
    """Submissions within the 5s grace band past the deadline still land."""
    headers = student_headers(client)
    client.post("/exam", headers=auth(), json={
        "title": "Grace Quiz",
        "questions": [{"question": "q?", "options": ["a", "b"], "answer": 0}],
        "duration_minutes": 1})
    client.get("/exam/grace-quiz", headers=headers)
    import boto3

    table = boto3.resource("dynamodb", region_name=settings.region).Table("continuum")
    sk = f"STUDENT#{__user_id(client, headers)}"
    just_past = int((datetime.now(timezone.utc).timestamp() - 59) * 1000)  # ~1s past
    table.update_item(Key={"pk": "ATTEMPT#grace-quiz", "sk": sk},
                      UpdateExpression="SET started_at = :t",
                      ExpressionAttributeValues={":t": just_past})
    resp = client.post("/exam/grace-quiz/submit", headers=headers,
                       json={"answers": {"0": 0}})
    assert resp.status_code == 200
    assert resp.json()["score"] == 1


def test_attempt_start_race_returns_winner_start_time(client):
    headers = student_headers(client)
    client.post("/exam", headers=auth(), json={
        "title": "Race Quiz",
        "questions": [{"question": "q?", "options": ["a", "b"], "answer": 0}],
        "duration_minutes": 10})
    first = client.get("/exam/race-quiz", headers=headers).json()["attempt_started_at"]
    user_id = __user_id(client, headers)

    def conflicting_put(**kwargs):
        raise db.table().meta.client.exceptions.ClientError(
            {"Error": {"Code": "ConditionalCheckFailed", "Message": "exists"}},
            "PutItem")

    original = db.table().put_item
    db.table().put_item = conflicting_put
    try:
        retry = db.record_attempt_start("race-quiz", user_id)
    finally:
        db.table().put_item = original
    assert retry["started_at"] == first


def test_attempt_start_reraises_non_race_client_errors(client):
    def throttled(**kwargs):
        raise db.table().meta.client.exceptions.ClientError(
            {"Error": {"Code": "ProvisionedThroughputExceededException",
                       "Message": "slow down"}}, "PutItem")

    original = db.table().put_item
    db.table().put_item = throttled
    try:
        with pytest.raises(db.table().meta.client.exceptions.ClientError):
            db.record_attempt_start("any-exam", "any-student")
    finally:
        db.table().put_item = original


def test_attempt_start_returns_fresh_clock_if_winner_row_invisible(client):
    """Eventual consistency edge: race lost AND winner row not yet visible."""
    def conflicting_put(**kwargs):
        raise db.table().meta.client.exceptions.ClientError(
            {"Error": {"Code": "ConditionalCheckFailed", "Message": "exists"}},
            "PutItem")

    original_put = db.table().put_item
    original_get = db.table().get_item

    def empty_get(**kwargs):
        return {}

    db.table().put_item = conflicting_put
    db.table().get_item = empty_get
    try:
        fallback = db.record_attempt_start("ghost-attempt-exam", "student-x")
        assert "started_at" in fallback
    finally:
        db.table().put_item = original_put
        db.table().get_item = original_get


# ------------------------------------------------------- submission edge cases

def test_submit_unknown_exam_is_404(client):
    headers = student_headers(client)
    resp = client.post("/exam/ghost-quiz/submit", headers=headers,
                       json={"answers": {"0": 0}})
    assert resp.status_code == 404
    assert resp.json()["detail"] == "exam not found"


def test_attempt_deadline_handles_missing_exam_and_attempt(client):
    assert db.attempt_deadline("never-created", "student-x") == (None, None)
    client.post("/exam", headers=auth(), json={
        "title": "Untimed Deadline Probe",
        "questions": [{"question": "q?", "options": ["a", "b"], "answer": 0}]})
    assert db.attempt_deadline("untimed-deadline-probe", "student-x") == (None, None)


def test_submit_timed_exam_without_prior_fetch_still_commits(client):
    """No fetch yet means no attempt clock; the submission is simply accepted."""
    headers = student_headers(client)
    client.post("/exam", headers=auth(), json={
        "title": "Cold Start Quiz",
        "questions": [{"question": "q?", "options": ["a", "b"], "answer": 1}],
        "duration_minutes": 15})
    resp = client.post("/exam/cold-start-quiz/submit", headers=headers,
                       json={"answers": {"0": 1}})
    assert resp.status_code == 200
    assert resp.json()["status"] == "committed"


def test_result_review_tolerates_missing_exam_definition(client, monkeypatch):
    headers = student_headers(client)
    client.post(f"/exam/{EXAM}/submit", headers=headers,
                json={"answers": {"0": 1}})

    def gone(exam_id, include_answers=False):
        raise RuntimeError("definition vanished")

    monkeypatch.setattr(main_module().db, "get_exam", gone)
    result = client.get(f"/exam/{EXAM}/result", headers=headers)
    body = result.json()
    assert body["score"] == 1 and body["review"] == []


# -------------------------------------------------------------------- results

def test_result_endpoint_reports_score_and_review(client):
    headers = student_headers(client)
    miss = client.get("/exam/cs101-midterm/result", headers=headers)
    assert miss.status_code == 404
    assert miss.json()["detail"] == "no submission for this exam"

    client.post(f"/exam/{EXAM}/submit", headers=headers,
                json={"answers": {"0": 1, "1": 0}})
    result = client.get(f"/exam/{EXAM}/result", headers=headers).json()
    # Seed key: q0→B(1), q1→A(0) — both choices are correct here.
    assert (result["score"], result["total"]) == (2, 5)
    assert len(result["review"]) == 5
    q0, q1 = result["review"][0], result["review"][1]
    assert q0["was_correct"] is True and q0["correct_answer"] == 1
    assert q1["was_correct"] is True and q1["chosen"] == 0
    unanswered = result["review"][4]
    assert unanswered["chosen"] is None and unanswered["was_correct"] is False


# ------------------------------------------------------------- exam management

def make_payload(**overrides):
    payload = {"title": "Manage Me", "questions": [
        {"question": "q?", "options": ["a", "b"], "answer": 0}]}
    payload.update(overrides)
    return payload


def test_patch_exam_updates_title_and_window(client):
    teacher = teacher_headers(client)
    client.post("/exam", headers=teacher, json=make_payload())
    patched = client.patch("/exam/manage-me", headers=teacher, json={
        "title": "Renamed Quiz", "duration_minutes": 30})
    assert patched.status_code == 200
    body = patched.json()
    assert body["title"] == "Renamed Quiz"
    assert body["duration_minutes"] == 30
    listed = [e for e in client.get("/exams").json() if e["id"] == "manage-me"]
    assert listed[0]["title"] == "Renamed Quiz"


def test_patch_exam_requires_staff_and_existing_exam(client):
    teacher = teacher_headers(client)
    assert client.patch("/exam/nope-exam", headers=teacher,
                        json={"title": "X"}).status_code == 404
    student = student_headers(client)
    assert client.patch("/exam/manage-me", headers=student,
                        json={"title": "Hacked"}).status_code == 403


def test_patch_with_empty_payload_returns_current_metadata(client):
    teacher = teacher_headers(client)
    client.post("/exam", headers=teacher, json=make_payload())
    untouched = client.patch("/exam/manage-me", headers=teacher, json={})
    assert untouched.status_code == 200
    body = untouched.json()
    assert body["title"] == "Manage Me"
    assert "duration_minutes" not in body


def test_close_exam_sets_closes_at(client):
    teacher = teacher_headers(client)
    client.post("/exam", headers=teacher, json=make_payload())
    closed = client.post("/exam/manage-me/close", headers=teacher)
    assert closed.status_code == 200
    assert "closed_at" in closed.json()
    student = student_headers(client)
    blocked = client.get("/exam/manage-me", headers=student)
    assert blocked.status_code == 403


def test_close_missing_exam_is_404(client):
    teacher = teacher_headers(client)
    assert client.post("/exam/ghost-exam/close", headers=teacher).status_code == 404


def test_delete_exam_removes_definition_but_keeps_submissions(client):
    teacher = teacher_headers(client)
    client.post("/exam", headers=teacher, json=make_payload(title="Doomed"))
    student = student_headers(client)
    client.get("/exam/doomed", headers=student)
    client.post("/exam/doomed/submit", headers=student, json={"answers": {"0": 0}})

    removed = client.delete("/exam/doomed", headers=teacher)
    assert removed.status_code == 204
    assert client.delete("/exam/doomed", headers=teacher).status_code == 404

    listed = {e["id"] for e in client.get("/exams").json()}
    assert "doomed" not in listed
    submissions = client.get("/admin/submissions", headers=auth()).json()
    assert any(row["pk"] == "SUBMISSION#doomed" for row in submissions)


def test_delete_unknown_exam_is_404(client):
    teacher = teacher_headers(client)
    assert client.delete("/exam/never-was", headers=teacher).status_code == 404


# ------------------------------------------------------------ teacher results

def test_teacher_results_include_scores_and_average(client):
    teacher = teacher_headers(client)
    empty = client.get("/admin/exams/cs101-midterm/results", headers=teacher).json()
    assert empty["count"] == 0 and empty["average_pct"] is None

    for email, answers in (("r-one@example.com", {"0": 1}),
                           ("r-two@example.com", {"0": 0})):
        student_headers(client, email)
        # re-register to grab distinct tokens per student
        login = client.post("/auth/login", json={
            "email": email, "password": "hunter2hunter2"})
        token = {"Authorization": f"Bearer {login.json()['access_token']}"}
        client.post(f"/exam/{EXAM}/submit", headers=token, json={"answers": answers})

    results = client.get("/admin/exams/cs101-midterm/results", headers=teacher).json()
    assert results["count"] == 2
    # r-one scored 1/5 (20%), r-two 0/5 (0%) → average 10%.
    assert results["average_pct"] == 10.0
    names = {row["student_name"] for row in results["submissions"]}
    assert "Rules Student" in names
    scores = sorted(row["score"] for row in results["submissions"])
    assert scores == [0, 1]


def test_results_endpoint_rejects_students(client):
    student = student_headers(client)
    resp = client.get("/admin/exams/cs101-midterm/results", headers=student)
    assert resp.status_code == 403


# --------------------------------------------------------------------- outbox

def test_outbox_lists_pending_journal_entries(client):
    client.post(f"/exam/{EXAM}/submit", headers=student_headers(client),
                json={"answers": {"0": 1}, "request_id": "outbox-check"})
    entries = client.get("/admin/outbox", headers=auth()).json()
    assert any(entry["request_id"] == "outbox-check" for entry in entries)
    entry = next(e for e in entries if e["request_id"] == "outbox-check")
    assert entry["student_name"] == "Rules Student"
    assert entry["target_pk"] == f"SUBMISSION#{EXAM}"


def test_outbox_requires_admin_token(client):
    assert client.get("/admin/outbox").status_code == 403


def test_outbox_limit_is_clamped(client, monkeypatch):
    seen = {}

    def fake_list(limit):
        seen["limit"] = limit
        return []

    monkeypatch.setattr(main_module().db, "list_outbox", fake_list)
    client_get = client.get
    assert client_get("/admin/outbox?limit=99999", headers=auth()).status_code == 200
    assert seen["limit"] == 200
    client_get("/admin/outbox?limit=-5", headers=auth())
    assert seen["limit"] == 1


def main_module():
    import main

    return main
