package storage_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/storage"
)

func TestMemoryStorage_TableDriven(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(t *testing.T, s storage.Storage)
	}{
		{
			name: "Put and Get object lifecycle with metadata",
			run: func(t *testing.T, s storage.Storage) {
				payload := []byte("hello storage world")
				info, err := s.Put(ctx, "avatars/u1.png", bytes.NewReader(payload),
					storage.WithContentType("image/png"),
					storage.WithMetadata(map[string]string{"user_id": "123"}),
				)
				if err != nil {
					t.Fatalf("Put error: %v", err)
				}
				if info.Key != "avatars/u1.png" || info.Size != int64(len(payload)) || info.ContentType != "image/png" {
					t.Errorf("unexpected info: %+v", info)
				}
				if info.Metadata["user_id"] != "123" {
					t.Errorf("expected user_id 123, got %s", info.Metadata["user_id"])
				}

				// Exists
				exists, err := s.Exists(ctx, "avatars/u1.png")
				if err != nil || !exists {
					t.Errorf("expected exists=true, got %v, err=%v", exists, err)
				}

				// Stat
				stat, err := s.Stat(ctx, "avatars/u1.png")
				if err != nil || stat.ETag != info.ETag {
					t.Errorf("stat mismatch, got %+v", stat)
				}

				// Get
				rc, getInfo, err := s.Get(ctx, "avatars/u1.png")
				if err != nil {
					t.Fatalf("Get error: %v", err)
				}
				defer rc.Close()

				body, _ := io.ReadAll(rc)
				if string(body) != string(payload) {
					t.Errorf("expected payload %q, got %q", string(payload), string(body))
				}
				if getInfo.ETag != info.ETag {
					t.Errorf("etag mismatch, got %s", getInfo.ETag)
				}

				// Delete
				if err := s.Delete(ctx, "avatars/u1.png"); err != nil {
					t.Fatalf("Delete error: %v", err)
				}

				// Verify gone
				exists, _ = s.Exists(ctx, "avatars/u1.png")
				if exists {
					t.Error("expected object to be deleted")
				}
				_, _, err = s.Get(ctx, "avatars/u1.png")
				if err != storage.ErrNotFound {
					t.Errorf("expected ErrNotFound, got %v", err)
				}
			},
		},
		{
			name: "Range queries return sliced byte payload",
			run: func(t *testing.T, s storage.Storage) {
				payload := []byte("0123456789abcdef")
				_, _ = s.Put(ctx, "range.txt", bytes.NewReader(payload))

				// Valid range [0, 4] -> "01234"
				rc, info, err := s.Get(ctx, "range.txt", storage.WithRange(0, 4))
				if err != nil {
					t.Fatalf("Get range error: %v", err)
				}
				defer rc.Close()
				data, _ := io.ReadAll(rc)
				if string(data) != "01234" {
					t.Errorf("expected '01234', got %q", string(data))
				}
				if info.Size != 5 {
					t.Errorf("expected size 5, got %d", info.Size)
				}

				// Over-bound end clamped to end of content
				rc2, _, err := s.Get(ctx, "range.txt", storage.WithRange(10, 100))
				if err != nil {
					t.Fatalf("Get clamped range error: %v", err)
				}
				defer rc2.Close()
				data2, _ := io.ReadAll(rc2)
				if string(data2) != "abcdef" {
					t.Errorf("expected 'abcdef', got %q", string(data2))
				}

				// Invalid range
				_, _, err = s.Get(ctx, "range.txt", storage.WithRange(-1, 5))
				if err != storage.ErrInvalidRange {
					t.Errorf("expected ErrInvalidRange for start < 0, got %v", err)
				}
				_, _, err = s.Get(ctx, "range.txt", storage.WithRange(10, 5))
				if err != storage.ErrInvalidRange {
					t.Errorf("expected ErrInvalidRange for end < start, got %v", err)
				}
				_, _, err = s.Get(ctx, "range.txt", storage.WithRange(100, 200))
				if err != storage.ErrInvalidRange {
					t.Errorf("expected ErrInvalidRange for start >= len, got %v", err)
				}
			},
		},
		{
			name: "Invalid key inputs return ErrInvalidKey",
			run: func(t *testing.T, s storage.Storage) {
				if _, err := s.Put(ctx, "  ", strings.NewReader("")); err != storage.ErrInvalidKey {
					t.Errorf("Put: expected ErrInvalidKey, got %v", err)
				}
				if _, _, err := s.Get(ctx, ""); err != storage.ErrInvalidKey {
					t.Errorf("Get: expected ErrInvalidKey, got %v", err)
				}
				if err := s.Delete(ctx, ""); err != storage.ErrInvalidKey {
					t.Errorf("Delete: expected ErrInvalidKey, got %v", err)
				}
				if _, err := s.Exists(ctx, ""); err != storage.ErrInvalidKey {
					t.Errorf("Exists: expected ErrInvalidKey, got %v", err)
				}
				if _, err := s.Stat(ctx, ""); err != storage.ErrInvalidKey {
					t.Errorf("Stat: expected ErrInvalidKey, got %v", err)
				}
			},
		},
		{
			name: "Listing with prefixes, delimiters, and pagination",
			run: func(t *testing.T, s storage.Storage) {
				files := []string{
					"docs/2026/q1.pdf",
					"docs/2026/q2.pdf",
					"docs/archive/old.zip",
					"docs/readme.txt",
					"images/logo.png",
				}
				for _, f := range files {
					_, _ = s.Put(ctx, f, strings.NewReader("content"))
				}

				// List with delimiter "/"
				res, err := s.List(ctx, "docs/", storage.WithDelimiter("/"))
				if err != nil {
					t.Fatalf("List error: %v", err)
				}
				if len(res.Objects) != 1 || res.Objects[0].Key != "docs/readme.txt" {
					t.Errorf("expected docs/readme.txt in objects, got %+v", res.Objects)
				}
				if len(res.CommonPrefixes) != 2 {
					t.Fatalf("expected 2 common prefixes, got %d: %v", len(res.CommonPrefixes), res.CommonPrefixes)
				}

				// List with pagination maxKeys
				resPage1, err := s.List(ctx, "docs/", storage.WithMaxKeys(2))
				if err != nil {
					t.Fatalf("List page 1 error: %v", err)
				}
				if len(resPage1.Objects) != 2 || !resPage1.IsTruncated || resPage1.NextContinuationToken == "" {
					t.Fatalf("expected truncated page with token, got %+v", resPage1)
				}

				resPage2, err := s.List(ctx, "docs/", storage.WithMaxKeys(2), storage.WithContinuationToken(resPage1.NextContinuationToken))
				if err != nil {
					t.Fatalf("List page 2 error: %v", err)
				}
				if len(resPage2.Objects) != 2 {
					t.Fatalf("expected 2 items on page 2, got %d", len(resPage2.Objects))
				}

				// Clean up
				_ = s.DeleteMulti(ctx, files...)
			},
		},
		{
			name: "Presigned URL generation and verification",
			run: func(t *testing.T, s storage.Storage) {
				secret := []byte("gokit-memory-storage-presign-secret")

				getUrl, err := s.PresignGet(ctx, "report.pdf", 10*time.Minute)
				if err != nil {
					t.Fatalf("PresignGet error: %v", err)
				}
				method, key, err := storage.VerifyPresignedURL(getUrl, secret)
				if err != nil || method != "GET" || key != "report.pdf" {
					t.Errorf("VerifyPresignedURL Get failed: method=%s, key=%s, err=%v", method, key, err)
				}

				putUrl, err := s.PresignPut(ctx, "upload.png", 5*time.Minute, storage.WithPresignContentType("image/png"))
				if err != nil {
					t.Fatalf("PresignPut error: %v", err)
				}
				method, key, err = storage.VerifyPresignedURL(putUrl, secret)
				if err != nil || method != "PUT" || key != "upload.png" {
					t.Errorf("VerifyPresignedURL Put failed: method=%s, key=%s, err=%v", method, key, err)
				}

				// Expired signature
				expiredUrl, _ := s.PresignGet(ctx, "doc.pdf", -1*time.Minute)
				_, _, err = storage.VerifyPresignedURL(expiredUrl, secret)
				if err != storage.ErrPresignExpired {
					t.Errorf("expected ErrPresignExpired, got %v", err)
				}

				// Tampered signature
				tampered := strings.Replace(getUrl, "sig=", "sig=invalid", 1)
				_, _, err = storage.VerifyPresignedURL(tampered, secret)
				if err != storage.ErrPresignSignatureMismatch {
					t.Errorf("expected ErrPresignSignatureMismatch, got %v", err)
				}

				// Missing query params
				_, _, err = storage.VerifyPresignedURL("http://localhost/path", secret)
				if err != storage.ErrPresignSignatureMismatch {
					t.Errorf("expected ErrPresignSignatureMismatch for missing params, got %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mem := storage.NewMemory()
			tc.run(t, mem)
		})
	}
}

func TestFileSystemStorage_TableDriven(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(t *testing.T, fs *storage.FileSystemStorage)
	}{
		{
			name: "FileSystem full lifecycle with atomic writes and cleanup",
			run: func(t *testing.T, fs *storage.FileSystemStorage) {
				key := "reports/2026/annual.json"
				content := `{"revenue": 1000000}`
				info, err := fs.Put(ctx, key, strings.NewReader(content), storage.WithContentType("application/json"))
				if err != nil {
					t.Fatalf("Put error: %v", err)
				}
				if info.Key != key || info.Size != int64(len(content)) {
					t.Errorf("unexpected Put info: %+v", info)
				}

				// Exists
				exists, err := fs.Exists(ctx, key)
				if err != nil || !exists {
					t.Errorf("expected exists=true, got %v", exists)
				}

				// Stat
				stat, err := fs.Stat(ctx, key)
				if err != nil || stat.Size != int64(len(content)) {
					t.Errorf("Stat failed: %+v", stat)
				}

				// Get
				rc, getInfo, err := fs.Get(ctx, key)
				if err != nil {
					t.Fatalf("Get error: %v", err)
				}
				defer rc.Close()
				data, _ := io.ReadAll(rc)
				if string(data) != content {
					t.Errorf("expected %s, got %s", content, string(data))
				}
				if getInfo.ContentType != "application/json" {
					t.Errorf("expected application/json, got %s", getInfo.ContentType)
				}

				// Delete
				if err := fs.Delete(ctx, key); err != nil {
					t.Fatalf("Delete error: %v", err)
				}
				exists, _ = fs.Exists(ctx, key)
				if exists {
					t.Error("expected deleted file")
				}
			},
		},
		{
			name: "Path traversal attacks are blocked with ErrInvalidKey",
			run: func(t *testing.T, fs *storage.FileSystemStorage) {
				maliciousKeys := []string{
					"../../etc/passwd",
					"/etc/shadow",
					"subdir/../../../escape.txt",
					"....//test",
					"",
				}
				for _, k := range maliciousKeys {
					if _, err := fs.Put(ctx, k, strings.NewReader("bad")); err != storage.ErrInvalidKey {
						t.Errorf("Put(%q): expected ErrInvalidKey, got %v", k, err)
					}
					if _, _, err := fs.Get(ctx, k); err != storage.ErrInvalidKey {
						t.Errorf("Get(%q): expected ErrInvalidKey, got %v", k, err)
					}
					if err := fs.Delete(ctx, k); err != storage.ErrInvalidKey {
						t.Errorf("Delete(%q): expected ErrInvalidKey, got %v", k, err)
					}
				}
			},
		},
		{
			name: "Range queries read partial slice from file",
			run: func(t *testing.T, fs *storage.FileSystemStorage) {
				key := "media/sample.txt"
				payload := "abcdefghijklmnopqrstuvwxyz"
				_, _ = fs.Put(ctx, key, strings.NewReader(payload))

				rc, info, err := fs.Get(ctx, key, storage.WithRange(3, 7))
				if err != nil {
					t.Fatalf("Range get error: %v", err)
				}
				defer rc.Close()

				data, _ := io.ReadAll(rc)
				if string(data) != "defgh" {
					t.Errorf("expected 'defgh', got %q", string(data))
				}
				if info.Size != 5 {
					t.Errorf("expected size 5, got %d", info.Size)
				}

				// Invalid range
				_, _, err = fs.Get(ctx, key, storage.WithRange(-1, 5))
				if err != storage.ErrInvalidRange {
					t.Errorf("expected ErrInvalidRange, got %v", err)
				}
			},
		},
		{
			name: "Listing directory files with delimiter",
			run: func(t *testing.T, fs *storage.FileSystemStorage) {
				files := []string{
					"assets/img1.png",
					"assets/img2.png",
					"assets/nested/sub.png",
				}
				for _, f := range files {
					_, _ = fs.Put(ctx, f, strings.NewReader("png"))
				}

				res, err := fs.List(ctx, "assets/", storage.WithDelimiter("/"))
				if err != nil {
					t.Fatalf("List error: %v", err)
				}
				if len(res.Objects) != 2 {
					t.Errorf("expected 2 root objects, got %d", len(res.Objects))
				}
				if len(res.CommonPrefixes) != 1 || res.CommonPrefixes[0] != "assets/nested/" {
					t.Errorf("expected common prefix 'assets/nested/', got %v", res.CommonPrefixes)
				}
			},
		},
		{
			name: "HTTPHandler serves and receives presigned uploads/downloads",
			run: func(t *testing.T, _ *storage.FileSystemStorage) {
				secret := []byte("test-http-secret")
				fsWithURL, err := storage.NewFileSystem(t.TempDir(),
					storage.WithFSPresignSecret(secret),
				)
				if err != nil {
					t.Fatalf("NewFileSystem error: %v", err)
				}

				ts := httptest.NewServer(fsWithURL.HTTPHandler())
				defer ts.Close()

				// 1. Upload via PresignPut
				putURL, err := fsWithURL.PresignPut(ctx, "uploads/avatar.png", 5*time.Minute, storage.WithPresignContentType("image/png"))
				if err != nil {
					t.Fatalf("PresignPut error: %v", err)
				}
				putURL = strings.Replace(putURL, "http://localhost:8080", ts.URL, 1)

				req, err := http.NewRequest(http.MethodPut, putURL, strings.NewReader("image-bytes-123"))
				if err != nil {
					t.Fatalf("NewRequest PUT failed: %v", err)
				}
				req.Header.Set("Content-Type", "image/png")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("Upload PUT failed: %v", err)
				}
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("expected status 200 on upload, got %d", resp.StatusCode)
				}

				// 2. Download via PresignGet
				getURL, err := fsWithURL.PresignGet(ctx, "uploads/avatar.png", 5*time.Minute)
				if err != nil {
					t.Fatalf("PresignGet error: %v", err)
				}
				getURL = strings.Replace(getURL, "http://localhost:8080", ts.URL, 1)

				getResp, err := http.Get(getURL)
				if err != nil {
					t.Fatalf("Download GET failed: %v", err)
				}
				defer getResp.Body.Close()
				if getResp.StatusCode != http.StatusOK {
					t.Fatalf("expected status 200 on download, got %d", getResp.StatusCode)
				}
				data, _ := io.ReadAll(getResp.Body)
				if string(data) != "image-bytes-123" {
					t.Errorf("expected downloaded data 'image-bytes-123', got %q", string(data))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			fs, err := storage.NewFileSystem(dir)
			if err != nil {
				t.Fatalf("NewFileSystem error: %v", err)
			}
			tc.run(t, fs)
		})
	}
}

