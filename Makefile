.PHONY: test lint tidy vet fmt build

test:
	go test ./... -race -cover

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

vet:
	go vet ./...

fmt:
	gofmt -w .

build:
	go build ./...
