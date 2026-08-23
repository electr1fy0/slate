"""Cross-process end-to-end tier.

Boots two REAL uvicorn processes (primary active, standby read-only) sharing
one moto_server-backed DynamoDB endpoint, then drives them with the actual
continuum_chaos CLI over HTTP — no test doubles in the request path.
"""
import importlib.util
import json
import os
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
APP_DIR = ROOT / "app"
CLI_PATH = ROOT / "chaos" / "continuum_chaos.py"
REGION = "ap-south-1"


def _free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def _wait_ready(url, deadline_s=25):
    end = time.time() + deadline_s
    while time.time() < end:
        try:
            with urllib.request.urlopen(url, timeout=2) as resp:
                if resp.status == 200:
                    return True
        except Exception:
            time.sleep(0.3)
    return False


@pytest.fixture(scope="module")
def stack(tmp_path_factory):
    pytest.importorskip("uvicorn")

    logs = tmp_path_factory.mktemp("logs")
    # moto_server still requires the client to present *some* credentials.
    os.environ.setdefault("AWS_ACCESS_KEY_ID", "testing")
    os.environ.setdefault("AWS_SECRET_ACCESS_KEY", "testing")
    moto_port = _free_port()
    moto_log = open(logs / "moto.log", "w")
    moto_proc = subprocess.Popen(
        [sys.executable, "-m", "moto.server", "-H", "127.0.0.1", "-p", str(moto_port)],
        stdout=moto_log, stderr=subprocess.STDOUT)

    endpoint = f"http://127.0.0.1:{moto_port}"
    try:
        import boto3

        ddb = boto3.client("dynamodb", region_name=REGION, endpoint_url=endpoint)
        deadline = time.time() + 20
        while True:
            try:
                ddb.create_table(
                    TableName="continuum",
                    KeySchema=[{"AttributeName": "pk", "KeyType": "HASH"},
                               {"AttributeName": "sk", "KeyType": "RANGE"}],
                    AttributeDefinitions=[
                        {"AttributeName": "pk", "AttributeType": "S"},
                        {"AttributeName": "sk", "AttributeType": "S"}],
                    BillingMode="PAY_PER_REQUEST")
                break
            except ddb.exceptions.ResourceNotFoundException:
                if time.time() > deadline:
                    raise
                time.sleep(0.5)

        roles = tmp_path_factory.mktemp("roles")
        (roles / "primary").write_text("active")
        (roles / "standby").write_text("standby")

        def spawn(port, role):
            env = os.environ.copy()
            env.update({
                "TABLE_NAME": "continuum",
                "DDB_ENDPOINT_MODE": f"{endpoint}/",
                "CONTINUUM_ROLE_FILE": str(roles / role),
                "AWS_DEFAULT_REGION": REGION,
                "PYTHONPATH": str(APP_DIR),
                # CI/dev machines vary wildly in disk fill level and load;
                # keep the journeys focused on failover behaviour, not host
                # state.
                "DISK_THRESHOLD_PCT": "99",
                "LOAD_THRESHOLD": "64",
            })
            log = open(logs / f"app-{role}.log", "w")
            return subprocess.Popen(
                [sys.executable, "-m", "uvicorn", "main:app",
                 "--host", "127.0.0.1", "--port", str(port)],
                cwd=str(APP_DIR), env=env, stdout=log, stderr=subprocess.STDOUT)

        ports = {"primary": _free_port(), "standby": _free_port()}
        procs = {role: spawn(port, role) for role, port in ports.items()}

        ready = all(_wait_ready(f"http://127.0.0.1:{ports[role]}/health/live")
                    for role in ports)
        if not ready:
            for proc in procs.values():
                proc.terminate()
            moto_proc.terminate()
            diagnostics = ""
            for name in ("app-primary.log", "app-standby.log", "moto.log"):
                content = (logs / name).read_text(errors="replace")[-1500:]
                diagnostics += f"\n--- {name} ---\n{content}"
            pytest.fail(f"servers did not become ready{diagnostics}")

        yield {"endpoint": endpoint,
               "hosts": {role: f"127.0.0.1:{ports[role]}" for role in ports},
               "table": boto3.resource("dynamodb", region_name=REGION,
                                       endpoint_url=endpoint).Table("continuum")}
    finally:
        for proc in locals().get("procs", {}).values():
            proc.terminate()
        for proc in locals().get("procs", {}).values():
            proc.wait(timeout=10)
        moto_proc.terminate()
        moto_proc.wait(timeout=10)
        moto_log.close()


