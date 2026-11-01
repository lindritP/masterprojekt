# CCSRA developer entry points (SRS §9.5, DR-013).

GO_MODULES := app/v0 app/v1 tools test/e2e
COMPOSE    := docker compose -f deploy/local/docker-compose.yaml
APP        := identity-gateway case-service intake-service validation-worker swagger-ui

.DEFAULT_GOAL := help
.PHONY: help dev dev-down logs stack-up stack-down stack-reset corpus test test-conformance test-e2e \
        fmt fmt-check vet check-boundaries check-register ci docs

help: ## List targets
	@grep -hE '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

dev: stack-up ## Start the stack and the four V1 services (API on :8080, explorer on :8090)
	CCSRA_VERSION=$$(git rev-parse --short HEAD 2>/dev/null || echo dev) \
	  $(COMPOSE) --profile app up -d --build --wait $(APP)
	@echo "API: http://localhost:8080/api/v1   explorer: http://localhost:8090"

dev-down: ## Stop the four services, keep the stack running
	$(COMPOSE) --profile app stop $(APP)

logs: ## Follow the logs of the four services
	$(COMPOSE) --profile app logs -f identity-gateway case-service intake-service validation-worker

test-e2e: corpus ## Run the E2E suite against the running application (after make dev)
	@cd test/e2e && CCSRA_E2E=1 go test -count=1 ./...

stack-up: ## Start PostgreSQL, SeaweedFS (S3), RabbitMQ, Keycloak; migrate; create the bucket
	$(COMPOSE) up -d --wait postgres seaweedfs rabbitmq keycloak
	$(COMPOSE) run --rm db-migrate
	$(COMPOSE) run --rm s3-init

stack-down: ## Stop the stack and the services, keep the data
	$(COMPOSE) --profile app down

stack-reset: ## Stop everything and delete the data
	$(COMPOSE) --profile app down -v

corpus: ## Generate the synthetic PDF corpus and check it against its manifest (FR-048)
	@cd tools && go run ./corpusgen -out ../test/corpus
	@cd test/corpus && shasum -a 256 -c --quiet corpus.sha256 && echo "corpus matches corpus.sha256"

test: corpus ## Run unit tests in every Go module (includes the FR-410 determinism check)
	@for m in $(GO_MODULES); do echo "== $$m"; (cd $$m && go test ./...) || exit 1; done

test-conformance: ## Run the port conformance suite against the local stack (after make stack-up)
	@mkdir -p build
	@cd app/v1 && CCSRA_CONFORMANCE=1 go test -count=1 -json ./internal/adapters/... > ../../build/conformance-local.json || true
	@cd tools && go run ./conformancereport < ../build/conformance-local.json

fmt: ## Format all Go code
	@gofmt -w $(GO_MODULES)

fmt-check: ## Fail if any Go file is not gofmt-formatted
	@out=$$(gofmt -l $(GO_MODULES)); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet: ## Run go vet in every Go module
	@for m in $(GO_MODULES); do (cd $$m && go vet ./...) || exit 1; done

check-boundaries: ## Enforce AR-004 and FR-003–FR-005 import boundaries
	@scripts/check-boundaries.sh

check-register: ## Validate coupling-register.yaml against its schema (CR-001, CR-011)
	@cd tools && go run ./registercheck -srs ../SRS.md ../coupling-register.yaml

ci: fmt-check vet test check-boundaries check-register ## Everything CI runs

docs: ## Regenerate SRS.docx and Research-Design.docx from the Markdown sources
	cd tools && npm install --no-audit --no-fund && \
	node md2docx.js ../SRS.md ../SRS.docx && \
	node md2docx.js ../Research-Design.md ../Research-Design.docx
