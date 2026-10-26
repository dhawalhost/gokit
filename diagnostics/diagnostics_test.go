package diagnostics_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/diagnostics"
)

func TestMemorySummary_Read(t *testing.T) {
	summary := diagnostics.ReadMemorySummary()

	if summary.Alloc == 0 {
		t.Errorf("expected non-zero Alloc")
	}
	if summary.Sys == 0 {
		t.Errorf("expected non-zero Sys")
	}
	if summary.Timestamp.IsZero() {
		t.Errorf("expected non-zero Timestamp")
	}
}

func TestMemoryWatchdog_Watermark_Trigger_And_Cooldown(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gokit-watchdog-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	var hookCalled int
	var lastPath string
	var mu sync.Mutex

	// Threshold set to 1 byte ensures watermark is immediately breached
	w := diagnostics.NewMemoryWatchdog(
		diagnostics.WithMaxHeapAlloc(1),
		diagnostics.WithCooldown(50*time.Millisecond),
		diagnostics.WithCheckInterval(10*time.Millisecond),
		diagnostics.WithDumpDir(tempDir),
		diagnostics.WithWatermarkHook(func(summary diagnostics.MemorySummary, dumpPath string) {
			mu.Lock()
			hookCalled++
			lastPath = dumpPath
			mu.Unlock()
		}),
	)

	// 1. First check: should breach and create dump
	breached, _, checkErr := w.CheckNow()
	if !breached {
		t.Errorf("expected memory watermark to be breached")
	}
	if checkErr != nil {
		t.Errorf("unexpected check error: %v", checkErr)
	}

	mu.Lock()
	if hookCalled != 1 || lastPath == "" {
		t.Errorf("expected hook to be called once with valid dump path, got called %d, path %s", hookCalled, lastPath)
	}
	firstDumpPath := lastPath
	mu.Unlock()

	// Verify file exists on disk
	if _, statErr := os.Stat(firstDumpPath); statErr != nil {
		t.Errorf("dump file not found on disk: %v", statErr)
	}

	// 2. Immediate second check: within 50ms cooldown window, hook and dump should be suppressed
	breached2, _, _ := w.CheckNow()
	if !breached2 {
		t.Errorf("expected breached to remain true")
	}

	mu.Lock()
	if hookCalled != 1 {
		t.Errorf("expected dump to be suppressed by cooldown, but hook count is %d", hookCalled)
	}
	mu.Unlock()

	// 3. Wait for cooldown to expire and check again
	time.Sleep(60 * time.Millisecond)
	_, _, _ = w.CheckNow()

	mu.Lock()
	if hookCalled != 2 {
		t.Errorf("expected hook to be called a second time after cooldown expired, got %d", hookCalled)
	}
	mu.Unlock()

	// Test Start and Stop
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if startErr := w.Start(ctx); startErr != nil {
		t.Errorf("Start failed: %v", startErr)
	}
	// Start when already running
	if startErr := w.Start(ctx); startErr != nil {
		t.Errorf("second Start should be no-op: %v", startErr)
	}

	w.Stop()
	w.Stop() // idempotent

	// Start on stopped watchdog
	if startErr := w.Start(ctx); !errors.Is(startErr, diagnostics.ErrWatchdogStopped) {
		t.Errorf("expected ErrWatchdogStopped on stopped watchdog, got %v", startErr)
	}
}

