package secrets_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/secrets"
)

func TestSecret_RedactionAndZeroing(t *testing.T) {
	raw := "super-sensitive-api-token"
	sec := secrets.NewSecret(raw,
		secrets.WithVersion("v1.2"),
		secrets.WithMetadata(map[string]string{"env": "production"}),
	)

	// Unmasked retrieval
	if sec.Value() != raw {
		t.Errorf("expected %s, got %s", raw, sec.Value())
	}
	if string(sec.Bytes()) != raw {
		t.Errorf("expected %s in bytes, got %s", raw, string(sec.Bytes()))
	}
	if sec.Version() != "v1.2" {
		t.Errorf("expected v1.2, got %s", sec.Version())
	}
	if sec.Metadata()["env"] != "production" {
		t.Errorf("expected production metadata, got: %v", sec.Metadata())
	}
	if sec.UpdatedAt().IsZero() {
		t.Errorf("expected non-zero updatedAt")
	}

	// Redaction in fmt / string
	if sec.String() != "[REDACTED]" {
		t.Errorf("expected [REDACTED], got: %s", sec.String())
	}
	if sec.GoString() != "[REDACTED]" {
		t.Errorf("expected %#v to be [REDACTED], got: %s", sec, sec.GoString())
	}

	// Redaction in JSON
	jsonBytes, err := json.Marshal(sec)
	if err != nil || string(jsonBytes) != `"[REDACTED]"` {
		t.Errorf("expected JSON to be \"[REDACTED]\", got: %s (%v)", string(jsonBytes), err)
	}

	// Redaction in Text
	textBytes, err := sec.MarshalText()
	if err != nil || string(textBytes) != "[REDACTED]" {
		t.Errorf("expected Text to be [REDACTED], got: %s (%v)", string(textBytes), err)
	}

	// Defensive copy check
	bytesCopy := sec.Bytes()
	bytesCopy[0] = 'X'
	if sec.Value() == string(bytesCopy) {
		t.Errorf("Bytes() should return a defensive copy")
	}

	// Memory Zeroing
	sec.Zero()
	if sec.Value() != "" {
		t.Errorf("expected empty string after zeroing, got: %s", sec.Value())
	}
	if sec.Bytes() != nil {
		t.Errorf("expected nil bytes after zeroing")
	}
}

func TestSecret_NilReceiver(t *testing.T) {
	var sec *secrets.Secret
	if sec.Value() != "" {
		t.Errorf("expected empty string for nil secret")
	}
	if sec.Bytes() != nil {
		t.Errorf("expected nil bytes for nil secret")
	}
	if sec.Version() != "" {
		t.Errorf("expected empty version for nil secret")
	}
	if sec.Metadata() != nil {
		t.Errorf("expected nil metadata for nil secret")
	}
	if !sec.UpdatedAt().IsZero() {
		t.Errorf("expected zero time for nil secret")
	}
	sec.Zero() // should not panic
}

func TestMemoryProvider(t *testing.T) {
	ctx := context.Background()
	p := secrets.NewMemoryProvider()

	// Missing key
	_, err := p.Get(ctx, "missing")
	if !errors.Is(err, secrets.ErrSecretNotFound) {
		t.Errorf("expected ErrSecretNotFound, got: %v", err)
	}

	// Nil secret
	if err := p.Set(ctx, "key", nil); !errors.Is(err, secrets.ErrInvalidSecret) {
		t.Errorf("expected ErrInvalidSecret for nil secret, got: %v", err)
	}

	// Set & Get
	sec := secrets.NewSecret("secret-value")
	if err := p.Set(ctx, "db_pass", sec); err != nil {
		t.Fatalf("failed to set secret: %v", err)
	}

	got, err := p.Get(ctx, "db_pass")
	if err != nil || got.Value() != "secret-value" {
		t.Fatalf("expected secret-value, got %v (%v)", got, err)
	}

	// Delete
	if err := p.Delete(ctx, "db_pass"); err != nil {
		t.Fatalf("failed to delete secret: %v", err)
	}
	if err := p.Delete(ctx, "db_pass"); !errors.Is(err, secrets.ErrSecretNotFound) {
		t.Errorf("expected ErrSecretNotFound on second delete, got: %v", err)
	}

	// Cancelled context
	cancCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.Get(cancCtx, "any"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Get, got: %v", err)
	}
	if err := p.Set(cancCtx, "any", sec); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Set, got: %v", err)
	}
	if err := p.Delete(cancCtx, "any"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Delete, got: %v", err)
	}
}

