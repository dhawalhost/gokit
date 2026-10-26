package compression_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dhawalhost/gokit/compression"
)

var sampleText = []byte(strings.Repeat("Gokit high-performance microservices compression test payload. ", 100))

func TestEncodings_Validity_And_String(t *testing.T) {
	cases := []struct {
		enc   compression.Encoding
		valid bool
		str   string
	}{
		{compression.EncodingZstandard, true, "zstd"},
		{compression.EncodingBrotli, true, "br"},
		{compression.EncodingGzip, true, "gzip"},
		{compression.EncodingDeflate, true, "deflate"},
		{compression.EncodingIdentity, true, "identity"},
		{compression.Encoding("unknown"), false, "unknown"},
	}

	for _, c := range cases {
		if c.enc.IsValid() != c.valid {
			t.Errorf("encoding %s: expected valid=%v, got %v", c.enc, c.valid, c.enc.IsValid())
		}
		if c.enc.String() != c.str {
			t.Errorf("encoding %s: expected string %s, got %s", c.enc, c.str, c.enc.String())
		}
	}

	// ParseEncoding
	if e, ok := compression.ParseEncoding("ZSTD"); !ok || e != compression.EncodingZstandard {
		t.Fatalf("expected zstd, got %s", e)
	}
	if e, ok := compression.ParseEncoding("  br  "); !ok || e != compression.EncodingBrotli {
		t.Fatalf("expected br, got %s", e)
	}
	if _, ok := compression.ParseEncoding("invalid"); ok {
		t.Fatal("expected ok=false for invalid encoding")
	}
}

func TestStream_Roundtrip_AllCodecs(t *testing.T) {
	codecs := []compression.Encoding{
		compression.EncodingZstandard,
		compression.EncodingBrotli,
		compression.EncodingGzip,
		compression.EncodingDeflate,
		compression.EncodingIdentity,
	}

	levels := []compression.Level{
		compression.LevelDefault,
		compression.LevelBestSpeed,
		compression.LevelBestCompression,
	}

	for _, codec := range codecs {
		for _, lvl := range levels {
			t.Run(fmt.Sprintf("%s_Level_%d", codec, lvl), func(t *testing.T) {
				var compressed bytes.Buffer
				err := compression.Compress(codec, bytes.NewReader(sampleText), &compressed, compression.WithLevel(lvl))
				if err != nil {
					t.Fatalf("compression failed: %v", err)
				}

				if codec != compression.EncodingIdentity && compressed.Len() >= len(sampleText) {
					t.Fatalf("expected compression to reduce size: orig=%d, comp=%d", len(sampleText), compressed.Len())
				}

				var decompressed bytes.Buffer
				err = compression.Decompress(codec, &compressed, &decompressed)
				if err != nil {
					t.Fatalf("decompression failed: %v", err)
				}

				if !bytes.Equal(decompressed.Bytes(), sampleText) {
					t.Fatal("decompressed bytes mismatch")
				}
			})
		}
	}
}

func TestStream_Bytes_Helpers(t *testing.T) {
	codecs := []compression.Encoding{
		compression.EncodingZstandard,
		compression.EncodingBrotli,
		compression.EncodingGzip,
		compression.EncodingDeflate,
	}

	for _, codec := range codecs {
		t.Run(string(codec), func(t *testing.T) {
			comp, err := compression.CompressBytes(codec, sampleText)
			if err != nil {
				t.Fatalf("CompressBytes failed: %v", err)
			}

			decomp, err := compression.DecompressBytes(codec, comp)
			if err != nil {
				t.Fatalf("DecompressBytes failed: %v", err)
			}

			if !bytes.Equal(decomp, sampleText) {
				t.Fatal("decompressed bytes do not match original")
			}
		})
	}
}

