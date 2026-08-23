import argparse
import importlib.util
import io
import json
import runpy
import sys
from pathlib import Path

import pytest

CLI_PATH = Path(__file__).resolve().parents[2] / "chaos" / "continuum_chaos.py"


def load_cli():
    spec = importlib.util.spec_from_file_location("continuum_chaos", CLI_PATH)
    mod = importlib.util.module_from_spec(spec)
    sys.modules["continuum_chaos"] = mod
    spec.loader.exec_module(mod)
    return mod


@pytest.fixture(autouse=True)
def _aws_region(monkeypatch):
    monkeypatch.setenv("AWS_DEFAULT_REGION", "us-east-1")


@pytest.fixture()
def cli():
    mod = load_cli()
    mod.PRIMARY = "10.0.0.1"
    mod.STANDBY = "10.0.1.1"
    yield mod


class FakeResp:
    def __init__(self, status=200, raw=b'{"ok": true}'):
        self.status = status
        self._raw = raw

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    def read(self):
        return self._raw


# ------------------------------------------------------------------ transport

def test_http_get_success(cli, monkeypatch):
    monkeypatch.setattr("urllib.request.urlopen", lambda req, timeout=None: FakeResp(200))
    code, body = cli.http("http://x/status")
    assert (code, body) == (200, {"ok": True})


def test_http_post_encodes_json_payload(cli, monkeypatch):
    captured = {}

    def fake_urlopen(req, timeout=None):
        captured["data"] = req.data
        captured["method"] = req.method
        return FakeResp(201, b'{"status": "committed"}')

    monkeypatch.setattr("urllib.request.urlopen", fake_urlopen)
    code, body = cli.http("http://x/admin/chaos", "POST", {"error_pct": 5})
    assert code == 201
    assert json.loads(captured["data"]) == {"error_pct": 5}
    assert captured["method"] == "POST"
    assert body == {"status": "committed"}


def test_http_empty_body_yields_empty_dict(cli, monkeypatch):
    monkeypatch.setattr("urllib.request.urlopen", lambda req, timeout=None: FakeResp(200, b""))
    assert cli.http("http://x") == (200, {})


def test_http_http_error_branch(cli, monkeypatch):
    import urllib.error

    def boom(req, timeout=None):
        raise urllib.error.HTTPError("http://x", 503, "unavailable", {}, io.BytesIO(b"down"))

    monkeypatch.setattr("urllib.request.urlopen", boom)
    code, body = cli.http("http://x")
    assert code == 503
    assert body == {"error": "down"}


def test_http_generic_error_branch(cli, monkeypatch):
    import urllib.error

    def boom(req, timeout=None):
        raise urllib.error.URLError("no route to host")

    monkeypatch.setattr("urllib.request.urlopen", boom)
    code, body = cli.http("http://x")
    assert code == 0
    assert "no route to host" in body["error"]


def test_target_resolution(cli):
    assert cli.target("primary") == "http://10.0.0.1"
    assert cli.target("standby") == "http://10.0.1.1"
    with pytest.raises(SystemExit, match="unknown target mars"):
        cli.target("mars")


def test_load_targets_reads_ssm(aws):
    import boto3

    ssm = boto3.client("ssm")
    ssm.put_parameter(Name="/continuum/primary/public_ip", Value="1.1.1.1", Type="String")
    ssm.put_parameter(Name="/continuum/standby/public_ip", Value="2.2.2.2", Type="String")
    mod = load_cli()
    assert mod.PRIMARY is None and mod.STANDBY is None
    mod.load_targets()
    assert mod.PRIMARY == "1.1.1.1"
    assert mod.STANDBY == "2.2.2.2"


# --------------------------------------------------------------- app commands

def test_cmd_status_prints_both_nodes(cli, monkeypatch, capsys):
    calls = []

    def fake_http(url, method="GET", payload=None, headers=None, timeout=10):
        calls.append(url)
        return 200, {"healthy": True}

    monkeypatch.setattr(cli, "http", fake_http)
    cli.cmd_status(argparse.Namespace())
    assert len(calls) == 2
    assert "[primary]" in capsys.readouterr().out


def test_cmd_degrade_filters_none_fields(cli, monkeypatch, capsys):
    seen = {}

    def fake_set_chaos(t, payload):
        seen["target"], seen["payload"] = t, payload

    monkeypatch.setattr(cli, "_set_chaos", fake_set_chaos)
    cli.cmd_degrade_cmd(argparse.Namespace(target="standby", error_pct=60, latency_ms=None))
    assert seen == {"target": "http://10.0.1.1", "payload": {"error_pct": 60}}
    assert "degrading standby" in capsys.readouterr().out


