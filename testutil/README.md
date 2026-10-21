# Test Utilities (`gokit/testutil`)

`testutil` provides testing helpers for unit testing HTTP handlers and services, including synthetic JSON request generators, response recorders, mock loggers, and JSON assertion helpers.

---

## Features

- **Synthetic Request Factory (`NewTestRequest`)**: Creates mock `*http.Request` instances, automatically serializing JSON payloads and configuring headers.
- **Response Assertions (`AssertJSONResponse`)**: Asserts HTTP status codes and automatically unmarshals JSON response bodies into target structs.
- **Error Assertions (`AssertErrorResponse`)**: Asserts that error responses match expected domain error codes.
- **Mock Loggers (`NewTestLogger`)**: Supplies a no-op logger to avoid cluttering test outputs.

---

## Quick Start

```go
package user_test

import (
    "net/http"
    "testing"

    "github.com/dhawalhost/gokit/testutil"
)

type CreateUserRequest struct {
    Email string `json:"email"`
}

type UserResponse struct {
    ID    string `json:"id"`
    Email string `json:"email"`
}

func TestCreateUser(t *testing.T) {
    // 1. Create synthetic request and response recorder
    reqBody := CreateUserRequest{Email: "alice@example.com"}
    req := testutil.NewTestRequest(http.MethodPost, "/users", reqBody)
    rec := testutil.NewTestRecorder()

    // 2. Execute handler under test
    userHandler(rec, req)

    // 3. Assert status code and decode JSON body
    var resp UserResponse
    testutil.AssertJSONResponse(t, rec, http.StatusCreated, &resp)

    if resp.Email != "alice@example.com" {
        t.Errorf("expected email alice@example.com, got %s", resp.Email)
    }

    // 4. Assert error response
    // testutil.AssertErrorResponse(t, rec, http.StatusBadRequest, "INVALID_INPUT")
}
```
