#!/usr/bin/env python3
import argparse
import json
import sys
import time
import urllib.request

import boto3

PRIMARY = None
STANDBY = None
ADMIN_TOKEN = "continuum-demo-token"


def load_targets():
    global PRIMARY, STANDBY
    ssm = boto3.client("ssm")
    PRIMARY = ssm.get_parameter(Name="/continuum/primary/public_ip")["Parameter"]["Value"]
    STANDBY = ssm.get_parameter(Name="/continuum/standby/public_ip")["Parameter"]["Value"]


def http(url, method="GET", payload=None, headers=None, timeout=10):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(url, data=data, method=method,
                                 headers={"Content-Type": "application/json", **(headers or {})})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, json.loads(resp.read().decode() or "{}")
    except urllib.error.HTTPError as exc:
        return exc.code, {"error": exc.read().decode()[:200]}
    except Exception as exc:
        return 0, {"error": str(exc)[:200]}


def target(name):
    if name == "primary":
        return f"http://{PRIMARY}"
    if name == "standby":
        return f"http://{STANDBY}"
    raise SystemExit(f"unknown target {name}")


def cmd_status(args):
    for label in ("primary", "standby"):
        code, deep = http(f"{target(label)}/health/deep")
        print(f"[{label}] HTTP {code}")
        print(json.dumps(deep, indent=2)[:800])


def _set_chaos(t, payload):
    code, body = http(f"{t}/admin/chaos", "POST", payload, {"x-admin-token": ADMIN_TOKEN})
    print(f"HTTP {code}: {json.dumps(body)}")


def cmd_degrade_cmd(args):
    t = target(args.target)
    payload = {k: v for k, v in (("error_pct", args.error_pct),
                                 ("latency_ms", args.latency_ms)) if v is not None}
    print(f">> degrading {args.target} with {payload}")
    _set_chaos(t, payload)


def cmd_db_stall(args):
    print(f">> db_stall={args.on} on {args.target} (app alive, DB probes fail)")
    _set_chaos(target(args.target), {"db_stall": args.on})


def cmd_heal(args):
    for lbl in (args.target,) if args.target != "all" else ("primary", "standby"):
        _set_chaos(target(lbl), {"error_pct": 0, "latency_ms": 0, "db_stall": False})


def ec2():
    return boto3.client("ec2")


def instance_id(label):
    ssm = boto3.client("ssm")
    name = "/continuum/primary/instance_id" if label == "primary" else "/continuum/standby/instance_id"
    return ssm.get_parameter(Name=name)["Parameter"]["Value"]


def stop_instance(label):
    iid = instance_id(label)
    print(f">> stopping {label} instance {iid}")
    ec2().stop_instances(InstanceIds=[iid])
    print(">> waiting for stopped state...")
    boto3.client("ec2").get_waiter("instance_stopped").wait(InstanceIds=[iid])


def start_instance(label):
    iid = instance_id(label)
    print(f">> starting {label} instance {iid}")
    ec2().start_instances(InstanceIds=[iid])
    print(">> waiting for running state (app needs ~60s to boot)...")


def cmd_hard_down(args):
    stop_instance(args.target)
    if args.seconds:
        print(f">> sleeping {args.seconds}s then restarting (blip scenario)")
        time.sleep(args.seconds)
        start_instance(args.target)
        print(">> blip complete — expect NO failover from a transient outage this short")


def cmd_blip(args):
    cmd_hard_down(argparse.Namespace(target=args.target, seconds=args.seconds))


def run_ssm(label, commands, comment):
    ssm = boto3.client("ssm")
    resp = ssm.send_command(
        InstanceIds=[instance_id(label)],
        DocumentName="AWS-RunShellScript",
        Comment=comment,
        Parameters={"commands": commands})
    cid = resp["Command"]["CommandId"]
    time.sleep(3)
    inv = ssm.get_command_invocation(CommandId=cid, InstanceId=instance_id(label))
    print(inv["StandardOutputContent"] or inv["StandardErrorContent"] or "(no output)")


def cmd_promote(args):
    print(f">> promoting {args.target} to ACTIVE via SSM")
    run_ssm(args.target, ["echo active > /etc/continuum/role && systemctl restart continuum && sleep 3 && curl -s http://localhost/health/live"], "manual promote")


def cmd_demote(args):
    print(f">> demoting {args.target} to STANDBY via SSM")
    run_ssm(args.target, ["echo standby > /etc/continuum/role && systemctl restart continuum && sleep 3 && curl -s http://localhost/health/live"], "manual demote")


