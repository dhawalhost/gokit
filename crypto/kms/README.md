# KMS Envelope Encryption (`gokit/crypto/kms`)

`crypto/kms` provides **Envelope Encryption** for sensitive data at rest using a two-tier key hierarchy: a root Key Encryption Key (KEK) managed by a Key Management Service (KMS), and unique, ephemeral Data Encryption Keys (DEKs) generated per record.

---

## The Pattern: Two-Tier Key Hierarchy

Encrypting millions of records with a single static master key poses high risk: if the key is compromised, all data is lost, and rotating the key requires re-encrypting the entire database.

Envelope encryption solves this:
1. For each record, an ephemeral 256-bit AES Data Encryption Key (DEK) is generated.
2. The plaintext payload is encrypted with the DEK using **AES-256-GCM** (AEAD).
3. The DEK itself is encrypted under the Master Key (KEK) via the KMS Provider.
4. The encrypted DEK, IV, and ciphertext are combined into a serialized envelope stored in the database.
5. Plaintext DEKs exist only in memory for microseconds and are never persisted.

```
       Plaintext Payload
              │
              ├──► [AES-256-GCM] ◄── Ephemeral DEK (Random 32-bytes)
              │          │                  │
              │          ▼                  ▼
              │      Ciphertext      [KMS Provider] ◄── Master Key (KEK)
              │          │                  │
              ▼          ▼                  ▼
        ┌──────────────────────────────────────────────┐
        │        Envelope Record Stored in DB          │
        │ [KeyID] : [Encrypted DEK] : [IV] : [Payload] │
        └──────────────────────────────────────────────┘
```

---

## Features

- **Authenticated Encryption with Associated Data (AEAD)**:
  - Binds cryptographic context (e.g. `tenant_id` or `record_id`) into the encryption process via Additional Authenticated Data (AAD).
  - Prevents ciphertext transposition attacks (ciphertext from Tenant A cannot be decrypted by Tenant B).
- **Pluggable Multi-Cloud KMS Providers**:
  - `LocalKeyProvider`: Fast, zero-dependency local AES-256 provider with multi-version key rotation support.
  - `AWSKMSProvider`: AWS Key Management Service (`kms:GenerateDataKey`, `kms:Decrypt`).
  - `GCPKMSProvider`: Google Cloud Key Management Service.
  - `VaultProvider`: HashiCorp Vault Transit Secrets Engine.
- **Zero-Downtime Key Rotation**:
  - Encrypts new records using the active primary key version while continuing to seamlessly decrypt older records using historical key versions.

---

## Quick Start

### 1. Initializing the Envelope Cipher

```go
package main

import (
    "context"
    "fmt"

    "github.com/dhawalhost/gokit/crypto/kms"
)

func main() {
    ctx := context.Background()

    // 1. Setup provider (Local keys for dev/testing, Cloud KMS for production)
    masterKeys := map[string][]byte{
        "key-v1": []byte("01234567890123456789012345678901"), // 32-byte AES key
    }
    provider, err := kms.NewLocalKeyProvider(masterKeys, "key-v1")
    if err != nil {
        panic(err)
    }

    cipher := kms.NewEnvelopeCipher(provider)

    // 2. Encrypt sensitive credit card number bound to tenant context
    aad := []byte("tenant:acme-corp")
    envelope, err := cipher.EncryptString(ctx, "key-v1", "4111-2222-3333-4444", aad)
    if err != nil {
        panic(err)
    }

    fmt.Println("Encrypted Base64 Envelope:", envelope)

    // 3. Decrypt payload back to plaintext
    decrypted, err := cipher.DecryptString(ctx, envelope, aad)
    if err != nil {
        panic(err)
    }

    fmt.Println("Decrypted Plaintext:", decrypted)
}
```

---

## Key Rotation Workflow

When rotating keys:
1. Add the new key version to the provider: `key-v2`.
2. Set the primary active key to `key-v2`.
3. Existing records tagged with `key-v1` remain decryptable without downtime.
4. All subsequent writes and updates are automatically encrypted with `key-v2`.
