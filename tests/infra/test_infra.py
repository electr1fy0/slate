"""Validation tests for Terraform stacks and shell tooling."""
import json
import shutil
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
INFRA = ROOT / "infra"
STACKS = ["core", "standby", "control"]


def _run(cmd, cwd=None, timeout=300):
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True,
                          timeout=timeout)


@pytest.mark.skipif(shutil.which("terraform") is None, reason="terraform not installed")
def test_terraform_format_is_clean():
    result = _run(["terraform", "fmt", "-check", "-recursive", str(INFRA)])
    assert result.returncode == 0, f"unformatted files:\n{result.stdout}{result.stderr}"


@pytest.mark.skipif(shutil.which("terraform") is None, reason="terraform not installed")
@pytest.mark.parametrize("stack", STACKS)
def test_terraform_stack_validates(stack):
    stack_dir = INFRA / stack
    init = _run(["terraform", "init", "-backend=false", "-input=false"], cwd=stack_dir)
    assert init.returncode == 0, f"init failed:\n{init.stderr[-2000:]}"
    validate = _run(["terraform", "validate"], cwd=stack_dir)
    assert validate.returncode == 0, f"validate failed:\n{validate.stderr[-2000:]}"


def test_every_stack_has_required_files():
    for stack in STACKS:
        stack_dir = INFRA / stack
        assert (stack_dir / "main.tf").exists() or stack == "control"
        assert (stack_dir / "providers.tf").exists()
        assert (stack_dir / "variables.tf").exists()
        assert (stack_dir / "outputs.tf").exists()


def test_tfvars_examples_do_not_contain_secrets():
    for example in INFRA.rglob("terraform.tfvars.example"):
        content = example.read_text().lower()
        for secret_marker in ("AKIA", "BEGIN RSA PRIVATE KEY", "secret_access_key ="):
            assert secret_marker.lower() not in content, f"{example} looks like it leaks {secret_marker}"


def test_deploy_script_passes_bash_syntax_check():
    result = _run(["bash", "-n", str(ROOT / "scripts" / "deploy_app.sh")])
    assert result.returncode == 0, result.stderr


@pytest.mark.skipif(shutil.which("shellcheck") is None, reason="shellcheck not installed")
def test_deploy_script_passes_shellcheck():
    result = _run(["shellcheck", "--severity=warning",
                   str(ROOT / "scripts" / "deploy_app.sh")])
    assert result.returncode == 0, result.stdout


def test_user_data_template_references_expected_units():
    template = (INFRA / "modules" / "compute" / "templates" /
                "user-data.sh.tftpl").read_text()
    assert "continuum.service" in template
    assert "set_role" in template


def test_compute_module_grants_ssm_and_stacks_publish_parameters():
    main_tf = (INFRA / "modules" / "compute" / "main.tf").read_text()
    assert "ssm" in main_tf  # IAM wiring for SSM run-command access
    for stack in ("core", "standby"):
        ssm_tf = (INFRA / stack / "ssm.tf").read_text()
        assert "aws_ssm_parameter" in ssm_tf, f"{stack} publishes no SSM parameters"


def test_asl_file_referenced_by_control_stack():
    control = (INFRA / "control").glob("*.tf")
    blobs = [p.read_text() for p in control]
    combined = "\n".join(blobs)
    assert "failover.asl.json" in combined
    assert "templatefile" in combined or "file(" in combined