def test_cmd_db_stall_on_and_off(cli, monkeypatch):
    payloads = []
    monkeypatch.setattr(cli, "_set_chaos", lambda t, p: payloads.append(p))
    cli.cmd_db_stall(type("A", (), {"on": True, "target": "primary"}))
    cli.cmd_db_stall(type("A", (), {"on": False, "target": "primary"}))
    assert payloads == [{"db_stall": True}, {"db_stall": False}]


def test_cmd_heal_single_and_all(cli, monkeypatch):
    targets = []
    monkeypatch.setattr(cli, "_set_chaos", lambda t, p: targets.append(t))
    cli.cmd_heal(argparse.Namespace(target="primary"))
    cli.cmd_heal(argparse.Namespace(target="all"))
    assert targets == ["http://10.0.0.1", "http://10.0.0.1", "http://10.0.1.1"]


# ----------------------------------------------------------------- ec2 / ssm

def _seed_instance(aws, label):
    import boto3

    ec2 = boto3.client("ec2")
    iid = ec2.run_instances(ImageId="ami-12345678", MinCount=1,
                            MaxCount=1)["Instances"][0]["InstanceId"]
    boto3.client("ssm").put_parameter(
        Name=f"/continuum/{label}/instance_id", Value=iid, Type="String")
    return iid


def test_instance_id_lookup(aws):
    mod = load_cli()
    primary = _seed_instance(aws, "primary")
    standby = _seed_instance(aws, "standby")
    assert mod.instance_id("primary") == primary
    assert mod.instance_id("standby") == standby


def test_stop_and_start_instance_roundtrip(cli, aws, capsys):
    iid = _seed_instance(aws, "primary")
    cli.stop_instance("primary")
    import boto3

    state = boto3.client("ec2").describe_instances(InstanceIds=[iid])
    assert state["Reservations"][0]["Instances"][0]["State"]["Name"] == "stopped"
    cli.start_instance("primary")
    out = capsys.readouterr().out
    assert iid in out
    assert "starting primary" in out


def test_cmd_hard_down_plain(cli, monkeypatch):
    stopped, started = [], []
    monkeypatch.setattr(cli, "stop_instance", lambda lbl: stopped.append(lbl))
    monkeypatch.setattr(cli, "start_instance", lambda lbl: started.append(lbl))
    cli.cmd_hard_down(argparse.Namespace(target="primary", seconds=None))
    assert stopped == ["primary"]
    assert started == []


def test_cmd_hard_down_with_seconds_is_blip(cli, monkeypatch, capsys):
    slept, started = [], []
    monkeypatch.setattr(cli.time, "sleep", lambda s: slept.append(s))
    monkeypatch.setattr(cli, "stop_instance", lambda lbl: None)
    monkeypatch.setattr(cli, "start_instance", lambda lbl: started.append(lbl))
    cli.cmd_hard_down(argparse.Namespace(target="standby", seconds=40))
    assert slept == [40]
    assert started == ["standby"]
    assert "NO failover" in capsys.readouterr().out


def test_cmd_blip_delegates_to_hard_down(cli, monkeypatch):
    seen = {}
    monkeypatch.setattr(cli, "cmd_hard_down",
                        lambda ns: seen.update(target=ns.target, seconds=ns.seconds))
    cli.cmd_blip(argparse.Namespace(target="primary", seconds=15))
    assert seen == {"target": "primary", "seconds": 15}


class FakeSSM:
    def __init__(self, stdout="", stderr="", params=None):
        self.stdout, self.stderr = stdout, stderr
        self.params = params or {}

    def get_parameter(self, Name):
        return {"Parameter": {"Value": "i-fake"}}

    def send_command(self, **kwargs):
        self.last = kwargs
        return {"Command": {"CommandId": "cid-77"}}

    def get_command_invocation(self, CommandId, InstanceId):
        return {"StandardOutputContent": self.stdout,
                "StandardErrorContent": self.stderr}


def _fake_boto3_client(cli, fake_ssm, extra=()):
    def fake_client(service, **kwargs):
        if service == "ssm":
            return fake_ssm
        for candidate in extra:
            if candidate[0] == service:
                return candidate[1]()
        raise AssertionError(f"unexpected service {service}")

    return fake_client


