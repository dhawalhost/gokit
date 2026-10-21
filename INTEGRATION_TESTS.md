# Integration Tests

The `test/integration/` directory contains integration tests for gokit packages that require external services.

## Prerequisites

### PostgreSQL
```bash
# Using Docker
docker run -d \
  --name gokit-postgres \
  -e POSTGRES_PASSWORD=testpass \
  -e POSTGRES_USER=testuser \
  -e POSTGRES_DB=testdb \
  -p 5432:5432 \
  postgres:15-alpine
```

### Redis
```bash
# Using Docker
docker run -d \
  --name gokit-redis \
  -p 6379:6379 \
  redis:7-alpine
```

## Running Integration Tests

### Run all integration tests:
```bash
make test-integration
```

### Or manually with environment variables:
```bash
export TEST_DATABASE_DSN="postgres://testuser:testpass@localhost:5432/testdb?sslmode=disable"
export TEST_REDIS_ADDR="localhost:6379"

go test -tags=integration -v ./test/integration/...
```

### Run with verbose output:
```bash
TEST_DATABASE_DSN="..." TEST_REDIS_ADDR="..." go test -tags=integration -v ./test/integration/
```

## Environment Variables

- `TEST_DATABASE_DSN`: PostgreSQL connection string
- `TEST_REDIS_ADDR`: Redis server address (default: localhost:6379)

## Test Coverage

### Database Package
- Connection and ping
- CRUD operations with GORM
- Health checks
- Raw pgxpool queries
- SQL injection prevention validation

### Cache Package
- Redis connection
- Set/Get/Delete operations
- Key existence checks
- TTL expiration
- Connection cleanup

### Rate Limit Package
- In-memory rate limiting
- Redis-backed rate limiting
- Concurrent request handling
- TTL-based cleanup
- Sliding window algorithm

## Docker Compose

For convenience, you can use docker-compose to start all services:

```yaml
# docker-compose.test.yml
version: '3.8'
services:
  postgres:
    image: postgres:15-alpine
    environment:
      POSTGRES_USER: testuser
      POSTGRES_PASSWORD: testpass
      POSTGRES_DB: testdb
    ports:
      - "5432:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U testuser"]
      interval: 5s
      timeout: 5s
      retries: 5

  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 5
```

Run with:
```bash
docker-compose -f docker-compose.test.yml up -d
make test-integration
docker-compose -f docker-compose.test.yml down
```

## CI/CD Integration

Integration tests are skipped in CI by default (they require external services). To enable:

```yaml
# .github/workflows/integration.yml
name: Integration Tests

on: [push, pull_request]

jobs:
  integration:
    runs-on: ubuntu-latest
    
    services:
      postgres:
        image: postgres:15-alpine
        env:
          POSTGRES_USER: testuser
          POSTGRES_PASSWORD: testpass
          POSTGRES_DB: testdb
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
        ports:
          - 5432:5432
      
      redis:
        image: redis:7-alpine
        options: >-
          --health-cmd "redis-cli ping"
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
        ports:
          - 6379:6379
    
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      
      - name: Run integration tests
        env:
          TEST_DATABASE_DSN: postgres://testuser:testpass@localhost:5432/testdb?sslmode=disable
          TEST_REDIS_ADDR: localhost:6379
        run: make test-integration
```

## Cleanup

After running tests, clean up Docker containers:

```bash
docker stop gokit-postgres gokit-redis
docker rm gokit-postgres gokit-redis
```
