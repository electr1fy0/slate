TF := terraform
PY := $(shell [ -x .venv/bin/python ] && echo .venv/bin/python || echo python3)

.PHONY: core standby control app-deploy fmt validate destroy-all chaos-help test test-live api-dev web-install web-dev web-build web-check

core:
	cd infra/core && $(TF) init && $(TF) apply -auto-approve

standby:
	cd infra/standby && $(TF) init && $(TF) apply -auto-approve

control:
	cd infra/control && $(TF) init && $(TF) apply -auto-approve

plan-core:
	cd infra/core && $(TF) init && $(TF) plan

app-deploy:
	bash scripts/deploy_app.sh

fmt:
	$(TF) fmt -recursive infra/

validate:
	@$(TF) fmt -check -recursive infra/ || true
	@for d in infra/core infra/standby infra/control; do \
	  (cd $$d && $(TF) init -backend=false >/dev/null 2>&1 && $(TF) validate); \
	done
	@python3 -m compileall -q app lambdas chaos scripts && echo "python OK"
	@for f in statemachine/*.json; do jq empty "$$f" && echo "$$f OK"; done

destroy-all:
	cd infra/control && $(TF) destroy -auto-approve || true
	cd infra/standby && $(TF) destroy -auto-approve || true
	cd infra/core && $(TF) destroy -auto-approve || true

chaos-help:
	python3 chaos/continuum_chaos.py --help

test:
	$(PY) -m pytest

test-live:
	CONTINUUM_LIVE_TESTS=1 $(PY) -m pytest -m live

api-dev:
	bash scripts/dev_api.sh

web-install:
	cd web && bun install

web-dev:
	cd web && bun run dev

web-build:
	cd web && bun run build

web-check:
	cd web && bun run typecheck && bunx eslint src && bun run build