def ensure_student_session(host, student):
    """Register-or-login a demo student; returns bearer headers or None."""
    email = f"{student.lower()}@example.com"
    password = "demo-password-123"
    code, body = http(f"{host}/auth/login", "POST",
                      {"email": email, "password": password})
    if code != 200:
        code, body = http(f"{host}/auth/register", "POST",
                          {"email": email, "password": password, "name": student})
        if code >= 400:
            print(f"HTTP {code}: could not create demo session: {json.dumps(body)}")
            return None
    return {"Authorization": f"Bearer {body['access_token']}"}


def cmd_submit(args):
    exam = args.exam
    host = target(args.target or "primary")
    headers = ensure_student_session(host, args.student)
    if headers is None:
        return
    payload = {"answers": {"0": 1, "1": 0, "2": 1, "3": 1, "4": 1}}
    code, body = http(f"{host}/exam/{exam}/submit", "POST", payload, headers)
    print(f"HTTP {code}: {json.dumps(body)}")


def cmd_reconcile(args):
    lam = boto3.client("lambda")
    name = None
    paginator = boto3.client("lambda").get_paginator("list_functions")
    for page in paginator.paginate():
        for fn in page["Functions"]:
            if fn["FunctionName"].endswith("reconciler"):
                name = fn["FunctionName"]
    if not name:
        raise SystemExit("reconciler lambda not found")
    resp = lam.invoke(FunctionName=name, Payload=b"{}")
    print(json.dumps(json.loads(resp["Payload"].read()), indent=2))


def cmd_verify(args):
    lam = boto3.client("lambda")
    name = None
    for fn in boto3.client("lambda").list_functions()["Functions"]:
        if "verifier-sfn" in fn["FunctionName"]:
            name = fn["FunctionName"]
    if not name:
        raise SystemExit("verifier-sfn lambda not found (deploy control stack first)")
    event = {"mode": "force"}
    if args.target == "standby":
        event.update({"target": "standby", "target_url": f"http://{STANDBY}", "publish_metric": False})
    resp = lam.invoke(FunctionName=name, Payload=json.dumps(event).encode())
    print(json.dumps(json.loads(resp["Payload"].read()), indent=2))


def main():
    parser = argparse.ArgumentParser(prog="continuum_chaos",
                                     description="Continuum fault injection and ops CLI")
    sub = parser.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("status", help="deep health of both nodes")
    p.set_defaults(func=cmd_status)

    p = sub.add_parser("degrade", help="inject app-level degradation")
    p.add_argument("--target", default="primary", choices=["primary", "standby"])
    p.add_argument("--error-pct", type=int)
    p.add_argument("--latency-ms", type=int)
    p.set_defaults(func=cmd_degrade_cmd)

    p = sub.add_parser("db-stall", help="app alive but DB checks fail (catches what ping misses)")
    p.add_argument("state", choices=["on", "off"])
    p.add_argument("--target", default="primary", choices=["primary", "standby"])
    p.set_defaults(func=lambda a: cmd_db_stall(type("A", (), {"on": a.state == "on", "target": a.target})))

    p = sub.add_parser("heal", help="clear all chaos flags")
    p.add_argument("--target", default="all")
    p.set_defaults(func=cmd_heal)

    p = sub.add_parser("hard-down", help="stop the EC2 instance entirely")
    p.add_argument("--target", default="primary", choices=["primary", "standby"])
    p.add_argument("--seconds", type=int, help="auto-restart after N seconds (blip mode)")
    p.set_defaults(func=cmd_hard_down)

    p = sub.add_parser("promote", help="manually promote node to ACTIVE")
    p.add_argument("--target", default="standby", choices=["primary", "standby"])
    p.set_defaults(func=cmd_promote)

    p = sub.add_parser("demote", help="manually demote node to STANDBY")
    p.add_argument("--target", default="primary", choices=["primary", "standby"])
    p.set_defaults(func=cmd_demote)

    p = sub.add_parser("submit", help="submit one sample attempt")
    p.add_argument("--student", default=f"s{int(time.time())%100000}")
    p.add_argument("--exam", default="cs101-midterm")
    p.add_argument("--target", default=None, choices=[None, "primary", "standby"])
    p.set_defaults(func=cmd_submit)

    p = sub.add_parser("reconcile", help="invoke reconciler lambda manually")
    p.set_defaults(func=cmd_reconcile)

    p = sub.add_parser("verify", help="run verifier force-check now")
    p.add_argument("--target", default="primary", choices=["primary", "standby"])
    p.set_defaults(func=cmd_verify)

    args = parser.parse_args()
    load_targets()
    args.func(args)


if __name__ == "__main__":
    main()
