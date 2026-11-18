package s3_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/storage"
	"github.com/dhawalhost/gokit/storage/s3"
)

func TestS3Config_Validation_TableDriven(t *testing.T) {
	tests := []struct {
		name    string
		cfg     s3.Config
		wantErr bool
	}{
		{
			name:    "empty bucket returns error",
			cfg:     s3.Config{Bucket: ""},
			wantErr: true,
		},
		{
			name: "valid bucket and custom endpoint initializes",
			cfg: s3.Config{
				Bucket:          "my-bucket",
				Region:          "us-west-2",
				Endpoint:        "http://localhost:9000",
				AccessKeyID:     "minioadmin",
				SecretAccessKey: "minioadmin",
				UsePathStyle:    true,
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s3.New(context.Background(), tc.cfg)
			if (err != nil) != tc.wantErr {
				t.Errorf("New() wantErr=%v, got=%v", tc.wantErr, err)
			}
		})
	}
}

func TestS3Storage_MockServer_TableDriven(t *testing.T) {
	ctx := context.Background()
	data := make(map[string][]byte)
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		key := strings.TrimPrefix(r.URL.Path, "/test-bucket/")

		// S3 ListObjectsV2: GET /test-bucket?list-type=2
		if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			var xmlBuf bytes.Buffer
			xmlBuf.WriteString(`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
			xmlBuf.WriteString(`<Name>test-bucket</Name>`)
			xmlBuf.WriteString(`<IsTruncated>false</IsTruncated>`)
			for k, v := range data {
				xmlBuf.WriteString(fmt.Sprintf(`<Contents><Key>%s</Key><Size>%d</Size><ETag>"etag-%s"</ETag></Contents>`, k, len(v), k))
			}
			xmlBuf.WriteString(`</ListBucketResult>`)
			_, _ = w.Write(xmlBuf.Bytes())
			return
		}

		// S3 DeleteObjects: POST /test-bucket?delete
		if r.Method == http.MethodPost && (r.URL.Query().Has("delete") || strings.Contains(r.URL.RawQuery, "delete")) {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<DeleteResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></DeleteResult>`))
			return
		}

		switch r.Method {
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			data[key] = b
			w.Header().Set("ETag", `"etag-123"`)
			w.WriteHeader(http.StatusOK)

		case http.MethodGet:
			val, exists := data[key]
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`<Error><Code>NoSuchKey</Code></Error>`))
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("ETag", `"etag-123"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(val)

		case http.MethodHead:
			val, exists := data[key]
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(val)))
			w.Header().Set("ETag", `"etag-123"`)
			w.WriteHeader(http.StatusOK)

		case http.MethodDelete:
			if key == "mock-error-delete" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`<Error><Code>InternalError</Code></Error>`))
				return
			}
			delete(data, key)
			w.WriteHeader(http.StatusNoContent)

		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer ts.Close()

	s3Store, err := s3.NewS3(ctx, s3.S3Config{
		Bucket:          "test-bucket",
		Region:          "us-east-1",
		Endpoint:        ts.URL,
		AccessKeyID:     "test-key",
		SecretAccessKey: "test-secret",
		UsePathStyle:    true,
	})
	if err != nil {
		t.Fatalf("NewS3 error: %v", err)
	}

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "S3 Put, Get, Exists, Stat, and Delete lifecycle",
			run: func(t *testing.T) {
				payload := []byte("s3 payload data")
				info, err := s3Store.Put(ctx, "items/doc.txt", bytes.NewReader(payload),
					storage.WithContentType("text/plain"),
					storage.WithContentLength(int64(len(payload))),
				)
				if err != nil {
					t.Fatalf("Put error: %v", err)
				}
				if info.Key != "items/doc.txt" || info.ETag != "etag-123" {
					t.Errorf("unexpected Put info: %+v", info)
				}

				exists, err := s3Store.Exists(ctx, "items/doc.txt")
				if err != nil || !exists {
					t.Errorf("expected exists=true, got %v, err=%v", exists, err)
				}

				stat, err := s3Store.Stat(ctx, "items/doc.txt")
				if err != nil || stat.ETag != "etag-123" {
					t.Errorf("Stat error or mismatch: %+v, err=%v", stat, err)
				}

				rc, getInfo, err := s3Store.Get(ctx, "items/doc.txt")
				if err != nil {
					t.Fatalf("Get error: %v", err)
				}
				defer rc.Close()
				data, _ := io.ReadAll(rc)
				if string(data) != string(payload) {
					t.Errorf("expected %q, got %q", string(payload), string(data))
				}
				if getInfo.ETag != "etag-123" {
					t.Errorf("expected etag-123, got %s", getInfo.ETag)
				}

				rcRange, _, err := s3Store.Get(ctx, "items/doc.txt", storage.WithRange(0, 5))
				if err != nil {
					t.Fatalf("Get range error: %v", err)
				}
				rcRange.Close()

				if err := s3Store.Delete(ctx, "items/doc.txt"); err != nil {
					t.Fatalf("Delete error: %v", err)
				}

				exists, _ = s3Store.Exists(ctx, "items/doc.txt")
				if exists {
					t.Error("expected object to be gone after delete")
				}
			},
		},
		{
			name: "S3 List and DeleteMulti",
			run: func(t *testing.T) {
				_, _ = s3Store.Put(ctx, "batch/1.txt", strings.NewReader("1"))
				_, _ = s3Store.Put(ctx, "batch/2.txt", strings.NewReader("2"))

				listRes, err := s3Store.List(ctx, "batch/", storage.WithMaxKeys(10))
				if err != nil {
					t.Fatalf("List error: %v", err)
				}
				if len(listRes.Objects) == 0 {
					t.Error("expected objects in list response")
				}

				if err := s3Store.DeleteMulti(ctx, "batch/1.txt", "batch/2.txt"); err != nil {
					t.Fatalf("DeleteMulti error: %v", err)
				}
			},
		},
		{
			name: "S3 PresignGet and PresignPut generate signed URLs",
			run: func(t *testing.T) {
				getUrl, err := s3Store.PresignGet(ctx, "asset.png", 15*time.Minute)
				if err != nil {
					t.Fatalf("PresignGet error: %v", err)
				}
				if !strings.Contains(getUrl, "asset.png") || !strings.Contains(getUrl, "X-Amz-Signature") {
					t.Errorf("unexpected presigned get url: %s", getUrl)
				}

				putUrl, err := s3Store.PresignPut(ctx, "upload.jpg", 15*time.Minute, storage.WithPresignContentType("image/jpeg"))
				if err != nil {
					t.Fatalf("PresignPut error: %v", err)
				}
				if !strings.Contains(putUrl, "upload.jpg") || !strings.Contains(putUrl, "X-Amz-Signature") {
					t.Errorf("unexpected presigned put url: %s", putUrl)
				}
			},
		},
		{
			name: "S3 invalid keys return ErrInvalidKey",
			run: func(t *testing.T) {
				if _, err := s3Store.Put(ctx, "", strings.NewReader("")); err != storage.ErrInvalidKey {
					t.Errorf("Put: expected ErrInvalidKey, got %v", err)
				}
				if _, _, err := s3Store.Get(ctx, ""); err != storage.ErrInvalidKey {
					t.Errorf("Get: expected ErrInvalidKey, got %v", err)
				}
				if err := s3Store.Delete(ctx, ""); err != storage.ErrInvalidKey {
					t.Errorf("Delete: expected ErrInvalidKey, got %v", err)
				}
				if _, err := s3Store.Exists(ctx, ""); err != storage.ErrInvalidKey {
					t.Errorf("Exists: expected ErrInvalidKey, got %v", err)
				}
				if _, err := s3Store.Stat(ctx, ""); err != storage.ErrInvalidKey {
					t.Errorf("Stat: expected ErrInvalidKey, got %v", err)
				}
				if _, err := s3Store.PresignGet(ctx, "", time.Minute); err != storage.ErrInvalidKey {
					t.Errorf("PresignGet: expected ErrInvalidKey, got %v", err)
				}
				if _, err := s3Store.PresignPut(ctx, "", time.Minute); err != storage.ErrInvalidKey {
					t.Errorf("PresignPut: expected ErrInvalidKey, got %v", err)
				}
			},
		},
		{
			name: "S3 edge cases and error handling",
			run: func(t *testing.T) {
				if _, _, err := s3Store.Get(ctx, "some-key", storage.WithRange(-1, 5)); err != storage.ErrInvalidRange {
					t.Errorf("expected ErrInvalidRange, got %v", err)
				}
				if _, _, err := s3Store.Get(ctx, "some-key", storage.WithRange(10, 5)); err != storage.ErrInvalidRange {
					t.Errorf("expected ErrInvalidRange, got %v", err)
				}

				if _, _, err := s3Store.Get(ctx, "missing.txt"); err != storage.ErrNotFound {
					t.Errorf("expected ErrNotFound on Get, got %v", err)
				}

				if _, err := s3Store.Stat(ctx, "missing.txt"); err != storage.ErrNotFound {
					t.Errorf("expected ErrNotFound on Stat, got %v", err)
				}

				if err := s3Store.Delete(ctx, "mock-error-delete"); err == nil {
					t.Errorf("expected error on mock-error-delete, got nil")
				}

				if err := s3Store.DeleteMulti(ctx); err != nil {
					t.Errorf("expected nil for empty DeleteMulti, got %v", err)
				}
				if err := s3Store.DeleteMulti(ctx, "   "); err != nil {
					t.Errorf("expected nil for whitespace DeleteMulti, got %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t)
		})
	}
}
