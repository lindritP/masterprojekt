# CCSRA developer entry points (SRS §9.5, DR-013).
# Stage B0: services are stubs; `dev` starts the local stack only and `test`
# runs unit tests only. Port-conformance and E2E suites join at B2/B3.

GO_MODULES := app/v0 app/v1 tools
COMPOSE    := docker compose -f deploy/local/docker-compose.yaml

.DEFAULT_GOAL := help
.PHONY: help dev stack-up stack-down stack-reset test fmt fmt-check vet \
        check-boundaries check-register ci docs

help: ## List targets
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

dev: stack-up ## Start the local stack (services join at B3)
	@echo "Local stack is up. The four services are wired in stage B3 (SRS §11)."

stack-up: ## Start PostgreSQL, SeaweedFS (S3), RabbitMQ, Keycloak and wait until healthy
	$(COMPOSE) up -d --wait

stack-down: ## Stop the local stack, keep its data
	$(COMPOSE) down

stack-reset: ## Stop the local stack and delete its data
	$(COMPOSE) down -v

test: ## Run unit tests in every Go module
	@for m in $(GO_MODULES); do echo "== $$m"; (cd $$m && go test ./...) || exit 1; done

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
