package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FileSystemStorage implements Storage backed by a local directory.
// It is protected against path-traversal attacks and performs atomic file writes.
type FileSystemStorage struct {
	baseDir string
	baseURL string
	secret  []byte
	mu      sync.RWMutex
}

// FSOption configures FileSystemStorage.
type FSOption func(*FileSystemStorage)

// WithFSBaseURL sets the base URL returned by PresignGet and PresignPut.
func WithFSBaseURL(baseURL string) FSOption {
	return func(fs *FileSystemStorage) {
		fs.baseURL = baseURL
	}
}

// WithFSPresignSecret configures the HMAC signing secret for presigned URLs.
func WithFSPresignSecret(secret []byte) FSOption {
	return func(fs *FileSystemStorage) {
		fs.secret = secret
	}
}

// NewFileSystem creates a new FileSystemStorage rooted at baseDir.
func NewFileSystem(baseDir string, opts ...FSOption) (*FileSystemStorage, error) {
	absDir, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("storage: resolve base dir: %w", err)
	}

	if err := os.MkdirAll(absDir, 0750); err != nil {
		return nil, fmt.Errorf("storage: create base dir: %w", err)
	}

	fs := &FileSystemStorage{
		baseDir: absDir,
		baseURL: "http://localhost:8080",
		secret:  []byte("gokit-fs-storage-secret"),
	}

	for _, opt := range opts {
		opt(fs)
	}

	return fs, nil
}

func (fs *FileSystemStorage) resolvePath(key string) (string, error) {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, "..") || strings.Contains(trimmed, "\\") {
		return "", ErrInvalidKey
	}

	cleanKey := path.Clean(trimmed)
	if cleanKey == "." || cleanKey == ".." || strings.HasPrefix(cleanKey, "../") {
		return "", ErrInvalidKey
	}

	target := filepath.Join(fs.baseDir, filepath.FromSlash(cleanKey))
	// Path traversal guard
	rel, err := filepath.Rel(fs.baseDir, target)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
		return "", ErrInvalidKey
	}

	return target, nil
}

func (fs *FileSystemStorage) metaPath(targetPath string) string {
	return targetPath + ".gokit.meta"
}

// Put atomically writes an object to the filesystem.
func (fs *FileSystemStorage) Put(_ context.Context, key string, r io.Reader, opts ...PutOption) (*ObjectInfo, error) {
	targetPath, err := fs.resolvePath(key)
	if err != nil {
		return nil, err
	}

	var options PutOptions
	for _, opt := range opts {
		opt(&options)
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("storage: create parent dir: %w", err)
	}

	// Write to temporary file in the same directory for atomic rename
	tmpFile, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return nil, fmt.Errorf("storage: create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpName) // Clean up if rename didn't happen
	}()

	hasher := sha256.New()
	writer := io.MultiWriter(tmpFile, hasher)

	written, err := io.Copy(writer, r)
	if err != nil {
		_ = tmpFile.Close()
		return nil, fmt.Errorf("storage: write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("storage: close temp file: %w", err)
	}

	etag := hex.EncodeToString(hasher.Sum(nil))
	contentType := options.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(key))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
	}

	info := ObjectInfo{
		Key:          key,
		Size:         written,
		ContentType:  contentType,
		ETag:         etag,
		LastModified: time.Now().UTC(),
		Metadata:     options.Metadata,
	}

	// Save metadata sidecar
	metaBytes, err := json.Marshal(info)
	if err == nil {
		_ = os.WriteFile(fs.metaPath(tmpName), metaBytes, 0600)
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Atomic rename to target path
	if err := os.Rename(tmpName, targetPath); err != nil {
		return nil, fmt.Errorf("storage: rename to target: %w", err)
	}
	_ = os.Rename(fs.metaPath(tmpName), fs.metaPath(targetPath))

	return &info, nil
}

