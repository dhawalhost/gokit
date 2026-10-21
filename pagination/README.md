# Pagination (`gokit/pagination`)

`pagination` provides request parameter parsers and GORM query adapters for both offset-based and cursor-based pagination strategies.

---

## Quick Start

### Offset-Based Pagination

```go
package main

import (
    "net/http"

    "github.com/dhawalhost/gokit/pagination"
    "github.com/dhawalhost/gokit/response"
    "gorm.io/gorm"
)

type User struct {
    ID   string
    Name string
}

func ListUsersHandler(w http.ResponseWriter, r *http.Request, db *gorm.DB) {
    // 1. Parses 'page' (default 1) and 'limit' (default 20, max 100)
    p := pagination.ParseOffsetParams(r)

    var users []User
    var total int64

    // 2. Count total rows and apply Limit & Offset to query
    db.Model(&User{}).Count(&total)
    p.Apply(db).Find(&users)

    // 3. Return paginated JSON envelope
    response.Paginated(w, r, users, p.ToPagination(total))
}
```
