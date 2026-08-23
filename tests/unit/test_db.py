import types

import pytest
from boto3.dynamodb.conditions import Attr

import db
from config import settings

QUESTIONS = [
    {"question": "q1", "options": ["a", "b"], "answer": 0},
    {"question": "q2", "options": ["c"], "answer": 1},
]


@pytest.fixture()
def table(make_table):
    return make_table("continuum")


def test_now_and_iso_helpers():
    assert isinstance(db.now_ms(), int)
    assert "T" in db.iso_now()


def test_table_is_cached_after_first_build(table):
    assert db.table() is db.table()


def test_table_honours_endpoint_template(monkeypatch):
    seen = {}

    class FakeResource:
        def __init__(self, *args, **kwargs):
            seen.update(kwargs)

        def Table(self, name):
            return ("table", name)

    monkeypatch.setattr(db.boto3, "resource", FakeResource)
    monkeypatch.setattr(settings, "ddb_endpoint_mode", "http://localhost:{region}:8000")
    result = db.table()
    assert seen["endpoint_url"] == f"http://localhost:{settings.region}:8000"
    assert seen["region_name"] == settings.region
    assert result == ("table", settings.table_name)


def test_exam_roundtrip(table):
    db.ensure_exam("x1", "Exam One", QUESTIONS)
    meta = db.get_exam("x1")
    assert meta["title"] == "Exam One"
    assert meta["question_count"] == 2
    assert [q["index"] for q in meta["questions"]] == [0, 1]
    assert [q["options"] for q in meta["questions"]] == [["a", "b"], ["c"]]
    assert all("answer" not in q for q in meta["questions"])


def test_get_exam_missing_returns_none(table):
    assert db.get_exam("nope") is None


def test_submit_new_attempt_commits_with_version_one(table):
    result = db.submit_attempt("x1", "s1", "Stu One", {"0": 1}, None)
    assert result["status"] == "committed"
    assert result["version"] == 1
    assert len(result["request_id"]) == 36
    stored = table.get_item(Key={"pk": "SUBMISSION#x1", "sk": "STUDENT#s1"})["Item"]
    assert stored["student_name"] == "Stu One"


def test_submit_same_request_id_returns_duplicate(table):
    first = db.submit_attempt("x1", "s1", "Stu One", {"0": 1}, "req-1")
    again = db.submit_attempt("x1", "s1", "Stu One", {"0": 9}, "req-1")
    assert again["status"] == "duplicate"
    assert again["version"] == first["version"]
    # original answers preserved; duplicate reports the first commit timestamp
    stored = table.get_item(Key={"pk": "SUBMISSION#x1", "sk": "STUDENT#s1"})["Item"]
    assert stored["answers"] == {"0": 1}
    assert again["committed_at"] == stored["updated_at"]


def test_submit_second_attempt_conflicts_without_new_journal(table):
    db.submit_attempt("x1", "s1", "Stu One", {"0": 1}, "req-1")
    with pytest.raises(db.SubmissionConflict):
        db.submit_attempt("x1", "s1", "Stu One", {"1": 0}, "req-2")
    stored = table.get_item(Key={"pk": "SUBMISSION#x1", "sk": "STUDENT#s1"})["Item"]
    assert stored["version"] == 1
    journal = table.scan(FilterExpression=Attr("pk").begins_with("JOURNAL#"))["Items"]
    assert {j["request_id"] for j in journal} == {"req-1"}
    assert all(j["sk"] == "OUTBOX" for j in journal)
    assert all("ttl" in j for j in journal)


class StubTable:
    def __init__(self, read_back=True):
        self.read_back = read_back
        self.puts = []
        self.deleted = []

    def put_item(self, Item):
        self.puts.append(Item)

    def get_item(self, Key):
        return {"Item": dict(Key, ts=1)} if self.read_back else {}

    def delete_item(self, Key):
        self.deleted.append(Key)


def test_heartbeat_probe_roundtrip_cleans_up(monkeypatch, table):
    stub = StubTable(read_back=True)
    monkeypatch.setattr(db, "table", lambda: stub)
    out = db.heartbeat_probe(settings.region)
    assert out["ok"] is True
    assert out["latency_ms"] >= 0
    probe = stub.puts[0]
    assert probe["pk"] == "HEARTBEAT"
    assert probe["sk"].startswith(f"PROBE#{settings.region}#")
    assert stub.deleted == [{"pk": "HEARTBEAT", "sk": probe["sk"]}]


