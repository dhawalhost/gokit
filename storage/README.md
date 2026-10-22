# Cloud-Agnostic Object Storage (`gokit/storage`)

`storage` provides a unified, production-grade object storage abstraction for microservices. It seamlessly decouples application business logic from specific cloud storage vendors, allowing services to run against AWS S3, Cloudflare R2, MinIO, the local filesystem, or an in-memory mock backend without code changes.

---

## Features

- **Unified Interface**: Single API contract for `Put`, `Get`, `Delete`, `DeleteMulti`, `Exists`, `Stat`, `List`, `PresignGet`, and `PresignPut`.
- **Multi-Cloud S3-Compatible Driver**: Works out-of-the-box with AWS S3, Cloudflare R2, Google Cloud Storage (via S3 interop), and MinIO using `aws-sdk-go-v2`.
- **Hardened Local FileSystem Driver**:
  - **Atomic Writes**: Writes to `.upload-*` temporary files in the target directory and renames atomically upon completion, preventing partially written files.
  - **Path Traversal Protection**: Enforces strict canonical key sandboxing, rejecting `..`, root prefixes, and null bytes with `ErrInvalidKey`.
  - **Embedded HTTP Handler (`HTTPHandler()`)**: Serves and receives HMAC-SHA256 presigned uploads and downloads directly without requiring external object store daemons in local dev/testing.
- **Thread-Safe In-Memory Driver**: Fast, zero-dependency storage engine for unit and integration testing with built-in HMAC presigning and byte range slicing.
- **Byte Range Queries**: Supports `WithRange(start, end)` for video/audio streaming, resumable downloads, and HTTP 206 partial responses.
- **Pagination & Hierarchical Listing**: Common prefix delimiter support (folder simulation) and cursor-based pagination tokens (`WithMaxKeys`, `WithContinuationToken`, `WithDelimiter`).

---

## Architecture

```
                       ┌─────────────────────────┐
                       │     gokit/storage       │
                       │    Storage Interface    │
                       └────────────┬────────────┘
                                    │
         ┌──────────────────────────┼──────────────────────────┐
         ▼                          ▼                          ▼
┌──────────────────┐       ┌──────────────────┐       ┌──────────────────┐
│   S3 Compatible  │       │ Local FileSystem │       │ In-Memory Mock   │
│  AWS S3 / MinIO  │       │  Atomic Writes   │       │ Thread-Safe Map  │
│  Cloudflare R2   │       │  HTTPHandler()   │       │  Unit Testing    │
└──────────────────┘       └──────────────────┘       └──────────────────┘
```

---

## Quick Start

### 1. AWS S3 / Cloudflare R2 / MinIO

```go
package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dhawalhost/gokit/storage"
)

func main() {
	ctx := context.Background()

	// Connect to AWS S3 (or MinIO / Cloudflare R2 via Endpoint)
	store, err := storage.NewS3(ctx, storage.S3Config{
		Bucket:          "my-production-bucket",
		Region:          "us-east-1",
		Endpoint:        "http://minio:9000", // Optional: MinIO or R2 endpoint
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
		UsePathStyle:    true,                // Required for MinIO and local testing
	})
	if err != nil {
		panic(err)
	}

	// 1. Upload an object with metadata
	payload := strings.NewReader("Hello, world!")
	info, err := store.Put(ctx, "reports/2026/q1.txt", payload,
		storage.WithContentType("text/plain"),
		storage.WithMetadata(map[string]string{
			"author": "audit-team",
		}),
	)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Uploaded: %s (ETag: %s, Size: %d)\n", info.Key, info.ETag, info.Size)

	// 2. Generate a Presigned Download URL (SigV4)
	downloadURL, err := store.PresignGet(ctx, "reports/2026/q1.txt", 15*time.Minute)
	if err != nil {
		panic(err)
	}
	fmt.Println("Presigned Download URL:", downloadURL)

	// 3. Generate a Presigned Upload URL (Direct Client-to-S3 Upload)
	uploadURL, err := store.PresignPut(ctx, "uploads/avatar.png", 10*time.Minute,
		storage.WithPresignContentType("image/png"),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println("Presigned Upload URL:", uploadURL)
}
```

---

### 2. Local FileSystem Storage with Direct HTTP Server

The filesystem driver can host its own HTTP handler to handle presigned uploads and downloads seamlessly in staging or on-prem environments:

```go
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/dhawalhost/gokit/storage"
)

func main() {
	ctx := context.Background()

	fsStore, err := storage.NewFileSystem("/var/data/uploads",
		storage.WithFSBaseURL("http://localhost:8080/storage"),
		storage.WithFSPresignSecret([]byte("32-byte-secure-hmac-secret-key!!")),
	)
	if err != nil {
		panic(err)
	}

	// Mount storage HTTP handler on router
	http.Handle("/storage/", http.StripPrefix("/storage", fsStore.HTTPHandler()))

	// Client can upload directly via HMAC presigned URL
	uploadURL, _ := fsStore.PresignPut(ctx, "user-123/profile.jpg", 15*time.Minute)
	// PUT http://localhost:8080/storage/user-123/profile.jpg?expires=...&sig=...

	_ = http.ListenAndServe(":8080", nil)
}
```

---

### 3. In-Memory Storage for Unit Testing

Use `NewMemory()` in your unit tests to avoid starting external S3 or mock containers:

```go
func TestUserUpload(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemory()

	service := NewMediaService(store)
	err := service.UploadUserAvatar(ctx, "user-42", avatarBytes)
	assert.NoError(t, err)

	exists, err := store.Exists(ctx, "avatars/user-42.png")
	assert.True(t, exists)
}
```

---

### 4. Byte Range Requests (Streaming & Resumable Downloads)

```go
// Fetch only bytes 0 through 1024 of a large video or file
reader, info, err := store.Get(ctx, "media/video.mp4", storage.WithRange(0, 1024))
if err != nil {
	panic(err)
}
defer reader.Close()

fmt.Printf("Received partial content (%d bytes)\n", info.Size)
```

---

### 5. Hierarchical Folder Listing with Delimiters

```go
res, err := store.List(ctx, "documents/",
	storage.WithDelimiter("/"),
	storage.WithMaxKeys(50),
)
if err != nil {
	panic(err)
}

// "Subfolders"
for _, prefix := range res.CommonPrefixes {
	fmt.Println("Folder:", prefix)
}

// Objects in current directory
for _, obj := range res.Objects {
	fmt.Printf("File: %s (%d bytes)\n", obj.Key, obj.Size)
}

if res.IsTruncated {
	fmt.Println("Next page token:", res.NextContinuationToken)
}
```

---

## Sentinel Errors

| Error | Description |
|---|---|
| `ErrNotFound` | The requested object does not exist in the store |
| `ErrInvalidKey` | The key is empty, root-prefixed, or contains directory traversal sequences (`..`) |
| `ErrInvalidRange` | The requested byte range is invalid or out of file bounds |
| `ErrPresignExpired` | The presigned URL timestamp has expired |
| `ErrPresignSignatureMismatch` | The presigned URL HMAC signature is invalid or tampered |
