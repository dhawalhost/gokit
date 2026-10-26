# `session`: Distributed Secure Session Management

`session` provides an enterprise-grade HTTP session management suite for Go microservices. It features pluggable storage backends (in-memory with TTL eviction, stateless AES-256-GCM encrypted cookies, and Redis adapters), anti-session-hijacking validation, sliding inactivity expiration, one-time flash data, and session regeneration to eliminate fixation vulnerabilities.

---

## Key Features

- **Pluggable Storage Backends**:
  - `MemoryStore`: High-performance in-memory cache with concurrent active eviction workers.
  - `CookieStore`: Stateless client-side encrypted sessions using authenticated AES-256-GCM.
- **Session Hijacking Defense**: Validates client IP subnet (`/24` IPv4, `/48` IPv6) and User-Agent fingerprint hashes across requests to detect stolen session tokens.
- **Session Fixation Immunity**: `session.Regenerate()` rotates the session token while preserving underlying session data upon privilege level elevation (e.g. login).
- **Dual Expiration Windows**:
  - **Idle Timeout**: Sliding window refreshed on active requests.
  - **Absolute Timeout**: Hard ceiling enforcing maximum total lifetime from session inception.
- **Flash Messages**: One-time messages automatically cleared upon initial retrieval (`session.Flashes(key)`).
- **Clean Middleware Integration**: Intercepts `http.ResponseWriter` to commit cookies before response headers or body streams are sent.

---

## Architecture Overview

```
                      Client HTTP Request
                               │
                               ▼
               session.Manager.Middleware()
                               │
               ┌───────────────┴───────────────┐
               ▼                               ▼
       Extract Cookie                  Generate / Load Session
               │                               │
               ▼                               ▼
     Validate Fingerprint             Inject Into Request Context
   (Subnet & User-Agent)                       │
               │                               ▼
               └──────────────────────> http.Handler (ServeHTTP)
                                               │
                                               ▼
                                      session.FromContext()
                                   (Get / Set / Flash / Destroy)
                                               │
                                               ▼
                                    sessionResponseWriter
                                  (Auto Commit Set-Cookie)
                                               │
                                               ▼
                                   Backend Storage Commit
                               (Memory / Encrypted Cookie / Redis)
```

---

## Quick Start

### 1. Initialize Session Manager

```go
package main

import (
    "net/http"
    "time"

    "github.com/dhawalhost/gokit/session"
)

func main() {
    // In-memory store with 1-minute expiration cleanup interval
    store := session.NewMemoryStore(time.Minute)
    defer store.Close()

    mgr, err := session.NewManager(store,
        session.WithCookieName("session_token"),
        session.WithCookieSecure(true),
        session.WithCookieHTTPOnly(true),
        session.WithCookieSameSite(http.SameSiteLaxMode),
        session.WithIdleTimeout(30*time.Minute),
        session.WithAbsoluteTimeout(24*time.Hour),
        session.WithAntiHijacking(true),
    )
    if err != nil {
        panic(err)
    }

    mux := http.NewServeMux()

    // 2. Authentication: Set session values & regenerate token
    mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
        s := session.FromContext(r.Context())
        s.Set("user_id", "usr_9918")
        s.Set("role", "admin")
        s.AddFlash("notice", "Successfully logged in")
        s.Regenerate() // Prevent session fixation
        w.Write([]byte("Logged in"))
    })

    // 3. Authenticated Route: Access session values & flash data
    mux.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
        s := session.FromContext(r.Context())
        userID := s.GetString("user_id")
        if userID == "" {
            http.Error(w, "Unauthorized", http.StatusUnauthorized)
            return
        }
        flashes := s.Flashes("notice")
        _ = flashes
        w.Write([]byte("Welcome " + userID))
    })

    // 4. Logout: Invalidate session and clear cookie
    mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
        s := session.FromContext(r.Context())
        s.Destroy()
        w.Write([]byte("Logged out"))
    })

    // Wrap application with session middleware
    http.ListenAndServe(":8080", mgr.Middleware()(mux))
}
```