func TestMemoryWatchdog_NotBreached_And_SysAlloc(t *testing.T) {
	w := diagnostics.NewMemoryWatchdog(
		diagnostics.WithMaxHeapAlloc(100*1024*1024*1024), // 100 GB
		diagnostics.WithMaxSysAlloc(100*1024*1024*1024),
	)

	breached, _, err := w.CheckNow()
	if breached {
		t.Errorf("expected breached to be false for 100GB threshold")
	}
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGoroutineLeakDetector_NoLeak(t *testing.T) {
	baseline := diagnostics.CaptureGoroutines()
	if baseline.Count == 0 {
		t.Fatalf("expected non-zero baseline count")
	}

	// Do synchronous work
	sum := 0
	for i := 0; i < 1000; i++ {
		sum += i
	}
	_ = sum

	if err := diagnostics.VerifyNoGoroutineLeak(baseline, diagnostics.WithTolerance(0)); err != nil {
		t.Errorf("unexpected goroutine leak: %v", err)
	}
}

func TestGoroutineLeakDetector_LeakedRoutine(t *testing.T) {
	baseline := diagnostics.CaptureGoroutines()

	stopCh := make(chan struct{})
	started := make(chan struct{})

	// Spawn intentional leak
	go func() {
		close(started)
		<-stopCh
	}()
	<-started

	err := diagnostics.VerifyNoGoroutineLeak(
		baseline,
		diagnostics.WithTolerance(0),
		diagnostics.WithSettleTimeout(30*time.Millisecond),
	)
	if err == nil {
		t.Errorf("expected goroutine leak error, got nil")
	}
	if !errors.Is(err, diagnostics.ErrGoroutineLeak) {
		t.Errorf("expected unwrapped ErrGoroutineLeak, got %v", err)
	}

	// Release leaked goroutine
	close(stopCh)
	time.Sleep(20 * time.Millisecond)

	// Verify leak has cleared
	if clearErr := diagnostics.VerifyNoGoroutineLeak(baseline, diagnostics.WithTolerance(0)); clearErr != nil {
		t.Errorf("expected leak to clear after goroutine release, got %v", clearErr)
	}
}

func mySpecialIgnoredWorkerRoutine(started, stopCh chan struct{}) {
	close(started)
	<-stopCh
}

func TestGoroutineLeakDetector_IgnoredPatterns_And_Tolerance(t *testing.T) {
	baseline := diagnostics.CaptureGoroutines()

	stopCh := make(chan struct{})
	started := make(chan struct{})

	go mySpecialIgnoredWorkerRoutine(started, stopCh)
	<-started
	defer close(stopCh)

	// 1. With ignored pattern matching the routine
	err := diagnostics.VerifyNoGoroutineLeak(
		baseline,
		diagnostics.WithIgnoredPatterns("mySpecialIgnoredWorkerRoutine"),
		diagnostics.WithSettleTimeout(20*time.Millisecond),
	)
	if err != nil {
		t.Errorf("expected leak to be ignored via pattern, got %v", err)
	}

	// 2. With tolerance = 1
	err = diagnostics.VerifyNoGoroutineLeak(
		baseline,
		diagnostics.WithTolerance(1),
		diagnostics.WithSettleTimeout(20*time.Millisecond),
	)
	if err != nil {
		t.Errorf("expected leak to be tolerated via tolerance=1, got %v", err)
	}
}

func TestProfilers_Dumps(t *testing.T) {
	// DumpHeap
	var heapBuf bytes.Buffer
	if err := diagnostics.DumpHeap(&heapBuf); err != nil {
		t.Errorf("DumpHeap failed: %v", err)
	}
	if heapBuf.Len() == 0 {
		t.Errorf("expected non-empty heap profile")
	}

	// DumpGoroutines
	var grBuf bytes.Buffer
	if err := diagnostics.DumpGoroutines(&grBuf, 1); err != nil {
		t.Errorf("DumpGoroutines failed: %v", err)
	}
	if grBuf.Len() == 0 {
		t.Errorf("expected non-empty goroutine profile")
	}

	// DumpCPU
	var cpuBuf bytes.Buffer
	if err := diagnostics.DumpCPU(&cpuBuf, 20*time.Millisecond); err != nil {
		t.Errorf("DumpCPU failed: %v", err)
	}

	// DumpBlock
	var blockBuf bytes.Buffer
	if err := diagnostics.DumpBlock(&blockBuf, 1); err != nil {
		t.Errorf("DumpBlock failed: %v", err)
	}

	// DumpMutex
	var mutexBuf bytes.Buffer
	if err := diagnostics.DumpMutex(&mutexBuf, 1); err != nil {
		t.Errorf("DumpMutex failed: %v", err)
	}

	// Nil writer errors
	if err := diagnostics.DumpHeap(nil); !errors.Is(err, diagnostics.ErrNilWriter) {
		t.Errorf("expected ErrNilWriter on DumpHeap, got %v", err)
	}
	if err := diagnostics.DumpGoroutines(nil, 1); !errors.Is(err, diagnostics.ErrNilWriter) {
		t.Errorf("expected ErrNilWriter on DumpGoroutines, got %v", err)
	}
	if err := diagnostics.DumpCPU(nil, time.Second); !errors.Is(err, diagnostics.ErrNilWriter) {
		t.Errorf("expected ErrNilWriter on DumpCPU, got %v", err)
	}
	if err := diagnostics.DumpBlock(nil, 1); !errors.Is(err, diagnostics.ErrNilWriter) {
		t.Errorf("expected ErrNilWriter on DumpBlock, got %v", err)
	}
	if err := diagnostics.DumpMutex(nil, 1); !errors.Is(err, diagnostics.ErrNilWriter) {
		t.Errorf("expected ErrNilWriter on DumpMutex, got %v", err)
	}
}

func TestProtectedHandler_Authentication_And_Summary(t *testing.T) {
	watchdog := diagnostics.NewMemoryWatchdog(diagnostics.WithMaxHeapAlloc(100 * 1024 * 1024 * 1024))
	handler := diagnostics.NewHandler(
		diagnostics.WithBearerToken("secret-diagnostic-key-99"),
		diagnostics.WithPathPrefix("/admin/debug"),
		diagnostics.WithWatchdog(watchdog),
	)

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := ts.Client()

	// 1. Missing Token -> 401 Unauthorized
	resp1, err := client.Get(ts.URL + "/admin/debug/pprof/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for missing token, got %d", resp1.StatusCode)
	}
	if resp1.Header.Get("WWW-Authenticate") == "" {
		t.Errorf("expected WWW-Authenticate header")
	}

	// 2. Invalid Token -> 401 Unauthorized
	req2, _ := http.NewRequest("GET", ts.URL+"/admin/debug/pprof/", nil)
	req2.Header.Set("Authorization", "Bearer invalid-token")
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for bad token, got %d", resp2.StatusCode)
	}

	// 3. Valid Bearer Token in Header -> 200 OK
	req3, _ := http.NewRequest("GET", ts.URL+"/admin/debug/pprof/heap", nil)
	req3.Header.Set("Authorization", "Bearer secret-diagnostic-key-99")
	resp3, err := client.Do(req3)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for valid bearer token, got %d", resp3.StatusCode)
	}

	// 4. Valid Token in Query Parameter -> 200 OK
	resp4, err := client.Get(ts.URL + "/admin/debug/diagnostics/summary?token=secret-diagnostic-key-99")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp4.Body.Close()
	if resp4.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for query token, got %d", resp4.StatusCode)
	}

	var summaryResp struct {
		Memory         diagnostics.MemorySummary `json:"memory"`
		GoroutineCount int                       `json:"goroutine_count"`
		Watchdog       map[string]any            `json:"watchdog"`
	}
	if err := json.NewDecoder(resp4.Body).Decode(&summaryResp); err != nil {
		t.Fatalf("failed to decode summary JSON: %v", err)
	}
	if summaryResp.Memory.Alloc == 0 || summaryResp.GoroutineCount == 0 {
		t.Errorf("invalid summary content: %+v", summaryResp)
	}
	if summaryResp.Watchdog["breached"] != false {
		t.Errorf("expected watchdog breached=false")
	}
}

