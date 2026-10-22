# gRPC Microservice Kit (`gokit/grpc`)

`grpc` provides production-grade gRPC server and client primitives for high-performance internal service-to-service communication. It seamlessly integrates with GoKit's structured logging (`zap`), OpenTelemetry distributed tracing, circuit breaking, request IDs, request validation, and application error models.

---

## Features

- **Standard Server Interceptors**:
  - **Panic Recovery**: Recovers from handler panics, logs full stack traces safely, and returns `codes.Internal`.
  - **Distributed Tracing**: Extracts W3C TraceContext from incoming gRPC metadata and creates OpenTelemetry server spans with RPC attributes.
  - **Request ID Tracking**: Extracts or generates `X-Request-ID`, stores in context, and echoes in response metadata headers.
  - **Structured Logging**: Logs method name, peer IP, duration, and status codes via `gokit/logger`.
  - **Bearer Auth Interceptor**: Validates incoming bearer tokens and injects authenticated principal/claims into context.
  - **Automated Request Validation**: Automatically executes `Validate()`, `Validate(ctx)`, or `ValidateAll()` on incoming protobuf/struct payloads and returns `codes.InvalidArgument` on error.
- **Resilient Client Dialer**:
  - **Connection Tuning**: Sensible keepalive pings and 16MB message limits.
  - **Exponential Backoff Retries**: Automatic retries with randomized jitter for transient errors (`codes.Unavailable`, `codes.ResourceExhausted`).
  - **Cascading Failure Protection**: Built-in `gokit/circuitbreaker` interceptor that trips when downstream services are failing.
  - **Context Propagation**: Injects W3C TraceContext and request IDs into outgoing RPC metadata.
- **RFC-7807 & Status Code Mapping**:
  - Translates cleanly between GoKit `*errors.AppError` and gRPC `*status.Status`.

---

## Architecture

```
Client Service                                            Target Service
┌───────────────────────────┐                             ┌───────────────────────────┐
│     gokit/grpc.Dial       │                             │    gokit/grpc.NewServer   │
│                           │                             │                           │
│  [Circuit Breaker]        │                             │  [Recovery Interceptor]   │
│            ▼              │                             │            ▼              │
│  [Retry with Backoff]     │                             │  [Request ID Tracking]    │
│            ▼              │   gRPC / HTTP/2 (Protobuf)  │            ▼              │
│  [Trace Context Inject]   │ ──────────────────────────> │  [Trace Context Extract]  │
│            ▼              │                             │            ▼              │
│  [Request ID Inject]      │                             │  [Auth Interceptor]       │
│            ▼              │                             │            ▼              │
│  [Outbound Logging]       │                             │  [Request Validation]     │
│            ▼              │                             │            ▼              │
│   Network Transport       │                             │   Business Logic RPC      │
└───────────────────────────┘                             └───────────────────────────┘
```

---

## Quick Start

### 1. Production gRPC Server Setup

```go
package main

import (
	"context"
	"net"

	gokiterrors "github.com/dhawalhost/gokit/errors"
	gokitgrpc "github.com/dhawalhost/gokit/grpc"
	"github.com/dhawalhost/gokit/logger"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

func main() {
	log := logger.Get()

	// 1. Configure server with standard interceptors
	unaryInterceptors := gokitgrpc.DefaultUnaryServerInterceptors(log, "billing-service")
	
	// Add custom authentication interceptor
	authInterceptor := gokitgrpc.UnaryAuth(func(ctx context.Context, token string) (context.Context, error) {
		if token != "secret-token" {
			return nil, gokiterrors.Unauthorized("INVALID_TOKEN", "bad credentials")
		}
		return context.WithValue(ctx, "user_id", "usr_123"), nil
	})
	unaryInterceptors = append([]grpc.UnaryServerInterceptor{authInterceptor}, unaryInterceptors...)

	srv := gokitgrpc.NewServer(gokitgrpc.ServerConfig{
		EnableReflection:  true, // Enable grpcurl & Postman exploration
		UnaryInterceptors: unaryInterceptors,
		StreamInterceptors: gokitgrpc.DefaultStreamServerInterceptors(log, "billing-service"),
	})

	// 2. Register your Protobuf service implementation
	// pb.RegisterBillingServiceServer(srv, &billingHandler{})

	lis, err := net.Listen("tcp", ":9090")
	if err != nil {
		log.Fatal("failed to listen", zap.Error(err))
	}

	log.Info("gRPC server listening on :9090")
	if err := srv.Serve(lis); err != nil {
		log.Fatal("server stopped", zap.Error(err))
	}
}
```