// Get opens and reads an object from disk.
func (fs *FileSystemStorage) Get(_ context.Context, key string, opts ...GetOption) (io.ReadCloser, *ObjectInfo, error) {
	targetPath, err := fs.resolvePath(key)
	if err != nil {
		return nil, nil, err
	}

	var options GetOptions
	for _, opt := range opts {
		opt(&options)
	}

	fs.mu.RLock()
	defer fs.mu.RUnlock()

	// #nosec G304 -- targetPath is validated via resolvePath to prevent path traversal
	file, err := os.Open(filepath.Clean(targetPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, fmt.Errorf("storage: open file: %w", err)
	}

	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("storage: stat file: %w", err)
	}

	info := fs.loadInfo(key, targetPath, stat)

	var reader io.ReadCloser = file
	if options.RangeStart != nil && options.RangeEnd != nil {
		start := *options.RangeStart
		end := *options.RangeEnd
		if start < 0 || end < start || start >= stat.Size() {
			_ = file.Close()
			return nil, nil, ErrInvalidRange
		}
		if end >= stat.Size() {
			end = stat.Size() - 1
		}
		if _, err := file.Seek(start, io.SeekStart); err != nil {
			_ = file.Close()
			return nil, nil, fmt.Errorf("storage: seek range: %w", err)
		}
		rangeLength := end - start + 1
		info.Size = rangeLength
		reader = &rangedReadCloser{
			reader: io.LimitReader(file, rangeLength),
			closer: file,
		}
	}

	return reader, &info, nil
}

type rangedReadCloser struct {
	reader io.Reader
	closer io.Closer
}

func (r *rangedReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *rangedReadCloser) Close() error {
	return r.closer.Close()
}

func (fs *FileSystemStorage) loadInfo(key, targetPath string, stat os.FileInfo) ObjectInfo {
	metaFile := fs.metaPath(targetPath)
	// #nosec G304 -- metaFile path is internally derived from sanitized targetPath
	if data, err := os.ReadFile(filepath.Clean(metaFile)); err == nil {
		var info ObjectInfo
		if err := json.Unmarshal(data, &info); err == nil {
			return info
		}
	}

	contentType := mime.TypeByExtension(filepath.Ext(key))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	return ObjectInfo{
		Key:          key,
		Size:         stat.Size(),
		ContentType:  contentType,
		ETag:         fmt.Sprintf(`"%x-%x"`, stat.ModTime().UnixNano(), stat.Size()),
		LastModified: stat.ModTime().UTC(),
	}
}

