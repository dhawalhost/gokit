package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dhawalhost/gokit/errors"
)

const (
	// DefaultHeaderSignature is the standard header containing the HMAC signature.
	DefaultHeaderSignature = "X-Webhook-Signature"
	// DefaultHeaderTimestamp is the standard header containing the UNIX timestamp.
	DefaultHeaderTimestamp = "X-Webhook-Timestamp"
	// DefaultMaxAge defines the replay attack window (5 minutes).
	DefaultMaxAge = 5 * time.Minute
)

// ComputeSignature calculates the HMAC-SHA256 hex digest of timestamp + "." + payload.
func ComputeSignature(payload []byte, timestamp string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature validates that signature matches HMAC-SHA256(timestamp + "." + payload)
// and that timestamp is within maxAge to prevent replay attacks.
func VerifySignature(payload []byte, signature, timestamp string, secret []byte, maxAge time.Duration) error {
	if signature == "" {
		return fmt.Errorf("webhook: missing signature")
	}
	if timestamp == "" {
		return fmt.Errorf("webhook: missing timestamp")
	}

	// 1. Verify timestamp to block replay attacks
	tsUnix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("webhook: invalid timestamp format: %w", err)
	}

	eventTime := time.Unix(tsUnix, 0).UTC()
	now := time.Now().UTC()
	if maxAge > 0 {
		if now.Sub(eventTime) > maxAge || eventTime.Sub(now) > maxAge {
			return fmt.Errorf("webhook: timestamp outside tolerance window (replay attack detected)")
		}
	}

	// 2. Compute expected HMAC
	expectedHex := ComputeSignature(payload, timestamp, secret)

	sig := strings.TrimPrefix(signature, "sha256=")

	// Constant-time comparison
	if !hmac.Equal([]byte(sig), []byte(expectedHex)) {
		return fmt.Errorf("webhook: signature mismatch")
	}

	return nil
}

// InboundMiddleware creates an HTTP middleware that verifies incoming webhook signatures.
func InboundMiddleware(secret []byte, maxAge time.Duration) func(http.Handler) http.Handler {
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sig := r.Header.Get(DefaultHeaderSignature)
			ts := r.Header.Get(DefaultHeaderTimestamp)

			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				errors.WriteError(w, r, errors.BadRequest("WEBHOOK_BODY_ERROR", "failed to read webhook body"))
				return
			}
			// Reassign readable body so downstream handlers can read it
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

			if err := VerifySignature(bodyBytes, sig, ts, secret, maxAge); err != nil {
				errors.WriteError(w, r, errors.Unauthorized("WEBHOOK_VERIFICATION_FAILED", err.Error()))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