---

### 2. Resilient Client Dialer (Retries & Circuit Breaking)

```go
package main

import (
	"context"
	"time"

	"github.com/dhawalhost/gokit/circuitbreaker"
	gokitgrpc "github.com/dhawalhost/gokit/grpc"
	"github.com/dhawalhost/gokit/logger"
	"google.golang.org/grpc/codes"
)

func NewOrderClient(target string) (*grpc.ClientConn, error) {
	log := logger.Get()

	// Circuit breaker to prevent thundering herds when target is failing
	cb := circuitbreaker.New(circuitbreaker.Config{
		Name:         "orders-grpc",
		MaxFailures:  5,
		ResetTimeout: 10 * time.Second,
	})

	conn, err := gokitgrpc.Dial(context.Background(), gokitgrpc.ClientConfig{
		Target:   target,
		Insecure: true, // Use TLS in production
		Retry: &gokitgrpc.RetryConfig{
			MaxAttempts:    3,
			InitialBackoff: 50 * time.Millisecond,
			MaxBackoff:     500 * time.Millisecond,
			RetryableCodes: []codes.Code{codes.Unavailable, codes.ResourceExhausted},
		},
		CircuitBreaker:    cb,
		UnaryInterceptors: gokitgrpc.DefaultUnaryClientInterceptors(log, "api-gateway"),
	})
	if err != nil {
		return nil, err
	}

	return conn, nil
}
```

---

### 3. Bidirectional Error Translation

Seamlessly map application errors to gRPC status codes in handlers:

```go
func (s *server) GetUser(ctx context.Context, req *pb.GetUserRequest) (*pb.UserResponse, error) {
	user, err := s.repo.FindByID(ctx, req.Id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Maps automatically to codes.NotFound
			return nil, gokitgrpc.ToGRPCError(gokiterrors.NotFound("USER_NOT_FOUND", "user does not exist"))
		}
		// Maps to codes.Internal
		return nil, gokitgrpc.ToGRPCError(err)
	}

	return &pb.UserResponse{...}, nil
}
```

And parse gRPC status codes into clean application errors on the client side:

```go
resp, err := client.GetUser(ctx, req)
if err != nil {
	appErr := gokitgrpc.FromGRPCError(err)
	// appErr.HTTPStatus == 404, appErr.Code == "NOT_FOUND"
	return appErr
}
```

---

## Status Code Translation Table

| Application HTTP Status | gRPC Status Code | Description |
|---|---|---|
| `400 Bad Request` | `codes.InvalidArgument` | Invalid payload or field validation error |
| `401 Unauthorized` | `codes.Unauthenticated` | Missing or invalid auth credentials |
| `403 Forbidden` | `codes.PermissionDenied` | Authenticated caller lacks permissions |
| `404 Not Found` | `codes.NotFound` | Requested resource was not found |
| `409 Conflict` | `codes.AlreadyExists` | Resource already exists or version conflict |
| `422 Unprocessable` | `codes.InvalidArgument` | Semantic format validation failure |
| `429 Too Many Requests` | `codes.ResourceExhausted` | Rate limit or quota exhausted |
| `503 Service Unavailable`| `codes.Unavailable` | Downstream service down or transient error |
| `504 Gateway Timeout` | `codes.DeadlineExceeded` | Context deadline exceeded |
| `500 Internal Error` | `codes.Internal` | Unexpected server crash or database failure |