@pytest.fixture()
def cli(stack):
    spec = importlib.util.spec_from_file_location("continuum_chaos_live", CLI_PATH)
    mod = importlib.util.module_from_spec(spec)
    sys.modules["continuum_chaos_live"] = mod
    spec.loader.exec_module(mod)
    mod.PRIMARY = stack["hosts"]["primary"]
    mod.STANDBY = stack["hosts"]["standby"]
    return mod


def http(url, method="GET", payload=None, headers=None):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(url, data=data, method=method,
                                 headers={"Content-Type": "application/json",
                                          **(headers or {})})
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return resp.status, json.loads(resp.read().decode() or "{}")
    except urllib.error.HTTPError as exc:
        return exc.code, json.loads(exc.read().decode() or "{}")


@pytest.mark.e2e
def test_baseline_primary_serves_and_standby_is_read_only(cli, capsys):
    assert http(f"http://{cli.PRIMARY}/health/live")[0] == 200

    cli.cmd_submit(__import__("argparse").Namespace(
        student="e2e-burst1", exam="cs101-midterm", target=None))
    out = capsys.readouterr().out
    assert '"status": "committed"' in out

    # The standby node refuses writes until promoted.
    code, _ = http(f"http://{cli.STANDBY}/exam/cs101-midterm/submit",
                   "POST", {"student_id": "nope", "answers": {"0": 1}})
    assert code == 503


@pytest.mark.e2e
def test_cli_status_reports_both_nodes(stack, cli, capsys):
    import argparse

    cli.cmd_status(argparse.Namespace())
    out = capsys.readouterr().out
    assert "[primary]" in out and "[standby]" in out
    assert '"status": "alive"' in out or '"healthy"' in out or '"alive"' in out


@pytest.mark.e2e
def test_degrade_then_heal_roundtrip_over_real_http(cli, capsys):
    import argparse

    exam_url = f"http://{cli.PRIMARY}/exam/cs101-midterm"

    cli.cmd_degrade_cmd(argparse.Namespace(target="primary", error_pct=100,
                                           latency_ms=None))
    codes = {http(exam_url)[0] for _ in range(5)}
    assert codes == {500}  # every request injected

    cli.cmd_heal(argparse.Namespace(target="all"))
    assert http(exam_url)[0] == 404 or http(exam_url)[0] == 200


@pytest.mark.e2e
def test_db_stall_flag_travels_through_admin_api(cli, capsys):
    import argparse

    cli.cmd_db_stall(argparse.Namespace(on=True, target="standby"))
    code, deep = http(f"http://{cli.STANDBY}/health/deep")
    assert code == 503
    assert deep["checks"]["ddb"] == {"ok": False, "reason": "db_stall chaos flag"}

    # Liveness stays green: exactly the blind spot ping checks miss.
    assert http(f"http://{cli.STANDBY}/health/live")[0] == 200

    cli.cmd_heal(argparse.Namespace(target="standby"))
    assert http(f"http://{cli.STANDBY}/health/deep")[0] == 200


@pytest.mark.e2e
def test_submissions_persist_in_shared_global_table(stack, cli):
    item = stack["table"].get_item(
        Key={"pk": "SUBMISSION#cs101-midterm", "sk": "STUDENT#e2e-burst1"}).get("Item")
    assert item is not None
    assert item["request_id"]