func TestStream_Error_Handling(t *testing.T) {
	// Nil reader / writer
	if err := compression.Compress(compression.EncodingGzip, nil, &bytes.Buffer{}); err != compression.ErrNilReader {
		t.Fatalf("expected ErrNilReader, got %v", err)
	}
	if err := compression.Compress(compression.EncodingGzip, bytes.NewReader(sampleText), nil); err != compression.ErrNilWriter {
		t.Fatalf("expected ErrNilWriter, got %v", err)
	}
	if err := compression.Decompress(compression.EncodingGzip, nil, &bytes.Buffer{}); err != compression.ErrNilReader {
		t.Fatalf("expected ErrNilReader, got %v", err)
	}
	if err := compression.Decompress(compression.EncodingGzip, bytes.NewReader(sampleText), nil); err != compression.ErrNilWriter {
		t.Fatalf("expected ErrNilWriter, got %v", err)
	}

	// Unsupported encoding
	if _, err := compression.NewWriter("rar", &bytes.Buffer{}); err != compression.ErrUnsupportedEncoding {
		t.Fatalf("expected ErrUnsupportedEncoding on writer, got %v", err)
	}
	if _, err := compression.NewReader("rar", bytes.NewReader([]byte{})); err != compression.ErrUnsupportedEncoding {
		t.Fatalf("expected ErrUnsupportedEncoding on reader, got %v", err)
	}

	// Corrupted data
	corrupted := []byte{0x1f, 0x8b, 0x08, 0x00, 0xff, 0xff} // broken gzip header
	if _, err := compression.DecompressBytes(compression.EncodingGzip, corrupted); err == nil {
		t.Fatal("expected error decompressing corrupted gzip data")
	}
	if _, err := compression.DecompressBytes(compression.EncodingZstandard, corrupted); err == nil {
		t.Fatal("expected error decompressing corrupted zstd data")
	}
}

func TestMiddleware_Response_Compression(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(sampleText)
	})

	mw := compression.Middleware(compression.WithMinLength(100))

	cases := []struct {
		name           string
		acceptEncoding string
		expectedCodec  string
	}{
		{"Zstandard", "zstd", "zstd"},
		{"Brotli", "br", "br"},
		{"Gzip", "gzip", "gzip"},
		{"Deflate", "deflate", "deflate"},
		{"Preference_Order", "gzip, br, zstd", "zstd"}, // server prefers zstd
		{"Client_Quality_Weights", "gzip;q=1.0, zstd;q=0.5", "gzip"},
		{"Wildcard", "*", "zstd"},
		{"Identity_Only", "identity", ""},
		{"No_Accept_Encoding", "", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/data", nil)
			if c.acceptEncoding != "" {
				req.Header.Set("Accept-Encoding", c.acceptEncoding)
			}

			mw(handler).ServeHTTP(rec, req)

			resp := rec.Result()
			defer resp.Body.Close()

			contentEncoding := resp.Header.Get("Content-Encoding")
			if contentEncoding != c.expectedCodec {
				t.Fatalf("expected Content-Encoding %q, got %q", c.expectedCodec, contentEncoding)
			}

			if c.expectedCodec != "" {
				if vary := resp.Header.Get("Vary"); !strings.Contains(vary, "Accept-Encoding") {
					t.Fatalf("expected Vary: Accept-Encoding, got %q", vary)
				}

				// Decompress and verify content
				codec, _ := compression.ParseEncoding(contentEncoding)
				decomp, err := compression.DecompressBytes(codec, rec.Body.Bytes())
				if err != nil {
					t.Fatalf("failed to decompress response: %v", err)
				}
				if !bytes.Equal(decomp, sampleText) {
					t.Fatal("decompressed response content mismatch")
				}
			} else {
				// Raw bytes
				if !bytes.Equal(rec.Body.Bytes(), sampleText) {
					t.Fatal("uncompressed response body mismatch")
				}
			}
		})
	}
}

func TestMiddleware_MinLength_Threshold(t *testing.T) {
	shortText := []byte("short response payload")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(shortText)
	})

	// Threshold is 1024 bytes; shortText is 22 bytes
	mw := compression.Middleware(compression.WithMinLength(1024))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/short", nil)
	req.Header.Set("Accept-Encoding", "gzip, zstd, br")

	mw(handler).ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if ce := resp.Header.Get("Content-Encoding"); ce != "" {
		t.Fatalf("expected no Content-Encoding for payload under MinLength, got %s", ce)
	}

	if !bytes.Equal(rec.Body.Bytes(), shortText) {
		t.Fatalf("body mismatch: %s", rec.Body.String())
	}
}

