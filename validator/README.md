# Validator (`gokit/validator`)

`validator` wraps `go-playground/validator/v10` to provide schema validation, JSON request body binding, custom validation tags, and human-readable error messages.

---

## Quick Start

```go
package main

import (
    "net/http"

    "github.com/dhawalhost/gokit/errors"
    "github.com/dhawalhost/gokit/validator"
)

type CreateInvoiceRequest struct {
    CustomerEmail string  `json:"customer_email" validate:"required,email"`
    Amount        float64 `json:"amount"         validate:"required,gt=0"`
    Currency      string  `json:"currency"       validate:"required,oneof=USD EUR GBP"`
}

func CreateInvoiceHandler(w http.ResponseWriter, r *http.Request) {
    v := validator.New()

    var req CreateInvoiceRequest
    // Binds JSON body and evaluates struct validation tags
    if err := v.Bind(r, &req); err != nil {
        errors.WriteError(w, r, errors.BadRequest("VALIDATION_FAILED", err.Error()))
        return
    }

    // Input is valid...
}
```
