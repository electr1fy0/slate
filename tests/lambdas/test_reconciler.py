import json
import time as _time

import pytest

TABLE = "continuum"
ENV = {"TABLE_NAME": TABLE, "LOOKBACK_MINUTES": "30", "METRIC_NAMESPACE": "Continuum"}


def journal_entry(rid="r1", version=1, ts=None, answers=None):
    return {
        "pk": f"JOURNAL#{rid}", "sk": "OUTBOX",
        "target_pk": "SUBMISSION#e1", "target_sk": f"STUDENT#{rid}",
        "version": version, "request_id": rid,
        "created_at": ts if ts is not None else int(_time.time() * 1000),
        "answers": answers if answers is not None else {"0": 1},
    }


def put_journal(table, *entries):
    for entry in entries:
        table.put_item(Item=entry)


@pytest.fixture()
def rec(lambda_loader, make_table):
    make_table(TABLE)
    return lambda_loader("reconciler", ENV)


def test_empty_journal_produces_zero_summary_and_metrics(rec):
    out = rec.handler({}, None)
    assert out == {"scanned": 0, "repaired": 0, "already_replicated": 0,
                   "conflicts": [], "repaired_ids": []}
    names = {m["MetricName"] for m in
             rec.cloudwatch.list_metrics(Namespace="Continuum")["Metrics"]}
    assert {"ReconcilerRepaired", "ReconcilerScanned"} <= names


def test_already_replicated_entries_are_skipped(rec, make_table):
    entry = journal_entry("r1")
    put_journal(rec.dynamodb, entry)
    rec.dynamodb.put_item(Item={"pk": "SUBMISSION#e1", "sk": "STUDENT#r1",
                                "request_id": "r1", "version": 1})
    out = rec.handler({}, None)
    assert out["scanned"] == 1
    assert out["already_replicated"] == 1
    assert out["repaired"] == 0
    assert out["conflicts"] == []


def test_newer_version_present_is_a_conflict(rec):
    put_journal(rec.dynamodb, journal_entry("r2", version=2))
    rec.dynamodb.put_item(Item={"pk": "SUBMISSION#e1", "sk": "STUDENT#r2",
                                "request_id": "older-rid", "version": 5})
    out = rec.handler({}, None)
    assert out["conflicts"] == [{"request_id": "r2", "reason": "newer version present",
                                 "journal_version": 2, "current_version": 5}]


def test_missing_target_is_repaired_with_condition(rec, monkeypatch):
    monkeypatch.setenv("AWS_REGION", "ap-southeast-1")
    entry = journal_entry("r3", version=3)
    put_journal(rec.dynamodb, entry)
    out = rec.handler({}, None)
    assert out["repaired_ids"] == ["r3"]
    stored = rec.dynamodb.get_item(Key={"pk": "SUBMISSION#e1",
                                        "sk": "STUDENT#r3"})["Item"]
    assert stored["version"] == 3
    assert stored["reconciled"] is True
    assert stored["written_by_region"] == "ap-southeast-1"
    assert stored["answers"] == {"0": 1}
    assert stored["updated_at"] == entry["created_at"]


def test_lost_race_on_conditional_write_is_recorded(rec):
    exc_cls = rec.dynamodb.meta.client.exceptions.ConditionalCheckFailedException

    class RacyTable:
        def __init__(self, real):
            self._real = real
            self.meta = real.meta

        def scan(self, **kwargs):
            return self._real.scan(**kwargs)

        def get_item(self, **kwargs):
            return self._real.get_item(**kwargs)

        def put_item(self, **kwargs):
            raise exc_cls({"Error": {"Code": "ConditionalCheckFailed"}}, "PutItem")

    put_journal(rec.dynamodb, journal_entry("r4"))
    original = rec.dynamodb
    rec.dynamodb = RacyTable(original)
    out = rec.handler({}, None)
    assert out["conflicts"] == [{"request_id": "r4", "reason": "lost race"}]
    assert out["repaired"] == 0


def test_scan_pagination_consumes_all_pages(rec):
    class PagingTable:
        calls = 0

        def __init__(self, real, pages):
            self._real = real
            self.pages = list(pages)

        def scan(self, **kwargs):
            PagingTable.calls += 1
            return self.pages.pop(0)

        def get_item(self, **kwargs):
            return {"Item": None}

        def put_item(self, **kwargs):
            return self._real.put_item(**kwargs)

    page1 = {"Items": [journal_entry("p1")], "LastEvaluatedKey": {"pk": "JOURNAL#p1"}}
    page2 = {"Items": [journal_entry("p2", version=9)]}
    rec.dynamodb = PagingTable(rec.dynamodb, [page1, page2])
    out = rec.handler({}, None)
    assert PagingTable.calls == 2
    assert out["scanned"] == 2


def test_old_journal_entries_outside_lookback_are_ignored(rec):
    stale_ts = int((_time.time() - 3600) * 1000)
    rec.dynamodb.put_item(Item=journal_entry("ancient", ts=stale_ts))
    out = rec.handler({}, None)
    assert out["scanned"] == 0


def test_handler_prints_json_summary(rec, capsys):
    out = rec.handler({}, None)
    printed = json.loads(capsys.readouterr().out.strip())
    assert printed == out
