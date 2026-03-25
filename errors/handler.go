package errors

import (
	"encoding/json"
	"net/http"
)

// WriteError writes an appropriate JSON error response for err.
// If err is an *AppError the status code and body come from it; otherwise a
// generic 500 is returned.
func WriteError(w http.ResponseWriter, _ *http.Request, err error) {
	ae, ok := IsAppError(err)
	if !ok {
		ae = Internal("an unexpected error occurred")
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ae.HTTPStatus)
	_ = json.NewEncoder(w).Encode(ae)
}
