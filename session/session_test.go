package session_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/session"
)

func TestSession_Values_And_Types(t *testing.T) {
	s := session.NewSession("fp-test")

	// Initial values
	if s.GetString("user") != "" {
		t.Errorf("expected empty string, got %s", s.GetString("user"))
	}
	if s.GetInt("age") != 0 {
		t.Errorf("expected 0, got %d", s.GetInt("age"))
	}
	if s.GetBool("is_admin") {
		t.Errorf("expected false, got true")
	}

	// Set values
	s.Set("user", "alice")
	s.Set("age", 30)
	s.Set("score", float64(99.5))
	s.Set("int64_val", int64(100))
	s.Set("is_admin", true)

	if s.GetString("user") != "alice" {
		t.Errorf("expected alice, got %s", s.GetString("user"))
	}
	if s.GetInt("age") != 30 {
		t.Errorf("expected 30, got %d", s.GetInt("age"))
	}
	if s.GetInt("score") != 99 {
		t.Errorf("expected 99, got %d", s.GetInt("score"))
	}
	if s.GetInt("int64_val") != 100 {
		t.Errorf("expected 100, got %d", s.GetInt("int64_val"))
	}
	if s.GetInt("user") != 0 {
		t.Errorf("expected 0 for string to int, got %d", s.GetInt("user"))
	}
	if !s.GetBool("is_admin") {
		t.Errorf("expected true, got false")
	}

	// Delete
	s.Delete("age")
	if _, ok := s.Get("age"); ok {
		t.Errorf("expected age to be deleted")
	}

	// Clear
	s.Clear()
	if s.GetString("user") != "" {
		t.Errorf("expected user to be cleared")
	}
}

func TestSession_FlashMessages(t *testing.T) {
	s := session.NewSession("fp-test")

	s.AddFlash("info", "Welcome!")
	s.AddFlash("info", "Check your email.")

	flashes := s.Flashes("info")
	if len(flashes) != 2 {
		t.Fatalf("expected 2 flash messages, got %d", len(flashes))
	}
	if flashes[0] != "Welcome!" || flashes[1] != "Check your email." {
		t.Errorf("unexpected flash messages: %v", flashes)
	}

	// Second read should be empty (popped)
	emptyFlashes := s.Flashes("info")
	if len(emptyFlashes) != 0 {
		t.Errorf("expected 0 flashes on second read, got %d", len(emptyFlashes))
	}

	// Non-existent key
	if s.Flashes("warn") != nil {
		t.Errorf("expected nil flashes for non-existent key")
	}
}

func TestSession_Regenerate_And_Destroy(t *testing.T) {
	s := session.NewSession("fp-test")
	origID := s.ID()

	s.Regenerate()
	if !s.IsModified() {
		t.Errorf("expected session to be marked modified after regenerate")
	}

	s.Destroy()
	if !s.IsDestroyed() {
		t.Errorf("expected session to be destroyed")
	}

	// Mutations ignored after destroy
	s.Set("key", "val")
	if _, ok := s.Get("key"); ok {
		t.Errorf("expected Set to be ignored on destroyed session")
	}
	s.AddFlash("key", "val")
	if s.Flashes("key") != nil {
		t.Errorf("expected AddFlash to be ignored on destroyed session")
	}

	if s.ID() != origID {
		t.Errorf("ID should not change until committed")
	}
}

func TestMemoryStore_Lifecycle(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore(50 * time.Millisecond)
	defer store.Close()

	data := &session.Data{
		Values:     map[string]any{"user_id": "u-123"},
		CreatedAt:  time.Now().UTC(),
		AccessedAt: time.Now().UTC(),
	}

	// Save
	if err := store.Save(ctx, "sess-1", data, 100*time.Millisecond); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load
	loaded, err := store.Load(ctx, "sess-1")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Values["user_id"] != "u-123" {
		t.Errorf("loaded user_id mismatch: %v", loaded.Values["user_id"])
	}

	// Touch
	if err := store.Touch(ctx, "sess-1", 200*time.Millisecond); err != nil {
		t.Fatalf("Touch failed: %v", err)
	}

	// Destroy
	if err := store.Destroy(ctx, "sess-1"); err != nil {
		t.Fatalf("Destroy failed: %v", err)
	}
	_, err = store.Load(ctx, "sess-1")
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound after destroy, got %v", err)
	}

	// Cancelled context
	cancCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.Load(cancCtx, "sess-1"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Load, got %v", err)
	}
	if err := store.Save(cancCtx, "sess-1", data, time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Save, got %v", err)
	}
	if err := store.Destroy(cancCtx, "sess-1"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Destroy, got %v", err)
	}
	if err := store.Touch(cancCtx, "sess-1", time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Touch, got %v", err)
	}
}

