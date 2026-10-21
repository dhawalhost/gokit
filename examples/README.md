# gokit Examples

This directory contains comprehensive examples demonstrating how to use the gokit library in production applications.

## Examples

### 1. Basic Server (`basic-server/`)
A minimal HTTP server with:
- Health checks (liveness and readiness)
- Prometheus metrics
- Structured logging
- Request ID tracking
- Secure headers

**Run:**
```bash
cd examples/basic-server
APP_SERVER_ADDR=:8080 APP_LOG_LEVEL=info go run main.go
```

### 2. Database CRUD (`database-crud/`)
RESTful API with PostgreSQL:
- GORM integration
- Input validation
- Pagination support
- SQL injection prevention
- Error handling
- Health checks for database

**Setup:**
```bash
# Set database connection
export APP_DATABASE_DSN="postgres://user:pass@localhost:5432/dbname?sslmode=disable"
cd examples/database-crud
go run main.go
```

### 3. JWT Authentication (`auth-jwt/`)
API with JWT authentication:
- Login endpoint
- Protected routes
- JWT token generation and validation
- Claims extraction from context

**Run:**
```bash
export APP_JWT_SECRET="your-super-secret-key-min-32-bytes-long!"
cd examples/auth-jwt
go run main.go
```

**Test:**
```bash
# Login
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"user@example.com","password":"password123"}' | jq -r .data.token)

# Access protected route
curl http://localhost:8080/api/v1/auth/profile \
  -H "Authorization: Bearer $TOKEN"
```

### 4. Cache & Rate Limiting (`cache-ratelimit/`)
Redis-backed caching and rate limiting:
- Redis cache operations (get/set)
- Per-IP rate limiting
- Health checks for Redis

**Setup:**
```bash
# Start Redis
docker run -d -p 6379:6379 redis:latest

export APP_REDIS_ADDR=localhost:6379
cd examples/cache-ratelimit
go run main.go
```

**Test:**
```bash
# Set cache value
curl "http://localhost:8080/api/v1/cache/set?key=mykey&value=myvalue"

# Get cache value
curl "http://localhost:8080/api/v1/cache/get?key=mykey"

# Trigger rate limit (run this many times quickly)
for i in {1..30}; do curl http://localhost:8080/api/v1/cache/get?key=test; done
```

### 5. Circuit Breaker (`circuit-breaker/`)
Fault-tolerant external service calls:
- Circuit breaker pattern
- Automatic failure detection
- State transition monitoring
- Execution timeout

**Run:**
```bash
cd examples/circuit-breaker
go run main.go
```

**Test:**
```bash
# Call multiple times to see circuit breaker in action
for i in {1..20}; do 
  curl http://localhost:8080/api/v1/call
  sleep 0.1
done

# Check circuit breaker status
curl http://localhost:8080/api/v1/status
```

## Configuration

All examples support configuration via environment variables with the `APP_` prefix:

```bash
# Server
APP_SERVER_ADDR=:8080
APP_SERVER_READ_TIMEOUT=30s
APP_SERVER_WRITE_TIMEOUT=30s

# Database
APP_DATABASE_DSN="postgres://..."
APP_DATABASE_MAX_OPEN_CONNS=25
APP_DATABASE_MAX_IDLE_CONNS=5

# Redis
APP_REDIS_ADDR=localhost:6379
APP_REDIS_PASSWORD=""
APP_REDIS_DB=0
APP_REDIS_DIAL_TIMEOUT=5s
APP_REDIS_READ_TIMEOUT=3s
APP_REDIS_WRITE_TIMEOUT=3s

# JWT
APP_JWT_SECRET="your-secret-key"
APP_JWT_EXPIRY=15m

# Logging
APP_LOG_LEVEL=info
APP_LOG_DEVELOPMENT=false

# Telemetry
APP_TELEMETRY_ENABLED=true
APP_TELEMETRY_OTLP_ENDPOINT=localhost:4318
APP_TELEMETRY_SERVICE_NAME=myservice
```

## Best Practices Demonstrated

1. **Security**: JWT validation, SQL injection prevention, secure headers
2. **Observability**: Structured logging, metrics, health checks
3. **Resilience**: Circuit breakers, rate limiting, timeouts
4. **Error Handling**: Structured errors, proper HTTP status codes
5. **Configuration**: Environment-based config with sensible defaults
6. **Testing**: Each example can be tested with curl commands

## Production Checklist

Before deploying to production:

- [ ] Set strong JWT secrets (minimum 32 bytes)
- [ ] Configure database connection pooling
- [ ] Enable TLS/HTTPS
- [ ] Set appropriate rate limits
- [ ] Configure health check timeouts
- [ ] Enable telemetry and monitoring
- [ ] Set up proper logging levels
- [ ] Use environment-specific configurations
- [ ] Test graceful shutdown behavior
- [ ] Verify circuit breaker thresholds

## Additional Resources

- [Main README](../README.md)
- [Security Policy](../SECURITY.md)
- [Changelog](../CHANGELOG.md)
- [API Documentation](https://pkg.go.dev/github.com/dhawalhost/gokit)