// Delete removes the file and its metadata sidecar from disk.
func (fs *FileSystemStorage) Delete(_ context.Context, key string) error {
	targetPath, err := fs.resolvePath(key)
	if err != nil {
		return err
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	_ = os.Remove(fs.metaPath(targetPath))
	if err := os.Remove(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("storage: delete file: %w", err)
	}

	return nil
}

// DeleteMulti removes multiple files from disk.
func (fs *FileSystemStorage) DeleteMulti(ctx context.Context, keys ...string) error {
	for _, k := range keys {
		if err := fs.Delete(ctx, k); err != nil {
			return err
		}
	}
	return nil
}

// Exists checks if the file exists on disk.
func (fs *FileSystemStorage) Exists(_ context.Context, key string) (bool, error) {
	targetPath, err := fs.resolvePath(key)
	if err != nil {
		return false, err
	}

	fs.mu.RLock()
	defer fs.mu.RUnlock()

	_, err = os.Stat(targetPath)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("storage: check existence: %w", err)
}

// Stat retrieves file metadata without opening the file for reading.
func (fs *FileSystemStorage) Stat(_ context.Context, key string) (*ObjectInfo, error) {
	targetPath, err := fs.resolvePath(key)
	if err != nil {
		return nil, err
	}

	fs.mu.RLock()
	defer fs.mu.RUnlock()

	stat, err := os.Stat(targetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: stat file: %w", err)
	}

	info := fs.loadInfo(key, targetPath, stat)
	return &info, nil
}

// List scans files rooted in baseDir matching prefix.
func (fs *FileSystemStorage) List(_ context.Context, prefix string, opts ...ListOption) (*ListResult, error) {
	var options ListOptions
	for _, opt := range opts {
		opt(&options)
	}
	if options.MaxKeys <= 0 {
		options.MaxKeys = 1000
	}

	fs.mu.RLock()
	defer fs.mu.RUnlock()

	var allKeys []string
	err := filepath.WalkDir(fs.baseDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".gokit.meta") {
			return nil
		}
		rel, err := filepath.Rel(fs.baseDir, p)
		if err != nil {
			return nil
		}
		slashKey := filepath.ToSlash(rel)
		if strings.HasPrefix(slashKey, prefix) {
			allKeys = append(allKeys, slashKey)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("storage: walk directory: %w", err)
	}

	sort.Strings(allKeys)

	startIndex := 0
	if options.ContinuationToken != "" {
		for i, k := range allKeys {
			if k > options.ContinuationToken {
				startIndex = i
				break
			}
			if i == len(allKeys)-1 {
				startIndex = len(allKeys)
			}
		}
	}

	res := &ListResult{
		Objects:        make([]ObjectInfo, 0),
		CommonPrefixes: make([]string, 0),
	}

	prefixSet := make(map[string]struct{})
	count := 0

	for i := startIndex; i < len(allKeys); i++ {
		key := allKeys[i]

		if options.Delimiter != "" {
			trimmed := strings.TrimPrefix(key, prefix)
			if idx := strings.Index(trimmed, options.Delimiter); idx != -1 {
				commonPrefix := prefix + trimmed[:idx+len(options.Delimiter)]
				if _, seen := prefixSet[commonPrefix]; !seen {
					prefixSet[commonPrefix] = struct{}{}
					res.CommonPrefixes = append(res.CommonPrefixes, commonPrefix)
					count++
				}
				continue
			}
		}

		targetPath := filepath.Join(fs.baseDir, filepath.FromSlash(key))
		stat, statErr := os.Stat(targetPath)
		if statErr == nil {
			res.Objects = append(res.Objects, fs.loadInfo(key, targetPath, stat))
			count++
		}

		if count >= options.MaxKeys {
			if i+1 < len(allKeys) {
				res.IsTruncated = true
				res.NextContinuationToken = key
			}
			break
		}
	}

	return res, nil
}

// PresignGet creates a signed HTTP GET URL for direct file downloads.
func (fs *FileSystemStorage) PresignGet(_ context.Context, key string, ttl time.Duration, _ ...PresignOption) (string, error) {
	return fs.generatePresignedURL("GET", key, ttl, "")
}

// PresignPut creates a signed HTTP PUT URL for direct file uploads.
func (fs *FileSystemStorage) PresignPut(_ context.Context, key string, ttl time.Duration, opts ...PresignOption) (string, error) {
	var options PresignOptions
	for _, opt := range opts {
		opt(&options)
	}
	return fs.generatePresignedURL("PUT", key, ttl, options.ContentType)
}

func (fs *FileSystemStorage) generatePresignedURL(method, key string, ttl time.Duration, contentType string) (string, error) {
	if _, err := fs.resolvePath(key); err != nil {
		return "", err
	}

	expires := time.Now().Add(ttl).Unix()
	msg := fmt.Sprintf("%s\n%s\n%d\n%s", method, key, expires, contentType)

	mac := hmac.New(sha256.New, fs.secret)
	mac.Write([]byte(msg))
	sig := hex.EncodeToString(mac.Sum(nil))

	u, err := url.Parse(fs.baseURL)
	if err != nil {
		return "", fmt.Errorf("storage: parse base url: %w", err)
	}

	q := u.Query()
	q.Set("key", key)
	q.Set("method", method)
	q.Set("expires", strconv.FormatInt(expires, 10))
	q.Set("sig", sig)
	if contentType != "" {
		q.Set("content_type", contentType)
	}

	u.Path = "/storage/file"
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// HTTPHandler returns an http.Handler that serves and accepts presigned uploads/downloads directly.
func (fs *FileSystemStorage) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, key, err := VerifyPresignedURL(r.URL.String(), fs.secret)
		if err != nil {
			if errors.Is(err, ErrPresignExpired) {
				http.Error(w, "presigned url expired", http.StatusForbidden)
				return
			}
			http.Error(w, "invalid signature", http.StatusForbidden)
			return
		}

		if r.Method != method {
			http.Error(w, fmt.Sprintf("method %s not allowed, expected %s", r.Method, method), http.StatusMethodNotAllowed)
			return
		}

		switch method {
		case http.MethodGet:
			reader, info, err := fs.Get(r.Context(), key)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					http.NotFound(w, r)
					return
				}
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer func() {
				_ = reader.Close()
			}()

			w.Header().Set("Content-Type", info.ContentType)
			w.Header().Set("ETag", info.ETag)
			w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
			_, _ = io.Copy(w, reader)

		case http.MethodPut:
			contentType := r.Header.Get("Content-Type")
			info, err := fs.Put(r.Context(), key, r.Body, WithContentType(contentType))
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("ETag", info.ETag)
			w.WriteHeader(http.StatusOK)

		default:
			http.Error(w, "unsupported presign method", http.StatusBadRequest)
		}
	})
}
