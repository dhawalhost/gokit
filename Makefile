.PHONY: test test-race lint tidy vet fmt build vuln cover coverage test-coverage hooks

# Run all unit tests.
test:
	go test \
		./audit/... \
		./batcher/... \
		./authz/... \
		./bloomfilter/... \
		./bulkhead/... \
		./cache/... \
		./circuitbreaker/... \
		./client/... \
		./compression/... \
		./config/... \
		./crypto/... \
		./database/... \
		./distlock/... \
		./errors/... \
		./eventbus/... \
		./eventstore/... \
		./featureflag/... \
		./gateway/... \
		./grpc/... \
		./health/... \
		./i18n/... \
		./idempotency/... \
		./idgen/... \
		./jobqueue/... \
		./loadshed/... \
		./logger/... \
		./middleware/... \
		./migration/... \
		./multitenancy/... \
		./notify/... \
		./observability/... \
		./outbox/... \
		./pagination/... \
		./ratelimit/... \
		./response/... \
		./router/... \
		./saga/... \
		./scheduler/... \
		./search/... \
		./session/... \
		./secrets/... \
		./server/... \
		./storage/... \
		./streaming/... \
		./testutil/... \
		./validator/... \
		./webhook/... \
		./workflow/...

# Run tests with the race detector.
test-race:
	go test -race \
		./audit/... \
		./batcher/... \
		./authz/... \
		./bloomfilter/... \
		./bulkhead/... \
		./cache/... \
		./circuitbreaker/... \
		./client/... \
		./compression/... \
		./config/... \
		./crypto/... \
		./database/... \
		./distlock/... \
		./errors/... \
		./eventbus/... \
		./eventstore/... \
		./featureflag/... \
		./gateway/... \
		./grpc/... \
		./health/... \
		./i18n/... \
		./idempotency/... \
		./idgen/... \
		./jobqueue/... \
		./loadshed/... \
		./logger/... \
		./middleware/... \
		./migration/... \
		./multitenancy/... \
		./notify/... \
		./observability/... \
		./outbox/... \
		./pagination/... \
		./ratelimit/... \
		./response/... \
		./router/... \
		./saga/... \
		./scheduler/... \
		./search/... \
		./session/... \
		./secrets/... \
		./server/... \
		./storage/... \
		./streaming/... \
		./testutil/... \
		./validator/... \
		./webhook/... \
		./workflow/...

# Generate coverage report (opens in browser via 'go tool cover -html').
cover:
	go test -coverprofile=coverage.out -covermode=atomic \
		./audit/... \
		./batcher/... \
		./authz/... \
		./bloomfilter/... \
		./bulkhead/... \
		./cache/... \
		./circuitbreaker/... \
		./client/... \
		./compression/... \
		./config/... \
		./crypto/... \
		./database/... \
		./distlock/... \
		./errors/... \
		./eventbus/... \
		./eventstore/... \
		./featureflag/... \
		./gateway/... \
		./grpc/... \
		./health/... \
		./i18n/... \
		./idempotency/... \
		./idgen/... \
		./jobqueue/... \
		./loadshed/... \
		./logger/... \
		./middleware/... \
		./migration/... \
		./multitenancy/... \
		./notify/... \
		./observability/... \
		./outbox/... \
		./pagination/... \
		./ratelimit/... \
		./response/... \
		./router/... \
		./saga/... \
		./scheduler/... \
		./search/... \
		./session/... \
		./secrets/... \
		./server/... \
		./storage/... \
		./streaming/... \
		./testutil/... \
		./validator/... \
		./webhook/... \
		./workflow/...
	go tool cover -html=coverage.out

# Run tests and print coverage summary to terminal (without opening browser).
coverage test-coverage:
	go test -coverprofile=coverage.out -covermode=atomic \
		./audit/... \
		./batcher/... \
		./authz/... \
		./bloomfilter/... \
		./bulkhead/... \
		./cache/... \
		./circuitbreaker/... \
		./client/... \
		./compression/... \
		./config/... \
		./crypto/... \
		./database/... \
		./distlock/... \
		./errors/... \
		./eventbus/... \
		./eventstore/... \
		./featureflag/... \
		./gateway/... \
		./grpc/... \
		./health/... \
		./i18n/... \
		./idempotency/... \
		./idgen/... \
		./jobqueue/... \
		./loadshed/... \
		./logger/... \
		./middleware/... \
		./migration/... \
		./multitenancy/... \
		./notify/... \
		./observability/... \
		./outbox/... \
		./pagination/... \
		./ratelimit/... \
		./response/... \
		./router/... \
		./saga/... \
		./scheduler/... \
		./search/... \
		./session/... \
		./secrets/... \
		./server/... \
		./storage/... \
		./streaming/... \
		./testutil/... \
		./validator/... \
		./webhook/... \
		./workflow/...
	@go tool cover -func=coverage.out | tail -n 1

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
