TF := terraform

.PHONY: core standby control app-deploy fmt validate destroy-all test coverage api-dev web-install web-dev web-build web-check

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
	@go vet ./... && go build ./... && echo "go OK"
	@for f in statemachine/*.json; do jq empty "$$f" && echo "$$f OK"; done

destroy-all:
	cd infra/control && $(TF) destroy -auto-approve || true
	cd infra/standby && $(TF) destroy -auto-approve || true
	cd infra/core && $(TF) destroy -auto-approve || true

test:
	go test -race ./cmd/... ./internal/...

coverage:
	go test -race -covermode=atomic -coverpkg=./cmd/...,./internal/... -coverprofile=/tmp/slate-go.cover ./cmd/... ./internal/...
	@test "$$(go tool cover -func=/tmp/slate-go.cover | awk '/^total:/ {print $$NF}')" = "100.0%"
	cd web && bun run test:coverage

api-dev:
	bash scripts/dev_api.sh

web-install:
	cd web && bun install

web-dev:
	cd web && bun run dev

web-build:
	cd web && bun run build

web-check:
	cd web && bun run typecheck && bun run lint && bun run test:coverage && bun run build