func TestCookieStore_Encryption_Decryption(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)

	// Invalid key size
	_, err := session.NewCookieStore([]byte("too-short"))
	if err == nil {
		t.Errorf("expected error for invalid key length")
	}

	cs, err := session.NewCookieStore(key)
	if err != nil {
		t.Fatalf("NewCookieStore failed: %v", err)
	}

	data := &session.Data{
		Values:     map[string]any{"role": "admin"},
		CreatedAt:  time.Now().UTC(),
		AccessedAt: time.Now().UTC(),
	}

	// Encrypt
	ciphertext, err := cs.Encrypt(data)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Decrypt
	decrypted, err := cs.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if decrypted.Values["role"] != "admin" {
		t.Errorf("expected admin, got %v", decrypted.Values["role"])
	}

	// Tampered ciphertext
	_, err = cs.Decrypt("tampered-data-gibberish")
	if !errors.Is(err, session.ErrInvalidSession) {
		t.Errorf("expected ErrInvalidSession for tampered data, got %v", err)
	}

	// Store interface operations
	ctx := context.Background()
	if err := cs.Save(ctx, "c-1", data, time.Hour); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	loaded, err := cs.Load(ctx, "c-1")
	if err != nil || loaded.Values["role"] != "admin" {
		t.Errorf("Load failed or role mismatch: %v", err)
	}
	if err := cs.Touch(ctx, "c-1", time.Hour); err != nil {
		t.Fatalf("Touch failed: %v", err)
	}
	if err := cs.Destroy(ctx, "c-1"); err != nil {
		t.Fatalf("Destroy failed: %v", err)
	}
	_, err = cs.Load(ctx, "c-1")
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound after destroy, got %v", err)
	}
}

func TestManager_AntiHijacking(t *testing.T) {
	store := session.NewMemoryStore(time.Minute)
	defer store.Close()

	mgr, err := session.NewManager(store,
		session.WithAntiHijacking(true),
		session.WithIdleTimeout(10*time.Minute),
	)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// 1. Initial request from IP 192.168.1.50
	r1 := httptest.NewRequest("GET", "/", nil)
	r1.RemoteAddr = "192.168.1.50:54321"
	r1.Header.Set("User-Agent", "Mozilla/5.0")

	sess1, err := mgr.Load(r1)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	sess1.Set("auth", true)

	rec1 := httptest.NewRecorder()
	if err := mgr.Commit(rec1, sess1); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	cookies := rec1.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("expected session cookie to be set")
	}
	sessionCookie := cookies[0]

	// 2. Legitimate request from same subnet (e.g. 192.168.1.51 /24 subnet) and same User-Agent
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "192.168.1.51:54321"
	r2.Header.Set("User-Agent", "Mozilla/5.0")
	r2.AddCookie(sessionCookie)

	sess2, err := mgr.Load(r2)
	if err != nil {
		t.Fatalf("legitimate subnet load failed: %v", err)
	}
	if !sess2.GetBool("auth") {
		t.Errorf("expected session auth to be true")
	}

	// 3. Hijacking attempt from completely different IP subnet (e.g. 10.0.0.1) or different User-Agent
	r3 := httptest.NewRequest("GET", "/", nil)
	r3.RemoteAddr = "10.0.0.1:12345"
	r3.Header.Set("User-Agent", "curl/7.68.0")
	r3.AddCookie(sessionCookie)

	_, err = mgr.Load(r3)
	if !errors.Is(err, session.ErrHijackingDetected) {
		t.Errorf("expected ErrHijackingDetected, got %v", err)
	}
}

func TestManager_Timeouts(t *testing.T) {
	store := session.NewMemoryStore(time.Minute)
	defer store.Close()

	mgr, _ := session.NewManager(store,
		session.WithIdleTimeout(50*time.Millisecond),
		session.WithAbsoluteTimeout(100*time.Millisecond),
		session.WithAntiHijacking(false),
	)

	// Create and commit session
	r := httptest.NewRequest("GET", "/", nil)
	sess, _ := mgr.Load(r)
	sess.Set("test", "active")
	rec := httptest.NewRecorder()
	_ = mgr.Commit(rec, sess)

	cookie := rec.Result().Cookies()[0]

	// Wait for idle timeout
	time.Sleep(70 * time.Millisecond)

	rExpired := httptest.NewRequest("GET", "/", nil)
	rExpired.AddCookie(cookie)
	reloaded, _ := mgr.Load(rExpired)

	if reloaded.GetString("test") != "" {
		t.Errorf("expected session to have expired due to idle timeout")
	}
}

