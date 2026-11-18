package middleware

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWKSOption configures NewJWKSKeyFunc behavior.
type JWKSOption func(*jwksProvider)

// WithJWKSCacheTTL sets how long fetched JWKS keys are cached (defaults to 5 minutes).
func WithJWKSCacheTTL(ttl time.Duration) JWKSOption {
	return func(p *jwksProvider) {
		if ttl > 0 {
			p.cacheTTL = ttl
		}
	}
}

// WithJWKSMinRefreshInterval sets the minimum delay between refreshes triggered by
// unknown key IDs to prevent upstream DoS during key-ID floods (defaults to 10 seconds).
func WithJWKSMinRefreshInterval(d time.Duration) JWKSOption {
	return func(p *jwksProvider) {
		if d >= 0 {
			p.minRefreshInterval = d
		}
	}
}

// WithJWKSHTTPClient configures a custom HTTP client used to fetch the JWKS endpoint.
func WithJWKSHTTPClient(c *http.Client) JWKSOption {
	return func(p *jwksProvider) {
		if c != nil {
			p.httpClient = c
		}
	}
}

type jwksProvider struct {
	jwksURL            string
	cacheTTL           time.Duration
	minRefreshInterval time.Duration
	httpClient         *http.Client

	mu        sync.RWMutex
	keys      map[string]any
	lastFetch time.Time
}

type jwksDocument struct {
	Keys []jwkEntry `json:"keys"`
}

type jwkEntry struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// NewJWKSKeyFunc returns a jwt.Keyfunc that fetches and caches JSON Web Key Sets from jwksURL,
// refreshing on TTL expiration or (rate-limited) unknown kid lookups.
func NewJWKSKeyFunc(jwksURL string, opts ...JWKSOption) jwt.Keyfunc {
	p := &jwksProvider{
		jwksURL:            jwksURL,
		cacheTTL:           5 * time.Minute,
		minRefreshInterval: 10 * time.Second,
		httpClient:         &http.Client{Timeout: 5 * time.Second},
		keys:               make(map[string]any),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}

	return func(token *jwt.Token) (any, error) {
		if token == nil {
			return nil, errors.New("middleware: nil jwt token")
		}
		kid, _ := token.Header["kid"].(string)
		return p.getKey(kid)
	}
}

func (p *jwksProvider) getKey(kid string) (any, error) {
	now := time.Now()
	p.mu.RLock()
	cacheValid := len(p.keys) > 0 && now.Sub(p.lastFetch) < p.cacheTTL
	if cacheValid {
		if key, ok := lookupKey(p.keys, kid); ok {
			p.mu.RUnlock()
			return key, nil
		}
	}
	canRefresh := now.Sub(p.lastFetch) >= p.minRefreshInterval || p.lastFetch.IsZero()
	p.mu.RUnlock()

	if !cacheValid || canRefresh {
		if err := p.refresh(); err != nil {
			// Fall back to cached keys if a refresh fails while cache still holds entries.
			p.mu.RLock()
			key, ok := lookupKey(p.keys, kid)
			p.mu.RUnlock()
			if ok {
				return key, nil
			}
			return nil, err
		}
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	if key, ok := lookupKey(p.keys, kid); ok {
		return key, nil
	}
	return nil, fmt.Errorf("middleware: jwk kid %q not found", kid)
}

func lookupKey(keys map[string]any, kid string) (any, bool) {
	if kid != "" {
		k, ok := keys[kid]
		return k, ok
	}
	if len(keys) == 1 {
		for _, k := range keys {
			return k, true
		}
	}
	return nil, false
}

func (p *jwksProvider) refresh() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	if !p.lastFetch.IsZero() && now.Sub(p.lastFetch) < p.minRefreshInterval && len(p.keys) > 0 && now.Sub(p.lastFetch) < p.cacheTTL {
		return nil
	}

	parsedURL, err := url.Parse(p.jwksURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return fmt.Errorf("middleware: invalid jwks url %q", p.jwksURL)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return fmt.Errorf("middleware: new jwks request: %w", err)
	}

	//nolint:gosec // #nosec G107,G704 -- jwksURL is validated above and configured by the service operator
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("middleware: fetch jwks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("middleware: jwks endpoint returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("middleware: read jwks response: %w", err)
	}

	var doc jwksDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("middleware: decode jwks: %w", err)
	}

	nextKeys := make(map[string]any, len(doc.Keys))
	for _, entry := range doc.Keys {
		if entry.Use != "" && entry.Use != "sig" {
			continue
		}
		pubKey, parseErr := parseJWKPublicKey(entry)
		if parseErr != nil {
			continue
		}
		keyID := entry.Kid
		if keyID == "" {
			keyID = fmt.Sprintf("unnamed-%d", len(nextKeys))
		}
		nextKeys[keyID] = pubKey
	}

	if len(nextKeys) == 0 {
		return errors.New("middleware: jwks response contained no valid signing keys")
	}

	p.keys = nextKeys
	p.lastFetch = time.Now()
	return nil
}

func parseJWKPublicKey(entry jwkEntry) (any, error) {
	switch entry.Kty {
	case "RSA":
		nBytes, err := base64.RawURLEncoding.DecodeString(entry.N)
		if err != nil || len(nBytes) == 0 {
			return nil, errors.New("invalid rsa modulus")
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(entry.E)
		if err != nil || len(eBytes) == 0 {
			return nil, errors.New("invalid rsa exponent")
		}
		var exp int
		for _, b := range eBytes {
			exp = exp<<8 + int(b)
		}
		if exp <= 1 {
			return nil, errors.New("invalid rsa exponent value")
		}
		return &rsa.PublicKey{
			N: new(big.Int).SetBytes(nBytes),
			E: exp,
		}, nil
	case "EC":
		var curve elliptic.Curve
		switch entry.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("unsupported ec curve %q", entry.Crv)
		}
		xBytes, err := base64.RawURLEncoding.DecodeString(entry.X)
		if err != nil || len(xBytes) == 0 {
			return nil, errors.New("invalid ec x coordinate")
		}
		yBytes, err := base64.RawURLEncoding.DecodeString(entry.Y)
		if err != nil || len(yBytes) == 0 {
			return nil, errors.New("invalid ec y coordinate")
		}
		coordSize := (curve.Params().BitSize + 7) / 8
		if len(xBytes) > coordSize || len(yBytes) > coordSize {
			return nil, errors.New("invalid ec coordinate length")
		}
		uncompressed := make([]byte, 1+2*coordSize)
		uncompressed[0] = 0x04
		copy(uncompressed[1+coordSize-len(xBytes):1+coordSize], xBytes)
		copy(uncompressed[1+2*coordSize-len(yBytes):], yBytes)
		return ecdsa.ParseUncompressedPublicKey(curve, uncompressed)
	default:
		return nil, fmt.Errorf("unsupported jwk key type %q", entry.Kty)
	}
}