func TestProtectedHandler_CIDR_Allowlist(t *testing.T) {
	handler := diagnostics.NewHandler(
		diagnostics.WithAllowedCIDRs("10.0.0.0/8", "127.0.0.1/32"),
	)

	// 1. Forbidden IP (192.168.1.1) -> 403 Forbidden
	rForbidden := httptest.NewRequest("GET", "/debug/pprof/", nil)
	rForbidden.RemoteAddr = "192.168.1.1:45678"
	recForbidden := httptest.NewRecorder()
	handler.ServeHTTP(recForbidden, rForbidden)

	if recForbidden.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for unlisted IP, got %d", recForbidden.Code)
	}

	// 2. Allowed IP (127.0.0.1) -> 200 OK
	rAllowed := httptest.NewRequest("GET", "/debug/pprof/", nil)
	rAllowed.RemoteAddr = "127.0.0.1:45678"
	recAllowed := httptest.NewRecorder()
	handler.ServeHTTP(recAllowed, rAllowed)

	if recAllowed.Code != http.StatusOK {
		t.Errorf("expected 200 OK for allowed IP, got %d", recAllowed.Code)
	}

	// 3. X-Forwarded-For subnet matching (10.1.2.3 in 10.0.0.0/8) -> 200 OK
	rXFF := httptest.NewRequest("GET", "/debug/pprof/", nil)
	rXFF.RemoteAddr = "203.0.113.5:1234"
	rXFF.Header.Set("X-Forwarded-For", "10.1.2.3, 198.51.100.1")
	recXFF := httptest.NewRecorder()
	handler.ServeHTTP(recXFF, rXFF)

	if recXFF.Code != http.StatusOK {
		t.Errorf("expected 200 OK for X-Forwarded-For allowed subnet, got %d", recXFF.Code)
	}
}

func TestGoroutineLeakError_Format(t *testing.T) {
	err := &diagnostics.GoroutineLeakError{
		LeakedCount: 3,
		Details:     "goroutine 42 [running]:\nmain.leak()",
	}
	expected := "diagnostics: detected 3 leaked goroutines:\ngoroutine 42 [running]:\nmain.leak()"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
	if !errors.Is(err, diagnostics.ErrGoroutineLeak) {
		t.Errorf("expected unwrapped ErrGoroutineLeak")
	}
}