def test_run_ssm_prefers_stdout(cli, monkeypatch, capsys):
    fake = FakeSSM(stdout="active\n")
    monkeypatch.setattr(cli.boto3, "client", _fake_boto3_client(cli, fake))
    cli.run_ssm("standby", ["echo active"], "manual promote")
    out = capsys.readouterr().out.strip()
    assert out == "active"
    assert fake.last["Parameters"]["commands"] == ["echo active"]


def test_run_ssm_falls_back_to_stderr_then_placeholder(cli, monkeypatch, capsys):
    fake = FakeSSM(stderr="script failed")
    monkeypatch.setattr(cli.boto3, "client", _fake_boto3_client(cli, fake))
    cli.run_ssm("primary", ["false"], "c")
    assert "script failed" in capsys.readouterr().out

    silent = FakeSSM()
    monkeypatch.setattr(cli.boto3, "client", _fake_boto3_client(cli, silent))
    cli.run_ssm("primary", ["true"], "c")
    assert "(no output)" in capsys.readouterr().out


def test_cmd_promote_and_demote_issue_role_commands(cli, monkeypatch):
    issued = []
    monkeypatch.setattr(cli, "run_ssm",
                        lambda label, commands, comment: issued.append((label, commands[0])))
    cli.cmd_promote(argparse.Namespace(target="standby"))
    cli.cmd_demote(argparse.Namespace(target="primary"))
    assert "active > /etc/continuum/role" in issued[0][1]
    assert "standby > /etc/continuum/role" in issued[1][1]


# ------------------------------------------------------------------- lambdas

class FakeLambda:
    def __init__(self, functions, result=None):
        self.functions = functions
        self.result = result or {"scanned": 0}

    def list_functions(self):
        return {"Functions": [{"FunctionName": f} for f in self.functions]}

    def get_paginator(self, name):
        pages = [{"Functions": [{"FunctionName": f} for f in self.functions[:1]]},
                 {"Functions": [{"FunctionName": f} for f in self.functions[1:]]}]
        return type("P", (), {"paginate": lambda self, **kw: iter(pages)})()

    def invoke(self, FunctionName, Payload):
        return {"Payload": io.BytesIO(json.dumps(self.result).encode())}


def test_cmd_reconcile_finds_reconciler_across_pages(cli, monkeypatch, capsys):
    lam = FakeLambda(["continuum-verifier", "continuum-reconciler"],
                     result={"scanned": 3, "repaired": 1})
    monkeypatch.setattr(cli.boto3, "client",
                        _fake_boto3_client(cli, FakeSSM(), extra=[("lambda", lambda: lam)]))
    cli.cmd_reconcile(argparse.Namespace())
    out = json.loads(capsys.readouterr().out)
    assert out == {"scanned": 3, "repaired": 1}


def test_cmd_reconcile_missing_lambda_exits(cli, monkeypatch):
    lam = FakeLambda(["other-fn"])
    monkeypatch.setattr(cli.boto3, "client",
                        _fake_boto3_client(cli, FakeSSM(), extra=[("lambda", lambda: lam)]))
    with pytest.raises(SystemExit, match="reconciler lambda not found"):
        cli.cmd_reconcile(argparse.Namespace())


def test_cmd_verify_primary_event(cli, monkeypatch, capsys):
    lam = FakeLambda(["prod-verifier-sfn"], result={"healthy": True})
    monkeypatch.setattr(cli.boto3, "client",
                        _fake_boto3_client(cli, FakeSSM(), extra=[("lambda", lambda: lam)]))
    cli.cmd_verify(argparse.Namespace(target="primary"))
    out = json.loads(capsys.readouterr().out)
    assert out == {"healthy": True}


def test_cmd_verify_standby_includes_url(cli, monkeypatch):
    events = []

    class Recording(FakeLambda):
        def invoke(self, FunctionName, Payload):
            events.append(json.loads(Payload.decode()))
            return super().invoke(FunctionName, Payload)

    lam = Recording(["verifier-sfn"])
    monkeypatch.setattr(cli.boto3, "client",
                        _fake_boto3_client(cli, FakeSSM(), extra=[("lambda", lambda: lam)]))
    cli.cmd_verify(argparse.Namespace(target="standby"))
    assert events[0]["target"] == "standby"
    assert events[0]["target_url"] == "http://10.0.1.1"
    assert events[0]["publish_metric"] is False


