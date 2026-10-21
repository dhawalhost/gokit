package middleware

import (
	"net/http"

	"github.com/dhawalhost/gokit/idempotency"
)

// IdempotencyConfig is an alias for idempotency.Config.
type IdempotencyConfig = idempotency.Config

// Idempotency returns a middleware that guarantees idempotent request handling
// based on the Idempotency-Key HTTP header.
func Idempotency(cfg IdempotencyConfig) func(http.Handler) http.Handler {
	return idempotency.Middleware(cfg)
}