func TestEnvProvider(t *testing.T) {
	ctx := context.Background()
	p := secrets.NewEnvProvider("TEST_APP_")

	// Missing
	_, err := p.Get(ctx, "non.existent.key")
	if !errors.Is(err, secrets.ErrSecretNotFound) {
		t.Errorf("expected ErrSecretNotFound, got: %v", err)
	}

	// Nil secret
	if err := p.Set(ctx, "key", nil); !errors.Is(err, secrets.ErrInvalidSecret) {
		t.Errorf("expected ErrInvalidSecret, got: %v", err)
	}

	// Set & Get (tests key normalization with dots and dashes)
	sec := secrets.NewSecret("env-secret-123")
	if err := p.Set(ctx, "db.password-primary", sec); err != nil {
		t.Fatalf("failed to set env secret: %v", err)
	}

	got, err := p.Get(ctx, "db.password-primary")
	if err != nil || got.Value() != "env-secret-123" {
		t.Fatalf("expected env-secret-123, got %v (%v)", got, err)
	}

	// Check underlying env var name
	rawVal := os.Getenv("TEST_APP_DB_PASSWORD_PRIMARY")
	if rawVal != "env-secret-123" {
		t.Errorf("expected TEST_APP_DB_PASSWORD_PRIMARY to be env-secret-123, got: %s", rawVal)
	}

	// Delete
	if err := p.Delete(ctx, "db.password-primary"); err != nil {
		t.Fatalf("failed to delete env var: %v", err)
	}
	if err := p.Delete(ctx, "db.password-primary"); !errors.Is(err, secrets.ErrSecretNotFound) {
		t.Errorf("expected ErrSecretNotFound on deleted env var, got: %v", err)
	}

	// Cancelled context
	cancCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.Get(cancCtx, "any"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
	if err := p.Set(cancCtx, "any", sec); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
	if err := p.Delete(cancCtx, "any"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestFileProvider(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	p := secrets.NewFileProvider(tempDir)

	// Missing
	_, err := p.Get(ctx, "missing.key")
	if !errors.Is(err, secrets.ErrSecretNotFound) {
		t.Errorf("expected ErrSecretNotFound, got: %v", err)
	}

	// Nil secret
	if err := p.Set(ctx, "key", nil); !errors.Is(err, secrets.ErrInvalidSecret) {
		t.Errorf("expected ErrInvalidSecret, got: %v", err)
	}

	// Set & Get
	sec := secrets.NewSecret("file-secret-data")
	if err := p.Set(ctx, "api-key", sec); err != nil {
		t.Fatalf("failed to set file secret: %v", err)
	}

	got, err := p.Get(ctx, "api-key")
	if err != nil || got.Value() != "file-secret-data" {
		t.Fatalf("expected file-secret-data, got %v (%v)", got, err)
	}

	// Verify file permissions and content on disk
	content, err := os.ReadFile(filepath.Join(tempDir, "api-key"))
	if err != nil || string(content) != "file-secret-data" {
		t.Errorf("unexpected file content on disk: %s", string(content))
	}

	// Delete
	if err := p.Delete(ctx, "api-key"); err != nil {
		t.Fatalf("failed to delete file secret: %v", err)
	}
	if err := p.Delete(ctx, "api-key"); !errors.Is(err, secrets.ErrSecretNotFound) {
		t.Errorf("expected ErrSecretNotFound on deleted file, got: %v", err)
	}

	// Cancelled context
	cancCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.Get(cancCtx, "any"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
	if err := p.Set(cancCtx, "any", sec); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
	if err := p.Delete(cancCtx, "any"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestChainProvider(t *testing.T) {
	ctx := context.Background()

	p1 := secrets.NewMemoryProvider()
	p2 := secrets.NewMemoryProvider()

	chain := secrets.NewChainProvider(p1, p2)

	_ = p2.Set(ctx, "token", secrets.NewSecret("p2-token"))

	// Should fallback to p2
	got, err := chain.Get(ctx, "token")
	if err != nil || got.Value() != "p2-token" {
		t.Fatalf("expected p2-token, got %v (%v)", got, err)
	}

	// Set writes to p1 (the first provider)
	_ = chain.Set(ctx, "new_key", secrets.NewSecret("new_val"))
	p1Got, _ := p1.Get(ctx, "new_key")
	if p1Got == nil || p1Got.Value() != "new_val" {
		t.Errorf("expected secret written to p1")
	}

	// Delete
	if err := chain.Delete(ctx, "token"); err != nil {
		t.Fatalf("failed to delete across chain: %v", err)
	}
	if err := chain.Delete(ctx, "token"); !errors.Is(err, secrets.ErrSecretNotFound) {
		t.Errorf("expected ErrSecretNotFound on missing key delete, got: %v", err)
	}

	// Empty chain
	emptyChain := secrets.NewChainProvider()
	if _, err := emptyChain.Get(ctx, "any"); !errors.Is(err, secrets.ErrSecretNotFound) {
		t.Errorf("expected ErrSecretNotFound for empty chain, got: %v", err)
	}
	if err := emptyChain.Set(ctx, "any", secrets.NewSecret("v")); !errors.Is(err, secrets.ErrProviderUnavailable) {
		t.Errorf("expected ErrProviderUnavailable, got: %v", err)
	}
}

func TestManager_CachingAndAutoRefresh(t *testing.T) {
	ctx := context.Background()
	provider := secrets.NewMemoryProvider()

	mgr := secrets.NewManager(provider,
		secrets.WithTTL(50*time.Millisecond),
		secrets.WithCache(true),
		secrets.WithAutoRefresh(15*time.Millisecond),
	)
	defer mgr.Close()

	sec := secrets.NewSecret("initial-password")
	_ = mgr.Set(ctx, "db_pass", sec)

	// Fetch from cache
	got, err := mgr.Get(ctx, "db_pass")
	if err != nil || got.Value() != "initial-password" {
		t.Fatalf("expected initial-password, got %v (%v)", got, err)
	}

	// Update directly in underlying provider (simulating external rotation)
	rotated := secrets.NewSecret("rotated-password")
	_ = provider.Set(ctx, "db_pass", rotated)

	// Immediate Get should still return cached value
	gotCached, _ := mgr.Get(ctx, "db_pass")
	if gotCached.Value() != "initial-password" {
		t.Errorf("expected cached value initial-password, got: %s", gotCached.Value())
	}

	// Wait for background auto-refresh loop to sync
	time.Sleep(70 * time.Millisecond)

	refreshed, err := mgr.Get(ctx, "db_pass")
	if err != nil || refreshed.Value() != "rotated-password" {
		t.Fatalf("expected rotated-password after refresh, got %v (%v)", refreshed, err)
	}

	// Delete
	if err := mgr.Delete(ctx, "db_pass"); err != nil {
		t.Fatalf("failed to delete secret: %v", err)
	}
	if _, err := mgr.Get(ctx, "db_pass"); !errors.Is(err, secrets.ErrSecretNotFound) {
		t.Errorf("expected ErrSecretNotFound after delete, got: %v", err)
	}

	// Closed manager
	mgr.Close()
	if _, err := mgr.Get(ctx, "any"); !errors.Is(err, secrets.ErrManagerClosed) {
		t.Errorf("expected ErrManagerClosed on Get, got: %v", err)
	}
	if err := mgr.Set(ctx, "any", sec); !errors.Is(err, secrets.ErrManagerClosed) {
		t.Errorf("expected ErrManagerClosed on Set, got: %v", err)
	}
	if err := mgr.Delete(ctx, "any"); !errors.Is(err, secrets.ErrManagerClosed) {
		t.Errorf("expected ErrManagerClosed on Delete, got: %v", err)
	}
}

func TestManager_DisabledCache(t *testing.T) {
	ctx := context.Background()
	provider := secrets.NewMemoryProvider()

	mgr := secrets.NewManager(provider,
		secrets.WithCache(false),
	)
	defer mgr.Close()

	_ = mgr.Set(ctx, "key", secrets.NewSecret("v1"))

	// Direct update in provider
	_ = provider.Set(ctx, "key", secrets.NewSecret("v2"))

	got, err := mgr.Get(ctx, "key")
	if err != nil || got.Value() != "v2" {
		t.Fatalf("expected immediate v2 without cache, got %v (%v)", got, err)
	}

	_ = mgr.Delete(ctx, "key")
	if _, err := mgr.Get(ctx, "key"); !errors.Is(err, secrets.ErrSecretNotFound) {
		t.Errorf("expected ErrSecretNotFound, got: %v", err)
	}
}
