TF ?= terraform

.PHONY: demo-local demo-build demo-init demo-plan demo-deploy demo-web demo-pause demo-resume demo-destroy fmt validate test coverage web-check api-dev web-dev web-build core standby control app-deploy destroy-all

demo-local:
	bash scripts/demo_local.sh

demo-build:
	bash scripts/build_demo.sh

demo-init:
	$(TF) -chdir=infra/demo init

demo-plan: demo-build demo-init
	$(TF) -chdir=infra/demo plan -out=demo.tfplan

demo-deploy:
	bash scripts/deploy_demo.sh "$(TF)"

demo-web:
	bash scripts/deploy_demo_web.sh "$(TF)"

demo-pause:
	bash scripts/set_demo_state.sh paused "$(TF)"

demo-resume:
	bash scripts/set_demo_state.sh running "$(TF)"

demo-destroy:
	$(TF) -chdir=infra/demo destroy

fmt:
	$(TF) fmt -recursive infra/demo
	gofmt -w cmd/demo* cmd/router internal/continuum internal/clusterstate

validate:
	$(TF) -chdir=infra/demo validate
	go vet ./...
	go build ./...

test:
	go test -race -timeout 30m ./...

coverage:
	go test -race -timeout 30m -covermode=atomic -coverprofile=.demo-go.cover ./...
	go tool cover -func=.demo-go.cover
	cd web && bun run test:coverage

web-check:
	cd web && bun run typecheck && bun run lint && bun run test && bun run build

api-dev:
	bash scripts/dev_api.sh

web-dev:
	cd web && bun run dev

web-build:
	cd web && bun run build

# Disable shortcuts from the archived cross-Region proposal.
core standby control app-deploy destroy-all:
	@echo "Archived cross-Region targets are disabled. Use make demo-local or make demo-deploy."
	@exit 1
