package crypto_test

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/crypto"
	"github.com/golang-jwt/jwt/v5"
)

func generateRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	return key
}

func TestJWT_HS256_TableDriven(t *testing.T) {
	secret := []byte("super-secret-key")
	wrongSecret := []byte("wrong-secret-key")

	tests := []struct {
		name       string
		claims     crypto.Claims
		verifyKey  []byte
		wantErr    bool
		wantUserID string
	}{
		{
			name: "valid HS256 claims sign and verify successfully",
			claims: crypto.Claims{
				RegisteredClaims: jwt.RegisteredClaims{
					Subject:   "user-123",
					ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				},
				UserID: "user-123",
				Roles:  []string{"admin"},
			},
			verifyKey:  secret,
			wantErr:    false,
			wantUserID: "user-123",
		},
		{
			name: "verification with incorrect secret fails",
			claims: crypto.Claims{
				RegisteredClaims: jwt.RegisteredClaims{
					Subject:   "user-123",
					ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				},
			},
			verifyKey: wrongSecret,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token, err := crypto.SignHS256(tc.claims, secret)
			if err != nil {
				t.Fatalf("SignHS256: %v", err)
			}
			if token == "" {
				t.Fatal("expected non-empty token")
			}

			parsed, err := crypto.VerifyHS256(token, tc.verifyKey)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got=%v", tc.wantErr, err)
			}
			if !tc.wantErr && parsed.UserID != tc.wantUserID {
				t.Errorf("expected UserID %q, got %q", tc.wantUserID, parsed.UserID)
			}
		})
	}
}

func TestJWT_RS256_TableDriven(t *testing.T) {
	key := generateRSAKey(t)
	wrongKey := generateRSAKey(t)

	tests := []struct {
		name       string
		claims     crypto.Claims
		verifyKey  *rsa.PublicKey
		wantErr    bool
		wantUserID string
	}{
		{
			name: "valid RS256 claims sign and verify successfully",
			claims: crypto.Claims{
				RegisteredClaims: jwt.RegisteredClaims{
					Subject:   "user-rs",
					ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				},
				UserID: "user-rs",
				Roles:  []string{"viewer"},
			},
			verifyKey:  &key.PublicKey,
			wantErr:    false,
			wantUserID: "user-rs",
		},
		{
			name: "verification with mismatched RSA public key fails",
			claims: crypto.Claims{
				RegisteredClaims: jwt.RegisteredClaims{
					Subject:   "user-rs",
					ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				},
			},
			verifyKey: &wrongKey.PublicKey,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token, err := crypto.SignRS256(tc.claims, key)
			if err != nil {
				t.Fatalf("SignRS256: %v", err)
			}
			if token == "" {
				t.Fatal("expected non-empty token")
			}

			parsed, err := crypto.VerifyRS256(token, tc.verifyKey)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got=%v", tc.wantErr, err)
			}
			if !tc.wantErr && parsed.UserID != tc.wantUserID {
				t.Errorf("expected UserID %q, got %q", tc.wantUserID, parsed.UserID)
			}
		})
	}
}

func TestJWT_AlgorithmMismatch_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "verifying RS256 token as HS256 fails",
			run: func(t *testing.T) {
				key := generateRSAKey(t)
				claims := crypto.Claims{
					RegisteredClaims: jwt.RegisteredClaims{
						Subject:   "alg-test",
						ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
					},
				}
				token, _ := crypto.SignRS256(claims, key)
				_, err := crypto.VerifyHS256(token, []byte("any-secret"))
				if err == nil {
					t.Fatal("expected error when verifying RS256 token as HS256")
				}
			},
		},
		{
			name: "verifying HS256 token as RS256 fails",
			run: func(t *testing.T) {
				secret := []byte("some-secret")
				claims := crypto.Claims{
					RegisteredClaims: jwt.RegisteredClaims{
						Subject:   "alg-test",
						ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
					},
				}
				token, _ := crypto.SignHS256(claims, secret)
				key := generateRSAKey(t)
				_, err := crypto.VerifyRS256(token, &key.PublicKey)
				if err == nil {
					t.Fatal("expected error when verifying HS256 token as RS256")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