def test_heartbeat_probe_read_failure_skips_delete(monkeypatch, table):
    stub = StubTable(read_back=False)
    monkeypatch.setattr(db, "table", lambda: stub)
    out = db.heartbeat_probe("r2")
    assert out["ok"] is False
    assert stub.deleted == []


def test_latest_replicated_ts_returns_newest_probe_ts(table):
    ts = db.now_ms()
    table.put_item(Item={"pk": "HEARTBEAT", "sk": "PROBE#ap-south-1#001", "ts": ts - 50})
    table.put_item(Item={"pk": "HEARTBEAT", "sk": "PROBE#ap-south-1#002", "ts": ts})
    table.put_item(Item={"pk": "HEARTBEAT", "sk": "PROBE#other-region#003", "ts": ts + 999})
    assert db.latest_replicated_ts("ap-south-1") == ts


def test_latest_replicated_ts_no_probes_means_zero(table):
    assert db.latest_replicated_ts("nowhere") == 0


def test_list_submissions_scans_when_no_exam_given(table):
    for i in range(3):
        table.put_item(Item={"pk": "SUBMISSION#e1", "sk": f"S{i}", "v": i})
    table.put_item(Item={"pk": "OTHER", "sk": "X"})
    items = db.list_submissions(limit=10)
    assert len(items) == 3
    assert all(i["pk"].startswith("SUBMISSION#") for i in items)


def test_list_submissions_queries_specific_exam(table):
    for i in range(4):
        table.put_item(Item={"pk": "SUBMISSION#e1", "sk": f"S{i}", "v": i})
    items = db.list_submissions(exam_id="e1", limit=2)
    assert len(items) == 2


def test_stub_helper_smoke():
    assert StubTable().read_back is True


# ------------------------------------------------------------------ exam listing

def test_list_exams_empty_table_returns_empty_list(table):
    assert db.list_exams() == []


def test_list_exams_returns_meta_sorted_by_creation(table):
    db.ensure_exam("zulu", "Zulu Quiz", QUESTIONS)
    db.ensure_exam("alpha", "Alpha Quiz", QUESTIONS)
    exams = db.list_exams()
    assert [e["id"] for e in exams] == ["zulu", "alpha"]
    assert all(set(e) == {"id", "title", "question_count", "created_at"} for e in exams)
    assert all(e["question_count"] == 2 for e in exams)


def test_list_exams_ignores_non_meta_items(table):
    db.ensure_exam("e1", "Exam", QUESTIONS)
    table.put_item(Item={"pk": "EXAM#e1", "sk": "QUESTION#000",
                         "question": "q", "options": ["a"], "answer": 0})
    table.put_item(Item={"pk": "SUBMISSION#e1", "sk": "STUDENT#s1"})
    exams = db.list_exams()
    assert [e["id"] for e in exams] == ["e1"]


def test_list_submissions_scan_respects_limit(table):
    for i in range(5):
        table.put_item(Item={"pk": f"SUBMISSION#e{i}", "sk": "S"})
    assert len(db.list_submissions(limit=3)) == 3


# --------------------------------------------------------------------- grading

def test_grade_attempt_scores_correct_answers(table):
    db.ensure_exam("g1", "Graded", [
        {"question": "q1", "options": ["a", "b"], "answer": 1},
        {"question": "q2", "options": ["a", "b"], "answer": 0},
    ])
    result = db.grade_attempt("g1", {"0": 1, "1": 0})
    assert result == {"score": 2, "total": 2}


def test_grade_attempt_counts_wrong_answers_as_zero(table):
    db.ensure_exam("g2", "Graded", [{"question": "q1", "options": ["a", "b"], "answer": 1}])
    assert db.grade_attempt("g2", {"0": 0}) == {"score": 0, "total": 1}


def test_grade_attempt_ignores_unknown_and_malformed_question_indexes(table):
    db.ensure_exam("g3", "Graded", [{"question": "q1", "options": ["a", "b"], "answer": 0}])
    result = db.grade_attempt("g3", {"0": 0, "7": 1, "bogus": 0})
    assert result == {"score": 1, "total": 1}


def test_grade_attempt_on_missing_exam_scores_zero_over_zero(table):
    assert db.grade_attempt("ghost", {"0": 0}) == {"score": 0, "total": 0}
