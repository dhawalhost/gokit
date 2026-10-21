package crypto

import (
	"crypto/rsa"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// Claims extends jwt.RegisteredClaims with application-specific fields.
type Claims struct {
	jwt.RegisteredClaims
	UserID   string   `json:"user_id"`
	TenantID string   `json:"tenant_id"`
	Roles    []string `json:"roles"`
}

// SignHS256 signs claims using HMAC-SHA256 and returns the token string.
func SignHS256(claims Claims, secret []byte) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	str, err := token.SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("crypto: sign hs256: %w", err)
	}
	return str, nil
}

// VerifyHS256 parses and validates an HS256-signed token string.
// This function validates the token signature, expiration, and other standard claims.
func VerifyHS256(tokenStr string, secret []byte) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{},
		func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("%w: %v", ErrUnexpectedSigningMethod, t.Header["alg"])
			}
			return secret, nil
		},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("crypto: verify hs256: %w", err)
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidTokenClaims
	}
	return claims, nil
}

// SignRS256 signs claims using RSA-SHA256 and returns the token string.
func SignRS256(claims Claims, privateKey *rsa.PrivateKey) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	str, err := token.SignedString(privateKey)
	if err != nil {
		return "", fmt.Errorf("crypto: sign rs256: %w", err)
	}
	return str, nil
}

// VerifyRS256 parses and validates an RS256-signed token string.
// This function validates the token signature, expiration, and other standard claims.
func VerifyRS256(tokenStr string, publicKey *rsa.PublicKey) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{},
		func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("%w: %v", ErrUnexpectedSigningMethod, t.Header["alg"])
			}
			return publicKey, nil
		},
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("crypto: verify rs256: %w", err)
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidTokenClaims
	}
	return claims, nil
}
