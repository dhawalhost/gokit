package compression

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// Middleware returns an HTTP middleware that compresses responses and decompresses requests.
func Middleware(opts ...Option) func(http.Handler) http.Handler {
	cfg := DefaultOptions()
	for _, o := range opts {
		o(&cfg)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 1. Transparent request body decompression
			if cfg.EnableDecompression {
				reqEncoding := r.Header.Get("Content-Encoding")
				if reqEncoding != "" && reqEncoding != "identity" {
					codec, ok := ParseEncoding(reqEncoding)
					if ok && codec != EncodingIdentity {
						reader, err := NewReader(codec, r.Body)
						if err != nil {
							http.Error(w, "invalid or corrupted compressed request body", http.StatusBadRequest)
							return
						}
						defer func() { _ = reader.Close() }()
						r.Body = reader
					}
				}
			}

			// 2. Select optimal response encoding from Accept-Encoding
			acceptEncoding := r.Header.Get("Accept-Encoding")
			selectedCodec := selectEncoding(acceptEncoding, cfg.Encodings)

			// If client doesn't support any enabled compression, pass through
			if selectedCodec == "" || selectedCodec == EncodingIdentity {
				next.ServeHTTP(w, r)
				return
			}

			// 3. Wrap response writer with deferred threshold buffer
			rw := &compressResponseWriter{
				ResponseWriter: w,
				codec:          selectedCodec,
				cfg:            cfg,
				buf:            acquireBuffer(),
			}
			defer rw.finish()

			next.ServeHTTP(rw, r)
		})
	}
}

// Handler is an alias for Middleware.
func Handler(opts ...Option) func(http.Handler) http.Handler {
	return Middleware(opts...)
}

type compressResponseWriter struct {
	http.ResponseWriter
	codec       Encoding
	cfg         Options
	buf         *bytes.Buffer
	writer      io.WriteCloser
	status      int
	wroteHeader bool
	compressing bool
	bypassed    bool
}

func (w *compressResponseWriter) WriteHeader(status int) {
	if w.wroteHeader || w.status != 0 {
		return
	}
	w.status = status
}

func (w *compressResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.Header().Add("Vary", "Accept-Encoding")
	}

	// If already streaming compressed data
	if w.compressing {
		return w.writer.Write(p)
	}

	// If compression was explicitly bypassed (e.g. non-compressible MIME type)
	if w.bypassed {
		if !w.wroteHeader {
			w.flushHeaders()
		}
		return w.ResponseWriter.Write(p)
	}

	// Check MIME type suitability on first write
	if !w.isCompressibleMIME() {
		w.bypassed = true
		if !w.wroteHeader {
			w.flushHeaders()
		}
		return w.ResponseWriter.Write(p)
	}

	// Buffer initial bytes to see if payload exceeds MinLength threshold
	w.buf.Write(p)

	if w.buf.Len() >= w.cfg.MinLength {
		// Threshold exceeded: start compressed streaming
		if err := w.startCompression(); err != nil {
			// Fall back to uncompressed write on compression init error
			w.bypassed = true
			w.flushHeaders()
			_, writeErr := w.ResponseWriter.Write(w.buf.Bytes())
			releaseBuffer(w.buf)
			w.buf = nil
			return len(p), writeErr
		}

		// Write buffered bytes into the compressor
		if _, err := w.writer.Write(w.buf.Bytes()); err != nil {
			return 0, err
		}
		releaseBuffer(w.buf)
		w.buf = nil
	}

	return len(p), nil
}

func (w *compressResponseWriter) startCompression() error {
	w.compressing = true
	w.Header().Set("Content-Encoding", w.codec.String())
	w.Header().Del("Content-Length") // Length is unknown due to streaming compression

	w.flushHeaders()

	writer, err := NewWriter(w.codec, w.ResponseWriter, WithLevel(w.cfg.Level))
	if err != nil {
		return err
	}
	w.writer = writer
	return nil
}

func (w *compressResponseWriter) isCompressibleMIME() bool {
	ct := w.Header().Get("Content-Type")
	if ct == "" {
		return true // Default to compressible if unspecified
	}

	// Strip charset (e.g. "application/json; charset=utf-8" -> "application/json")
	if idx := strings.Index(ct, ";"); idx >= 0 {
		ct = ct[:idx]
	}
	ct = strings.TrimSpace(strings.ToLower(ct))

	for _, pattern := range w.cfg.MIMETypes {
		pattern = strings.ToLower(pattern)
		if strings.HasSuffix(pattern, "/*") {
			prefix := strings.TrimSuffix(pattern, "/*")
			if strings.HasPrefix(ct, prefix+"/") {
				return true
			}
		} else if ct == pattern || strings.HasPrefix(ct, pattern) {
			return true
		}
	}
	return false
}

func (w *compressResponseWriter) flushHeaders() {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.ResponseWriter.WriteHeader(w.status)
}

func (w *compressResponseWriter) finish() {
	if w.compressing {
		if w.writer != nil {
			_ = w.writer.Close()
		}
		return
	}

	// Payload was smaller than MinLength threshold; write uncompressed
	if w.buf != nil {
		if !w.wroteHeader {
			w.Header().Set("Content-Length", strconv.Itoa(w.buf.Len()))
			w.flushHeaders()
		}
		if w.buf.Len() > 0 {
			_, _ = w.ResponseWriter.Write(w.buf.Bytes())
		}
		releaseBuffer(w.buf)
		w.buf = nil
	} else if !w.wroteHeader {
		w.flushHeaders()
	}
}

// Hijack implements http.Hijacker interface for WebSocket and raw socket upgrades.
func (w *compressResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("compression: response writer does not support hijacking")
}

// Flush implements http.Flusher interface.
func (w *compressResponseWriter) Flush() {
	if w.compressing {
		if flusher, ok := w.writer.(interface{ Flush() error }); ok {
			_ = flusher.Flush()
		}
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// selectEncoding determines the best encoding according to client weights and server preferences.
func selectEncoding(acceptHeader string, serverPreferences []Encoding) Encoding {
	if acceptHeader == "" {
		return ""
	}

	clientWeights := parseAcceptEncoding(acceptHeader)
	if len(clientWeights) == 0 {
		return ""
	}

	// If client explicitly rejects all encodings (*;q=0)
	if q, ok := clientWeights["*"]; ok && q <= 0 {
		return EncodingIdentity
	}

	// Check server preferences in order
	var bestCodec Encoding
	var bestQ float64 = -1

	for _, pref := range serverPreferences {
		name := pref.String()
		q, ok := clientWeights[name]
		if !ok {
			// Check wildcard
			if wq, wOk := clientWeights["*"]; wOk {
				q = wq
				ok = true
			}
		}

		if ok && q > 0 && q > bestQ {
			bestQ = q
			bestCodec = pref
		}
	}

	return bestCodec
}

func parseAcceptEncoding(header string) map[string]float64 {
	weights := make(map[string]float64)
	parts := strings.Split(header, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		params := strings.Split(part, ";")
		enc := strings.ToLower(strings.TrimSpace(params[0]))
		q := 1.0

		for _, param := range params[1:] {
			param = strings.TrimSpace(param)
			if strings.HasPrefix(param, "q=") {
				val := strings.TrimPrefix(param, "q=")
				if parsed, err := strconv.ParseFloat(val, 64); err == nil {
					q = parsed
				}
			}
		}

		weights[enc] = q
	}

	return weights
}