func TestManager_Middleware_Full_HTTP_Cycle(t *testing.T) {
	store := session.NewMemoryStore(time.Minute)
	defer store.Close()

	mgr, _ := session.NewManager(store,
		session.WithCookieName("sid"),
		session.WithCookiePath("/"),
		session.WithCookieSecure(false),
		session.WithCookieHTTPOnly(true),
		session.WithCookieSameSite(http.SameSiteLaxMode),
		session.WithCookiePartitioned(true),
		session.WithIdleTimeout(10*time.Minute),
		session.WithAntiHijacking(false),
	)

	mux := http.NewServeMux()

	// Login endpoint: sets user and regenerates session
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		s := session.FromContext(r.Context())
		if s == nil {
			http.Error(w, "missing session", http.StatusInternalServerError)
			return
		}
		s.Set("user_id", "42")
		s.AddFlash("auth", "logged_in")
		s.Regenerate()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("logged in"))
	})

	// Profile endpoint: reads user and consumes flash
	mux.HandleFunc("/profile", func(w http.ResponseWriter, r *http.Request) {
		s := session.FromContext(r.Context())
		if s == nil {
			http.Error(w, "missing session", http.StatusInternalServerError)
			return
		}
		userID := s.GetString("user_id")
		flashes := s.Flashes("auth")
		flashStr := ""
		if len(flashes) > 0 {
			flashStr = fmt.Sprintf("%v", flashes[0])
		}
		_, _ = fmt.Fprintf(w, "user=%s,flash=%s", userID, flashStr)
	})

	// Logout endpoint: destroys session
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		s := session.FromContext(r.Context())
		s.Destroy()
		w.WriteHeader(http.StatusOK)
	})

	handler := mgr.Middleware()(mux)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := ts.Client()

	// 1. Hit /login
	resp1, err := client.Get(ts.URL + "/login")
	if err != nil {
		t.Fatalf("login request failed: %v", err)
	}
	defer resp1.Body.Close()

	var sidCookie *http.Cookie
	for _, c := range resp1.Cookies() {
		if c.Name == "sid" {
			sidCookie = c
			break
		}
	}
	if sidCookie == nil || sidCookie.Value == "" {
		t.Fatalf("expected sid cookie on login")
	}

	// 2. Hit /profile with cookie
	req2, _ := http.NewRequest("GET", ts.URL+"/profile", nil)
	req2.AddCookie(sidCookie)
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("profile request failed: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	if string(body2) != "user=42,flash=logged_in" {
		t.Errorf("expected 'user=42,flash=logged_in', got %q", string(body2))
	}

	// 3. Hit /profile second time: flash should be gone
	req3, _ := http.NewRequest("GET", ts.URL+"/profile", nil)
	req3.AddCookie(sidCookie)
	resp3, err := client.Do(req3)
	if err != nil {
		t.Fatalf("second profile request failed: %v", err)
	}
	body3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()

	if string(body3) != "user=42,flash=" {
		t.Errorf("expected flash to be consumed, got %q", string(body3))
	}

	// 4. Hit /logout
	req4, _ := http.NewRequest("GET", ts.URL+"/logout", nil)
	req4.AddCookie(sidCookie)
	resp4, err := client.Do(req4)
	if err != nil {
		t.Fatalf("logout request failed: %v", err)
	}
	resp4.Body.Close()

	var logoutCookie *http.Cookie
	for _, c := range resp4.Cookies() {
		if c.Name == "sid" {
			logoutCookie = c
			break
		}
	}
	if logoutCookie == nil || logoutCookie.MaxAge != -1 {
		t.Errorf("expected deletion cookie with MaxAge: -1 on logout, got %+v", logoutCookie)
	}
}

func TestManager_NilStore_Error(t *testing.T) {
	_, err := session.NewManager(nil)
	if !errors.Is(err, session.ErrNilStore) {
		t.Errorf("expected ErrNilStore, got %v", err)
	}
}

func TestManager_FromContext_Nil(t *testing.T) {
	if s := session.FromContext(nil); s != nil {
		t.Errorf("expected nil for nil context, got %v", s)
	}
	if s := session.FromContext(context.Background()); s != nil {
		t.Errorf("expected nil for empty context, got %v", s)
	}
}

func TestManager_Commit_Nil(t *testing.T) {
	store := session.NewMemoryStore(time.Minute)
	defer store.Close()
	mgr, _ := session.NewManager(store)
	if err := mgr.Commit(httptest.NewRecorder(), nil); err != nil {
		t.Errorf("expected nil error on commit nil session, got %v", err)
	}
}

