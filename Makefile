.PHONY: test test-race lint tidy vet fmt build vuln cover hooks

# Run all unit tests.
test:
	go test \
		./audit/... \
		./authz/... \
		./bloomfilter/... \
		./bulkhead/... \
		./cache/... \
		./circuitbreaker/... \
		./client/... \
		./config/... \
		./crypto/... \
		./database/... \
		./distlock/... \
		./errors/... \
		./eventbus/... \
		./featureflag/... \
		./health/... \
		./idempotency/... \
		./idgen/... \
		./jobqueue/... \
		./logger/... \
		./middleware/... \
		./multitenancy/... \
		./observability/... \
		./outbox/... \
		./pagination/... \
		./ratelimit/... \
		./response/... \
		./router/... \
		./saga/... \
		./server/... \
		./testutil/... \
		./validator/... \
		./webhook/...

# Run tests with the race detector.
test-race:
	go test -race \
		./audit/... \
		./authz/... \
		./bloomfilter/... \
		./bulkhead/... \
		./cache/... \
		./circuitbreaker/... \
		./client/... \
		./config/... \
		./crypto/... \
		./database/... \
		./distlock/... \
		./errors/... \
		./eventbus/... \
		./featureflag/... \
		./health/... \
		./idempotency/... \
		./idgen/... \
		./jobqueue/... \
		./logger/... \
		./middleware/... \
		./multitenancy/... \
		./observability/... \
		./outbox/... \
		./pagination/... \
		./ratelimit/... \
		./response/... \
		./router/... \
		./saga/... \
		./server/... \
		./testutil/... \
		./validator/... \
		./webhook/...

# Generate coverage report (opens in browser via 'go tool cover -html').
cover:
	go test -coverprofile=coverage.out -covermode=atomic \
		./audit/... \
		./authz/... \
		./bloomfilter/... \
		./bulkhead/... \
		./cache/... \
		./circuitbreaker/... \
		./client/... \
		./config/... \
		./crypto/... \
		./database/... \
		./distlock/... \
		./errors/... \
		./eventbus/... \
		./featureflag/... \
		./health/... \
		./idempotency/... \
		./idgen/... \
		./jobqueue/... \
		./logger/... \
		./middleware/... \
		./multitenancy/... \
		./observability/... \
		./outbox/... \
		./pagination/... \
		./ratelimit/... \
		./response/... \
		./router/... \
		./saga/... \
		./server/... \
		./testutil/... \
		./validator/... \
		./webhook/...
	go tool cover -html=coverage.out

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run --tests=false --timeout=15m ./...

tidy:
	go mod tidy

vet:
	go vet ./...

fmt:
	gofmt -w .

build:
	go build ./...

# Check for known vulnerabilities (requires: go install golang.org/x/vuln/cmd/govulncheck@latest).
vuln:
	govulncheck ./...

# Install repository git hooks.
hooks:
	git config core.hooksPath .githooks
	chmod +x .githooks/pre-commit
	@echo "Git hooks installed from .githooks/"

# Run integration tests (requires external services).
test-integration:
	@echo "Running integration tests (requires PostgreSQL and Redis)..."
	@echo "Set TEST_DATABASE_DSN and TEST_REDIS_ADDR environment variables"
	go test -tags=integration -v ./test/integration/...
