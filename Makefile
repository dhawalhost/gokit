.PHONY: test test-race lint tidy vet fmt build vuln cover

# Run all unit tests.
test:
	go test \
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
	golangci-lint run --timeout=5m ./...

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