func TestManager_Middleware_HijackingBlock(t *testing.T) {
	store := session.NewMemoryStore(time.Minute)
	defer store.Close()
	mgr, _ := session.NewManager(store,
		session.WithAntiHijacking(true),
		session.WithCookieDomain("example.com"),
		session.WithFingerprintFunc(session.DefaultFingerprint),
	)

	// Establish session
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "1.2.3.4:1234"
	r.Header.Set("User-Agent", "GoodAgent")
	sess, _ := mgr.Load(r)
	sess.Set("ok", true)
	rec := httptest.NewRecorder()
	_ = mgr.Commit(rec, sess)

	cookie := rec.Result().Cookies()[0]

	// Send through middleware with hijacked User-Agent
	handler := mgr.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	badReq := httptest.NewRequest("GET", "/", nil)
	badReq.RemoteAddr = "1.2.3.4:1234"
	badReq.Header.Set("User-Agent", "BadAgent")
	badReq.AddCookie(cookie)

	badRec := httptest.NewRecorder()
	handler.ServeHTTP(badRec, badReq)

	if badRec.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden on hijacking, got %d", badRec.Code)
	}
}

func TestCookieStore_EdgeCases(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	cs, _ := session.NewCookieStore(key)
	ctx := context.Background()

	// Encrypt nil
	if _, err := cs.Encrypt(nil); !errors.Is(err, session.ErrInvalidSession) {
		t.Errorf("expected ErrInvalidSession on Encrypt(nil), got %v", err)
	}

	// Decrypt empty and short
	if _, err := cs.Decrypt(""); !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound on Decrypt(''), got %v", err)
	}
	if _, err := cs.Decrypt("AQ"); !errors.Is(err, session.ErrInvalidSession) {
		t.Errorf("expected ErrInvalidSession on Decrypt('AQ'), got %v", err)
	}

	// Load empty ID
	if _, err := cs.Load(ctx, ""); !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound on Load(''), got %v", err)
	}

	// Save invalid
	if err := cs.Save(ctx, "", nil, time.Hour); !errors.Is(err, session.ErrInvalidSession) {
		t.Errorf("expected ErrInvalidSession on Save('', nil), got %v", err)
	}

	// Touch non-existent
	if err := cs.Touch(ctx, "non-existent", time.Hour); !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound on Touch, got %v", err)
	}

	// Expired transit entry
	_ = cs.Save(ctx, "expired-key", &session.Data{}, -time.Second)
	if _, err := cs.Load(ctx, "expired-key"); !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound for expired transit key, got %v", err)
	}

	// Context cancellations
	cancCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := cs.Load(cancCtx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Load, got %v", err)
	}
	if err := cs.Save(cancCtx, "k", &session.Data{}, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Save, got %v", err)
	}
	if err := cs.Destroy(cancCtx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Destroy, got %v", err)
	}
	if err := cs.Touch(cancCtx, "k", time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on Touch, got %v", err)
	}
}

func TestMemoryStore_EdgeCases(t *testing.T) {
	ctx := context.Background()
	ms := session.NewMemoryStore(time.Minute)
	defer ms.Close()

	if _, err := ms.Load(ctx, ""); !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound on Load(''), got %v", err)
	}

	if err := ms.Save(ctx, "", nil, time.Hour); !errors.Is(err, session.ErrInvalidSession) {
		t.Errorf("expected ErrInvalidSession on Save('', nil), got %v", err)
	}

	if err := ms.Touch(ctx, "non-existent", time.Hour); !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound on Touch('non-existent'), got %v", err)
	}

	// Expired item
	_ = ms.Save(ctx, "expired-sess", &session.Data{}, -time.Second)
	if _, err := ms.Load(ctx, "expired-sess"); !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound for expired session, got %v", err)
	}
}

func TestDefaultFingerprint_IPv6_And_Headers(t *testing.T) {
	// IPv6 test
	r6 := httptest.NewRequest("GET", "/", nil)
	r6.RemoteAddr = "[2001:db8::1]:12345"
	r6.Header.Set("User-Agent", "TestAgent")
	fp6 := session.DefaultFingerprint(r6)
	if fp6 == "" {
		t.Errorf("expected non-empty IPv6 fingerprint")
	}

	// X-Forwarded-For with multiple IPs
	rXFF := httptest.NewRequest("GET", "/", nil)
	rXFF.Header.Set("X-Forwarded-For", "203.0.113.195, 70.41.3.18, 150.172.238.178")
	rXFF.Header.Set("User-Agent", "TestAgent")
	fpXFF := session.DefaultFingerprint(rXFF)
	if fpXFF == "" {
		t.Errorf("expected non-empty XFF fingerprint")
	}
}