func TestFileSystemStorage_EdgeCases_TableDriven(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(t *testing.T, fs *storage.FileSystemStorage)
	}{
		{
			name: "DeleteMulti and WithContentLength option",
			run: func(t *testing.T, fs *storage.FileSystemStorage) {
				_, _ = fs.Put(ctx, "k1.txt", strings.NewReader("1"), storage.WithContentLength(1))
				_, _ = fs.Put(ctx, "k2.txt", strings.NewReader("22"), storage.WithContentLength(2))

				if err := fs.DeleteMulti(ctx, "k1.txt", "k2.txt", "nonexistent.txt"); err != nil {
					t.Fatalf("DeleteMulti error: %v", err)
				}
				e1, _ := fs.Exists(ctx, "k1.txt")
				e2, _ := fs.Exists(ctx, "k2.txt")
				if e1 || e2 {
					t.Error("expected files deleted")
				}
			},
		},
		{
			name: "HTTPHandler error branches",
			run: func(t *testing.T, fs *storage.FileSystemStorage) {
				handler := fs.HTTPHandler()

				// 1. Missing signature -> 403
				req1 := httptest.NewRequest(http.MethodGet, "/storage/file", nil)
				rec1 := httptest.NewRecorder()
				handler.ServeHTTP(rec1, req1)
				if rec1.Code != http.StatusForbidden {
					t.Errorf("expected 403 on missing sig, got %d", rec1.Code)
				}

				// 2. Method mismatch -> 405
				validGetURL, _ := fs.PresignGet(ctx, "dummy.txt", 10*time.Minute)
				req2 := httptest.NewRequest(http.MethodPost, validGetURL, nil)
				rec2 := httptest.NewRecorder()
				handler.ServeHTTP(rec2, req2)
				if rec2.Code != http.StatusMethodNotAllowed {
					t.Errorf("expected 405 on method mismatch, got %d", rec2.Code)
				}

				// 3. GET non-existent file -> 404
				req3 := httptest.NewRequest(http.MethodGet, validGetURL, nil)
				rec3 := httptest.NewRecorder()
				handler.ServeHTTP(rec3, req3)
				if rec3.Code != http.StatusNotFound {
					t.Errorf("expected 404 for missing file, got %d", rec3.Code)
				}
			},
		},
		{
			name: "loadInfo fallback when metadata sidecar is missing",
			run: func(t *testing.T, _ *storage.FileSystemStorage) {
				tmpDir := t.TempDir()
				fsCustom, err := storage.NewFileSystem(tmpDir)
				if err != nil {
					t.Fatalf("NewFileSystem error: %v", err)
				}

				// Write raw file directly without .meta.json
				if err := os.WriteFile(filepath.Join(tmpDir, "file.json"), []byte(`{"status":"ok"}`), 0644); err != nil {
					t.Fatalf("WriteFile error: %v", err)
				}

				// Stat should still succeed by falling back to OS stat and extension detection
				info, err := fsCustom.Stat(ctx, "file.json")
				if err != nil {
					t.Fatalf("Stat error: %v", err)
				}
				if info.ContentType != "application/json" {
					t.Errorf("expected application/json from extension fallback, got %s", info.ContentType)
				}

				// Stat on missing file returns ErrNotFound
				_, err = fsCustom.Stat(ctx, "nonexistent.txt")
				if err != storage.ErrNotFound {
					t.Errorf("expected ErrNotFound, got %v", err)
				}

				// Get with invalid range
				_, _, err = fsCustom.Get(ctx, "file.json", storage.WithRange(-1, 5))
				if err != storage.ErrInvalidRange {
					t.Errorf("expected ErrInvalidRange for start < 0, got %v", err)
				}
				_, _, err = fsCustom.Get(ctx, "file.json", storage.WithRange(5, 2))
				if err != storage.ErrInvalidRange {
					t.Errorf("expected ErrInvalidRange for start > end, got %v", err)
				}
			},
		},
		{
			name: "HTTPHandler handles expired signatures and invalid query",
			run: func(t *testing.T, fs *storage.FileSystemStorage) {
				handler := fs.HTTPHandler()

				// Expired signature
				expiredURL, err := fs.PresignGet(ctx, "expired.txt", -1*time.Minute)
				if err != nil {
					t.Fatalf("PresignGet error: %v", err)
				}
				req := httptest.NewRequest(http.MethodGet, expiredURL, nil)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusForbidden {
					t.Errorf("expected 403 for expired presign, got %d", rec.Code)
				}

				// Tampered signature
				tamperedURL := expiredURL + "tampered"
				reqTampered := httptest.NewRequest(http.MethodGet, tamperedURL, nil)
				recTampered := httptest.NewRecorder()
				handler.ServeHTTP(recTampered, reqTampered)
				if recTampered.Code != http.StatusForbidden {
					t.Errorf("expected 403 for tampered signature, got %d", recTampered.Code)
				}

				// Bad expires parameter format -> signature verification fails -> 403
				badReq := httptest.NewRequest(http.MethodGet, "/storage/bad?sig=123&expires=notanumber", nil)
				badRec := httptest.NewRecorder()
				handler.ServeHTTP(badRec, badReq)
				if badRec.Code != http.StatusForbidden {
					t.Errorf("expected 403 for bad expires param, got %d", badRec.Code)
				}

				// Presigned PUT upload via HTTPHandler
				uploadURL, err := fs.PresignPut(ctx, "incoming/doc.txt", 10*time.Minute, storage.WithPresignContentType("text/plain"))
				if err != nil {
					t.Fatalf("PresignPut error: %v", err)
				}
				putReq := httptest.NewRequest(http.MethodPut, uploadURL, strings.NewReader("uploaded content"))
				putReq.Header.Set("Content-Type", "text/plain")
				putRec := httptest.NewRecorder()
				handler.ServeHTTP(putRec, putReq)
				if putRec.Code != http.StatusOK {
					t.Errorf("expected 200 for PUT upload, got %d", putRec.Code)
				}

				// Verify file was written
				rc, info, err := fs.Get(ctx, "incoming/doc.txt")
				if err != nil {
					t.Fatalf("Get error after HTTPHandler PUT: %v", err)
				}
				rc.Close()
				if info.ContentType != "text/plain" {
					t.Errorf("expected text/plain, got %s", info.ContentType)
				}
			},
		},
		{
			name: "FileSystem pagination, range clamping, and octet-stream fallback",
			run: func(t *testing.T, fs *storage.FileSystemStorage) {
				// 1. Put without extension and without content type -> application/octet-stream
				putInfo, err := fs.Put(ctx, "blobfile", strings.NewReader("0123456789"))
				if err != nil {
					t.Fatalf("Put error: %v", err)
				}
				if putInfo.ContentType != "application/octet-stream" {
					t.Errorf("expected application/octet-stream, got %s", putInfo.ContentType)
				}

				// 2. Empty DeleteMulti returns nil
				if err := fs.DeleteMulti(ctx); err != nil {
					t.Errorf("expected nil for empty DeleteMulti, got %v", err)
				}

				// 3. Range with start >= stat.Size() returns ErrInvalidRange
				_, _, err = fs.Get(ctx, "blobfile", storage.WithRange(100, 150))
				if err != storage.ErrInvalidRange {
					t.Errorf("expected ErrInvalidRange for start >= size, got %v", err)
				}

				// 4. Range with end >= stat.Size() clamps to stat.Size() - 1
				rc, rangeInfo, err := fs.Get(ctx, "blobfile", storage.WithRange(5, 50))
				if err != nil {
					t.Fatalf("Get with clamped end error: %v", err)
				}
				defer rc.Close()
				data, _ := io.ReadAll(rc)
				if string(data) != "56789" {
					t.Errorf("expected 56789, got %s", string(data))
				}
				if rangeInfo.Size != 5 {
					t.Errorf("expected range size 5, got %d", rangeInfo.Size)
				}

				// 5. Pagination with ContinuationToken
				_, _ = fs.Put(ctx, "page/a", strings.NewReader("a"))
				_, _ = fs.Put(ctx, "page/b", strings.NewReader("b"))
				_, _ = fs.Put(ctx, "page/c", strings.NewReader("c"))

				res1, err := fs.List(ctx, "page/", storage.WithMaxKeys(1))
				if err != nil {
					t.Fatalf("List page 1 error: %v", err)
				}
				if !res1.IsTruncated || res1.NextContinuationToken == "" {
					t.Errorf("expected truncated with token, got %+v", res1)
				}

				res2, err := fs.List(ctx, "page/", storage.WithMaxKeys(2), storage.WithContinuationToken(res1.NextContinuationToken))
				if err != nil {
					t.Fatalf("List page 2 error: %v", err)
				}
				if len(res2.Objects) != 2 {
					t.Errorf("expected 2 objects on page 2, got %d", len(res2.Objects))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs, err := storage.NewFileSystem(t.TempDir(), storage.WithFSBaseURL("http://localhost:8080"))
			if err != nil {
				t.Fatalf("NewFileSystem error: %v", err)
			}
			tc.run(t, fs)
		})
	}
}

func TestNewFileSystem_InvalidBaseDir(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "a_file")
	if err := os.WriteFile(tempFile, []byte("data"), 0600); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	// Trying to create a directory under a regular file fails
	_, err := storage.NewFileSystem(filepath.Join(tempFile, "sub_dir"))
	if err == nil {
		t.Error("expected error from NewFileSystem when baseDir cannot be created, got nil")
	}
}
