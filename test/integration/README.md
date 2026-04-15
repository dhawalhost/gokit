# Integration Tests

This directory contains all integration tests for the gokit library.

## Structure

All integration tests are in the `integration_test` package:

- `database_test.go` - PostgreSQL database integration tests
- `cache_test.go` - Redis cache integration tests  
- `ratelimit_test.go` - Rate limiter (in-memory and Redis) integration tests

## Running Tests

From the project root:

```bash
# Run all integration tests
make test-integration

# Or manually
TEST_DATABASE_DSN="..." TEST_REDIS_ADDR="..." go test -tags=integration -v ./test/integration/...
```

## Requirements

- PostgreSQL (for database tests)
- Redis (for cache and ratelimit tests)

See [INTEGRATION_TESTS.md](../../INTEGRATION_TESTS.md) in the project root for detailed setup instructions.

## Environment Variables

- `TEST_DATABASE_DSN` - PostgreSQL connection string (e.g., `postgres://user:pass@localhost:5432/testdb?sslmode=disable`)
- `TEST_REDIS_ADDR` - Redis server address (e.g., `localhost:6379`)

## Test Coverage

### Database Tests (`database_test.go`)
- `TestDatabaseConnection` - Connection pool and ping
- `TestDatabaseCRUD` - Create, Read, Update, Delete operations
- `TestDatabaseHealthCheck` - Health check functionality
- `TestDatabaseRawQueries` - Raw SQL queries via pgxpool

### Cache Tests (`cache_test.go`)
- `TestRedisCacheConnection` - Connection and health check
- `TestRedisCacheOperations` - Set, Get, Delete, Exists operations
- `TestRedisCacheClose` - Graceful shutdown

### Rate Limit Tests (`ratelimit_test.go`)
- `TestInMemoryStoreRateLimit` - In-memory token bucket rate limiting
- `TestInMemoryStoreCleanup` - TTL-based memory cleanup
- `TestRedisStoreRateLimit` - Redis sliding window rate limiting
- `TestRedisStoreConcurrent` - Concurrent request handling

## Build Tags

All tests use the `integration` build tag to separate them from unit tests:

```go
//go:build integration
// +build integration
```

This prevents integration tests from running during normal `go test ./...` execution.