func TestMiddleware_MIME_Filtering(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg") // Non-compressible
		_, _ = w.Write(sampleText)
	})

	mw := compression.Middleware(compression.WithMinLength(100))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/photo.jpg", nil)
	req.Header.Set("Accept-Encoding", "gzip, zstd, br")

	mw(handler).ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if ce := resp.Header.Get("Content-Encoding"); ce != "" {
		t.Fatalf("expected image/jpeg to not be compressed, got %s", ce)
	}
}

func TestMiddleware_Request_Decompression(t *testing.T) {
	var receivedBody []byte

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		receivedBody = body
		w.WriteHeader(http.StatusOK)
	})

	mw := compression.Middleware(compression.WithDecompression(true))

	codecs := []compression.Encoding{
		compression.EncodingZstandard,
		compression.EncodingBrotli,
		compression.EncodingGzip,
		compression.EncodingDeflate,
	}

	for _, codec := range codecs {
		t.Run("Decompress_"+string(codec), func(t *testing.T) {
			compBytes, err := compression.CompressBytes(codec, sampleText)
			if err != nil {
				t.Fatalf("compress failed: %v", err)
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(compBytes))
			req.Header.Set("Content-Encoding", codec.String())

			mw(handler).ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200 OK, got %d", rec.Code)
			}
			if !bytes.Equal(receivedBody, sampleText) {
				t.Fatal("server received decompressed body mismatch")
			}
		})
	}

	// Corrupted request body
	t.Run("Corrupted_Body_400", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader([]byte("not-gzip")))
		req.Header.Set("Content-Encoding", "gzip")

		mw(handler).ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d", rec.Code)
		}
	})
}

func TestMiddleware_Options(t *testing.T) {
	// Custom encodings restriction (e.g. only gzip and br)
	mw := compression.Handler(
		compression.WithEncodings(compression.EncodingGzip, compression.EncodingBrotli),
		compression.WithMIMETypes("text/custom"),
	)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/custom")
		_, _ = w.Write(sampleText)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "zstd, gzip") // zstd not enabled in options

	mw(handler).ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if ce := resp.Header.Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("expected gzip since zstd was excluded, got %s", ce)
	}
}

func TestConcurrency(t *testing.T) {
	mw := compression.Middleware(compression.WithMinLength(10))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(sampleText)
	})

	var wg sync.WaitGroup
	workers := 10
	iterations := 20

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/test", nil)
				switch (workerID + j) % 4 {
				case 0:
					req.Header.Set("Accept-Encoding", "zstd")
				case 1:
					req.Header.Set("Accept-Encoding", "br")
				case 2:
					req.Header.Set("Accept-Encoding", "gzip")
				case 3:
					req.Header.Set("Accept-Encoding", "deflate")
				}
				mw(handler).ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("worker %d: expected 200, got %d", workerID, rec.Code)
				}
			}
		}(i)
	}

	wg.Wait()
}

func TestMiddleware_Explicit_WriteHeader(t *testing.T) {
	mw := compression.Middleware()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.WriteHeader(http.StatusOK) // idempotent call
		_, _ = w.Write(sampleText)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/create", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", rec.Code)
	}
}

func TestMiddleware_Flusher_And_Hijacker(t *testing.T) {
	mw := compression.Middleware(compression.WithMinLength(10))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(sampleText)

		// Test Flusher
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		// Test Hijacker (recorder does not support hijack, should return error)
		if hijacker, ok := w.(http.Hijacker); ok {
			if _, _, err := hijacker.Hijack(); err == nil {
				t.Fatal("expected error hijacking httptest.ResponseRecorder")
			}
		}
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestMiddleware_Wildcard_Reject(t *testing.T) {
	mw := compression.Middleware()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(sampleText)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "*;q=0")

	mw(handler).ServeHTTP(rec, req)

	if ce := rec.Header().Get("Content-Encoding"); ce != "" {
		t.Fatalf("expected no compression when client sends *;q=0, got %s", ce)
	}
}

func TestMiddleware_MIME_Wildcard_Prefix(t *testing.T) {
	mw := compression.Middleware(
		compression.WithMIMETypes("text/*"),
		compression.WithMinLength(10),
	)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		_, _ = w.Write(sampleText)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/export.csv", nil)
	req.Header.Set("Accept-Encoding", "zstd")

	mw(handler).ServeHTTP(rec, req)

	if ce := rec.Header().Get("Content-Encoding"); ce != "zstd" {
		t.Fatalf("expected text/* wildcard to match text/csv, got %s", ce)
	}
}
