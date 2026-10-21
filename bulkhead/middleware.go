package bulkhead

import (
	"net/http"

	"github.com/dhawalhost/gokit/errors"
)

// KeyFunc extracts a partitioning key from an HTTP request (e.g. tenant_id, path, IP).
type KeyFunc func(r *http.Request) string

// Middleware creates an HTTP middleware that limits concurrent in-flight requests per key.
func Middleware(pb *PartitionedBulkhead, keyFunc KeyFunc) func(http.Handler) http.Handler {
	if keyFunc == nil {
		keyFunc = func(r *http.Request) string {
			return "global"
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFunc(r)
			b := pb.Get(key)

			err := b.Execute(r.Context(), func() error {
				next.ServeHTTP(w, r)
				return nil
			})

			if err != nil {
				if err == ErrBulkheadFull {
					w.Header().Set("Retry-After", "1")
					errors.WriteError(w, r, errors.TooManyRequests("CONCURRENCY_LIMIT_EXCEEDED", "too many concurrent requests for this resource"))
					return
				}
				errors.WriteError(w, r, errors.Internal(err.Error()))
				return
			}
		})
	}
}
