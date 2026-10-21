# Errors (`gokit/errors`)

`errors` provides standardized domain errors with HTTP status code mapping, machine-readable error codes, metadata detail attachment, and direct JSON response serialization.

---

## Standard Error Codes

| Constructor | HTTP Status | Code | Typical Use Case |
|---|---|---|---|
| `errors.NotFound` | `404` | `"NOT_FOUND"` | Entity, row, or route does not exist |
| `errors.BadRequest` | `400` | `"BAD_REQUEST"` | Validation failed or malformed request body |
| `errors.Unauthorized`| `401` | `"UNAUTHORIZED"`| Missing, invalid, or expired authentication credentials |
| `errors.Forbidden` | `403` | `"FORBIDDEN"` | Authenticated actor lacks required permission or tenant boundary |
| `errors.Conflict` | `409` | `"CONFLICT"` | Duplicate key or state conflict |
| `errors.Internal` | `500` | `"INTERNAL"` | Unexpected server or database error |

---

## Quick Start

```go
package main

import (
    "net/http"

    "github.com/dhawalhost/gokit/errors"
)

func GetUserHandler(w http.ResponseWriter, r *http.Request) {
    userID := r.URL.Query().Get("id")
    if userID == "" {
        err := errors.BadRequest("MISSING_PARAMETER", "user id is required")
        errors.WriteError(w, r, err)
        return
    }

    user, err := findUser(userID)
    if err != nil {
        notFoundErr := errors.NotFound("USER_NOT_FOUND", "user does not exist")
        notFoundErr = errors.WithDetails(notFoundErr, map[string]string{"id": userID})
        errors.WriteError(w, r, notFoundErr)
        return
    }

    // Success response...
}
```
