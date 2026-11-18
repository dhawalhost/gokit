.PHONY: test test-race lint tidy tidy-diff vet fmt build vuln cover coverage test-coverage hooks test-integration

MODULES := . ./x ./database ./outbox ./storage/s3 ./eventbus/kafka ./eventbus/rabbitmq
EXAMPLE_MODULES := ./examples/database-crud ./examples/enterprise-service
ALL_MODULES := $(MODULES) $(EXAMPLE_MODULES) ./test/integration
ROOT_DIR := $(CURDIR)

# Run all unit tests across core, x, and adapter modules.
test:
	@for m in $(MODULES); do \
		echo "==> go test ($$m)"; \
		(cd "$$m" && go test ./...) || exit 1; \
	done

# Run tests with the race detector across core, x, and adapter modules.
test-race:
	@for m in $(MODULES); do \
		echo "==> go test -race ($$m)"; \
		(cd "$$m" && go test -race ./...) || exit 1; \
	done

# Generate coverage report for core module (opens in browser via 'go tool cover -html').
cover:
	go test -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -html=coverage.out

# Run tests across all modules and print core module coverage summary.
coverage test-coverage:
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	@for m in $(filter-out .,$(MODULES)); do \
		echo "==> go test -race ($$m)"; \
		(cd "$$m" && go test -race ./...) || exit 1; \
	done
	@go tool cover -func=coverage.out | tail -n 1

lint:
	@for m in $(MODULES); do \
		echo "==> golangci-lint ($$m)"; \
		(cd "$$m" && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run --config="$(ROOT_DIR)/.golangci.yml" --tests=false --timeout=15m ./...) || exit 1; \
	done

tidy:
	@for m in $(ALL_MODULES); do \
		echo "==> go mod tidy ($$m)"; \
		(cd "$$m" && go mod tidy) || exit 1; \
	done

tidy-diff:
	@for m in $(ALL_MODULES); do \
		echo "==> go mod tidy -diff ($$m)"; \
		(cd "$$m" && go mod tidy -diff) || exit 1; \
	done

vet:
	@for m in $(MODULES) $(EXAMPLE_MODULES); do \
		echo "==> go vet ($$m)"; \
		(cd "$$m" && go vet ./...) || exit 1; \
	done
	@echo "==> go vet -tags=integration (./test/integration)"
	@(cd ./test/integration && go vet -tags=integration ./...)

fmt:
	gofmt -w .

build:
	@for m in $(MODULES); do \
		echo "==> go build ($$m)"; \
		(cd "$$m" && go build ./...) || exit 1; \
	done
	@for m in $(EXAMPLE_MODULES); do \
		echo "==> go build ($$m)"; \
		(cd "$$m" && go build -o /dev/null ./...) || exit 1; \
	done
	@echo "==> compile integration tests (./test/integration)"
	@(cd ./test/integration && go test -tags=integration -c . -o /dev/null)

# Check for known vulnerabilities (requires: go install golang.org/x/vuln/cmd/govulncheck@latest).
vuln:
	@for m in $(MODULES); do \
		echo "==> govulncheck ($$m)"; \
		(cd "$$m" && govulncheck ./...) || exit 1; \
	done

# Install repository git hooks.
hooks:
	git config core.hooksPath .githooks
	chmod +x .githooks/pre-commit
	@echo "Git hooks installed from .githooks/"

# Run integration tests (requires external services).
test-integration:
	@echo "Running integration tests (requires PostgreSQL and Redis)..."
	@echo "Set TEST_DATABASE_DSN and TEST_REDIS_ADDR environment variables"
	cd ./test/integration && go test -tags=integration -v ./...
