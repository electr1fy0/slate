#!/usr/bin/env python3
"""Refine IAM Policy Autopilot's checked baseline for the demo's service roles.

The baseline, not an LLM's SDK-to-IAM mapping, supplies all runtime actions.
This script only selects declared role capabilities and binds Terraform ARNs.
"""
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
POLICIES = ROOT / "infra/demo/policies"
baseline = json.loads((POLICIES / "autopilot-baseline.json").read_text())
available = set()
for wrapper in baseline["Policies"]:
    for statement in wrapper["Policy"]["Statement"]:
        value = statement["Action"]
        available.update([value] if isinstance(value, str) else value)


def statement(actions, resource, condition=None, effect="Allow"):
    missing = set(actions) - available
    if missing:
        raise SystemExit(f"Regenerate Autopilot baseline; missing actions: {missing}")
    value = {"Effect": effect, "Action": sorted(actions), "Resource": resource}
    if condition:
        value["Condition"] = condition
    return value


def write(name, statements):
    value = json.dumps({"Version": "2012-10-17", "Statement": statements}, indent=2)
    # instance_arns is a Terraform JSON array, not a quoted string.
    value = value.replace('"${instance_arns}"', '${instance_arns}')
    (POLICIES / f"{name}.json.tftpl").write_text(value + "\n")


table = ["${table_arn}"]
control_keys = {
    "ForAllValues:StringEquals": {"dynamodb:LeadingKeys": ["CONTROL#ACTIVE", "CONTROL#EVENTS"]},
    "Null": {"dynamodb:LeadingKeys": "false"},
}
active_key = {
    "ForAllValues:StringEquals": {"dynamodb:LeadingKeys": ["CONTROL#ACTIVE"]},
    "Null": {"dynamodb:LeadingKeys": "false"},
}
regional = {"StringEquals": {"aws:RequestedRegion": "ap-southeast-2"}}

write("router", [statement(["dynamodb:GetItem", "dynamodb:PutItem"], table, active_key)])
write("demo-verifier", [statement(["dynamodb:GetItem"], table, active_key)])
write("demo-monitor", [
    statement(["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:Query"], table, control_keys),
    statement(["lambda:InvokeFunction"], ["${verifier_arn}"]),
    statement(["states:StartExecution"], ["${state_machine_arn}"]),
])
write("demo-control", [
    statement(["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem", "dynamodb:Query"], table, control_keys),
    statement(["lambda:InvokeFunction"], ["${verifier_arn}", "${reconciler_arn}", "${monitor_arn}"]),
    statement(["states:StartExecution"], ["${state_machine_arn}"]),
    statement(["ec2:DescribeInstances"], "*", regional),
    statement(["ec2:StopInstances", "ec2:StartInstances"], "${instance_arns}", regional),
])
write("demo-reconciler", [statement(["dynamodb:Scan", "dynamodb:GetItem", "dynamodb:ConditionCheckItem", "dynamodb:PutItem", "dynamodb:UpdateItem"], table)])
write("workflow", [statement(["lambda:InvokeFunction"], ["${verifier_arn}", "${control_arn}", "${reconciler_arn}"])])
app_write = ["dynamodb:PutItem", "dynamodb:UpdateItem", "dynamodb:DeleteItem", "dynamodb:BatchWriteItem"]
write("app", [
    statement(app_write + ["dynamodb:GetItem", "dynamodb:Query", "dynamodb:Scan", "dynamodb:ConditionCheckItem"], table),
    statement(app_write, table, {
        "ForAnyValue:StringLike": {"dynamodb:LeadingKeys": ["CONTROL#*", "AUDIT#*"]},
        "Null": {"dynamodb:LeadingKeys": "false"},
    }, effect="Deny"),
])
print("Generated 7 reviewed role templates from the Autopilot baseline.")
