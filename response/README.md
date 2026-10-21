# Response (`gokit/response`)

`response` provides type-safe generic response envelopes for REST APIs in Go, eliminating boilerplate JSON marshaling and header writing.

---

## Response Formats

### Standard Response Envelope
```json
{
  "data": { "id": "usr-1", "name": "Alice" }
}
```

### Paginated Response Envelope
```json
{
  "data": [ ... ],
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 150,
    "total_pages": 8
  }
}
```

---

## Quick Start

```go
package main

import (
    "net/http"

    "github.com/dhawalhost/gokit/response"
)

type User struct {
    ID   string `json:"id"`
    Name string `json:"name"`
}

func UserHandlers(w http.ResponseWriter, r *http.Request) {
    user := User{ID: "usr-1", Name: "Alice"}

    // 200 OK
    response.Ok(w, r, user)

    // 201 Created
    // response.Created(w, r, user)

    // 204 No Content
    // response.NoContent(w)

    // 200 OK Paginated
    // response.Paginated(w, r, usersList, paginationMetadata)
}
```