def test_cmd_verify_missing_verifier_exits(cli, monkeypatch):
    lam = FakeLambda(["reconciler-only"])
    monkeypatch.setattr(cli.boto3, "client",
                        _fake_boto3_client(cli, FakeSSM(), extra=[("lambda", lambda: lam)]))
    with pytest.raises(SystemExit, match="verifier-sfn lambda not found"):
        cli.cmd_verify(argparse.Namespace(target="primary"))


def test_cmd_submit_defaults_and_targets(cli, monkeypatch, capsys):
    posts = []

    def fake_http(url, method="GET", payload=None, headers=None, timeout=10):
        posts.append((url, method, payload))
        return 200, {"status": "committed"}

    monkeypatch.setattr(cli, "http", fake_http)
    cli.cmd_submit(argparse.Namespace(student="burst1", exam="cs101-midterm", target=None))
    cli.cmd_submit(argparse.Namespace(student="burst2", exam="cs101-midterm", target="standby"))
    assert posts[0][0] == "http://10.0.0.1/exam/cs101-midterm/submit"
    assert posts[1][0].startswith("http://10.0.1.1/")
    assert posts[0][1] == "POST"
    assert posts[0][2]["student_id"] == "burst1"
    assert "committed" in capsys.readouterr().out


# --------------------------------------------------------------------- main

SPY_TARGETS = ("cmd_status", "cmd_degrade_cmd", "cmd_db_stall", "cmd_heal",
               "cmd_hard_down", "cmd_promote", "cmd_demote", "cmd_submit",
               "cmd_reconcile", "cmd_verify")


def run_main(cli, argv, monkeypatch):
    calls = []

    def make_spy(name):
        def spy(args):
            calls.append((name, args))
        return spy

    for name in SPY_TARGETS:
        monkeypatch.setattr(cli, name, make_spy(name))
    monkeypatch.setattr(cli, "load_targets", lambda: None)
    monkeypatch.setattr(sys, "argv", ["continuum_chaos"] + argv)
    cli.main()
    return calls


@pytest.mark.parametrize("argv,expected", [
    (["status"], "cmd_status"),
    (["degrade", "--target", "standby", "--error-pct", "60"], "cmd_degrade_cmd"),
    (["degrade", "--latency-ms", "400"], "cmd_degrade_cmd"),
    (["db-stall", "on"], "cmd_db_stall"),
    (["db-stall", "off", "--target", "standby"], "cmd_db_stall"),
    (["heal"], "cmd_heal"),
    (["hard-down", "--target", "primary", "--seconds", "30"], "cmd_hard_down"),
    (["promote"], "cmd_promote"),
    (["demote"], "cmd_demote"),
    (["submit", "--student", "s9", "--exam", "final"], "cmd_submit"),
    (["submit", "--target", "standby"], "cmd_submit"),
    (["reconcile"], "cmd_reconcile"),
    (["verify", "--target", "standby"], "cmd_verify"),
])
def test_main_dispatches_each_subcommand(cli, argv, expected, monkeypatch):
    calls = run_main(cli, argv, monkeypatch)
    assert len(calls) == 1
    name, args = calls[0]
    assert name == expected


def test_main_parsed_args_reach_handlers(cli, monkeypatch):
    calls = run_main(cli, ["degrade", "--target", "standby",
                           "--error-pct", "25", "--latency-ms", "100"], monkeypatch)
    _, args = calls[0]
    assert args.target == "standby"
    assert args.error_pct == 25
    assert args.latency_ms == 100


def test_main_db_stall_wraps_state_flag(cli, monkeypatch):
    received = []

    def fake_db_stall(ns):
        received.append(ns)

    monkeypatch.setattr(cli, "cmd_db_stall", fake_db_stall)
    monkeypatch.setattr(cli, "load_targets", lambda: None)
    monkeypatch.setattr(sys, "argv", ["continuum_chaos", "db-stall", "on"])
    cli.main()
    assert received[0].on is True and received[0].target == "primary"


def test_script_guard_runs_argparse_when_executed(monkeypatch, capsys):
    monkeypatch.setattr(sys, "argv", ["continuum_chaos.py", "--help"])
    with pytest.raises(SystemExit) as exc:
        runpy.run_path(str(CLI_PATH), run_name="__main__")
    assert exc.value.code == 0
    assert "usage:" in capsys.readouterr().out


def test_script_guard_requires_subcommand(monkeypatch, capsys):
    monkeypatch.setattr(sys, "argv", ["continuum_chaos.py"])
    with pytest.raises(SystemExit) as exc:
        runpy.run_path(str(CLI_PATH), run_name="__main__")
    assert exc.value.code == 2
