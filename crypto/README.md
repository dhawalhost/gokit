# Crypto (`gokit/crypto`)

`crypto` provides production-grade cryptographic utilities for authenticated symmetric encryption (AES-256-GCM), bcrypt password hashing, constant-time HMAC signatures, JWT signing/verification, and PKCE challenge verification.

---

## Features

- **AES-256-GCM**: Authenticated symmetric encryption preventing ciphertext tampering.
- **Bcrypt Passwords**: Adaptive-cost password hashing resistant to GPU rainbow table cracking.
- **JWT HS256/RS256**: Standards-compliant token signing and verification with claim validation.
- **Timing-Safe PKCE**: RFC 7636 Proof Key for Code Exchange generators and verifiers for OAuth2 flows.

---

## Quick Start

```go
package main

import (
    "fmt"

    "github.com/dhawalhost/gokit/crypto"
)

func main() {
    key32 := []byte("01234567890123456789012345678901") // 32-byte AES key

    // 1. AES-256-GCM Encryption
    ciphertext, _ := crypto.EncryptString("secret message", key32)
    plaintext, _ := crypto.DecryptString(ciphertext, key32)
    fmt.Println("Decrypted:", plaintext)

    // 2. Password Hashing (Bcrypt)
    hash, _ := crypto.HashPassword("user-password-123")
    err := crypto.CheckPassword("user-password-123", hash) // nil if matches

    // 3. PKCE OAuth2 Verification
    verifier, _ := crypto.GenerateCodeVerifier()
    challenge := crypto.GenerateCodeChallenge(verifier)
    isValid := crypto.VerifyCodeChallenge(verifier, challenge) // true
    fmt.Println("PKCE Valid:", isValid)
}
```
