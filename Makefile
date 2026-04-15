.PHONY: test test-race lint tidy vet fmt build vuln cover hooks

# Run all unit tests.
test:
	go test \
		./bloomfilter/... \
		./circuitbreaker/... \
		./config/... \
		./crypto/... \
		./errors/... \
		./health/... \
		./idgen/... \
		./logger/... \
		./middleware/... \
		./observability/... \
		./pagination/... \
		./ratelimit/... \
		./response/... \
		./router/... \
		./testutil/... \
		./validator/...

# Run tests with the race detector.
test-race:
	go test -race \
		./bloomfilter/... \
		./circuitbreaker/... \
		./config/... \
		./crypto/... \
		./errors/... \
		./health/... \
		./idgen/... \
		./logger/... \
		./middleware/... \
		./observability/... \
		./pagination/... \
		./ratelimit/... \
		./response/... \
		./router/... \
		./testutil/... \
		./validator/...

# Generate coverage report (opens in browser via 'go tool cover -html').
cover:
	go test -coverprofile=coverage.out -covermode=atomic \
		./bloomfilter/... \
		./circuitbreaker/... \
		./config/... \
		./crypto/... \
		./errors/... \
		./health/... \
		./idgen/... \
		./logger/... \
		./middleware/... \
		./observability/... \
		./pagination/... \
		./ratelimit/... \
		./response/... \
		./router/... \
		./testutil/... \
		./validator/...
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
