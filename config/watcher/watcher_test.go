package watcher_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/dhawalhost/gokit/config/watcher"
)

func TestMemoryProvider(t *testing.T) {
	mp := watcher.NewMemoryProvider(map[string]any{
		"env":  "production",
		"rate": 100,
	})

	if mp.Name() != "memory" {
		t.Errorf("expected name memory, got %s", mp.Name())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	data, err := mp.Fetch(ctx)
	if err != nil || data["env"] != "production" {
		t.Fatalf("fetch failed: %v", err)
	}

	var notified bool
	var updateData map[string]any
	err = mp.Watch(ctx, func(values map[string]any, err error) {
		notified = true
		updateData = values
	})
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	mp.Set("rate", 200)
	if !notified || updateData["rate"] != 200 {
		t.Errorf("expected update notification on Set")
	}

	notified = false
	mp.SetAll(map[string]any{"env": "staging"})
	if !notified || updateData["env"] != "staging" || updateData["rate"] != nil {
		t.Errorf("expected update notification on SetAll")
	}

	notified = false
	mp.Delete("env")
	if !notified || updateData["env"] != nil {
		t.Errorf("expected update notification on Delete")
	}
}

func TestPollingProvider(t *testing.T) {
	var count int
	pp := watcher.NewPollingProvider("vault", 10*time.Millisecond, func(ctx context.Context) (map[string]any, error) {
		count++
		if count == 2 {
			return nil, errors.New("temporary network timeout")
		}
		if count >= 3 {
			return map[string]any{"secret": "v2"}, nil
		}
		return map[string]any{"secret": "v1"}, nil
	})

	if pp.Name() != "vault" {
		t.Errorf("expected name vault, got %s", pp.Name())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	data, err := pp.Fetch(ctx)
	if err != nil || data["secret"] != "v1" {
		t.Fatalf("initial fetch failed: %v", err)
	}

	var (
		pollMu      sync.Mutex
		updates     []map[string]any
		errReceived bool
	)

	err = pp.Watch(ctx, func(values map[string]any, err error) {
		pollMu.Lock()
		defer pollMu.Unlock()
		if err != nil {
			errReceived = true
			return
		}
		updates = append(updates, values)
	})
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	// Wait for polling cycles (count 1 -> 2 (err) -> 3 (v2))
	time.Sleep(45 * time.Millisecond)

	pollMu.Lock()
	hasErr := errReceived
	numUpdates := len(updates)
	pollMu.Unlock()

	if !hasErr {
		t.Errorf("expected error notification from polling provider")
	}
	if numUpdates == 0 {
		t.Errorf("expected at least one update")
	}
}

func TestFileProvider_YAMLAndJSON(t *testing.T) {
	tempDir := t.TempDir()

	// 1. YAML file test
	yamlPath := filepath.Join(tempDir, "config.yaml")
	yamlContent := []byte("app:\n  name: gokit-app\n  port: 8080\n")
	if err := os.WriteFile(yamlPath, yamlContent, 0600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	fp := watcher.NewFileProvider(yamlPath, watcher.WithDebounce(10*time.Millisecond))
	if fp.Name() != "file" {
		t.Errorf("expected file provider name, got %s", fp.Name())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	data, err := fp.Fetch(ctx)
	if err != nil {
		t.Fatalf("fetch yaml: %v", err)
	}
	appMap, ok := data["app"].(map[string]any)
	if !ok || appMap["name"] != "gokit-app" {
		t.Errorf("unexpected yaml content: %v", data)
	}

	// 2. Watch and reload on file rewrite
	reloadedCh := make(chan map[string]any, 1)
	err = fp.Watch(ctx, func(values map[string]any, err error) {
		if err == nil {
			select {
			case reloadedCh <- values:
			default:
			}
		}
	})
	if err != nil {
		t.Fatalf("watch file: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	_ = os.WriteFile(yamlPath, []byte("app:\n  name: updated-app\n"), 0600)

	var reloadedData map[string]any
	select {
	case reloadedData = <-reloadedCh:
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for file reload event")
	}

	if reloadedData != nil {
		newApp, _ := reloadedData["app"].(map[string]any)
		if newApp != nil && newApp["name"] != "updated-app" {
			t.Errorf("unexpected new app name: %v", newApp)
		}
	}

	// 3. JSON file test
	jsonPath := filepath.Join(tempDir, "config.json")
	if err := os.WriteFile(jsonPath, []byte(`{"version": "1.0.0"}`), 0600); err != nil {
		t.Fatalf("write json: %v", err)
	}
	jsonFp := watcher.NewFileProvider(jsonPath)
	jsonData, err := jsonFp.Fetch(ctx)
	if err != nil || jsonData["version"] != "1.0.0" {
		t.Fatalf("fetch json failed: %v", err)
	}
}

func TestWatcher_CoreAndTypeGetters(t *testing.T) {
	mp := watcher.NewMemoryProvider(map[string]any{
		"server": map[string]any{
			"port":    8080,
			"host":    "0.0.0.0",
			"timeout": "15s",
			"rate":    12.5,
			"enabled": true,
		},
		"version": "v1.0",
		"debug":   "true",
	})

	w := watcher.New(mp)
	w = watcher.New(mp) // verify options
	_ = watcher.WithLogger(zap.NewNop())

	ctx := context.Background()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("start watcher: %v", err)
	}
	defer w.Stop()

	// Snapshot
	snap := w.Snapshot()
	if snap["version"] != "v1.0" {
		t.Errorf("expected version v1.0, got %v", snap["version"])
	}

	// Get & GetString
	if w.GetString("version") != "v1.0" {
		t.Errorf("expected version v1.0, got %s", w.GetString("version"))
	}
	if w.GetString("missing", "default-str") != "default-str" {
		t.Errorf("expected default-str")
	}

	// Nested lookup
	if w.GetString("server.host") != "0.0.0.0" {
		t.Errorf("expected 0.0.0.0 for server.host, got %s", w.GetString("server.host"))
	}

	// GetInt
	if w.GetInt("server.port") != 8080 {
		t.Errorf("expected 8080, got %d", w.GetInt("server.port"))
	}
	if w.GetInt("missing.int", 42) != 42 {
		t.Errorf("expected 42")
	}

	// GetFloat64
	if w.GetFloat64("server.rate") != 12.5 {
		t.Errorf("expected 12.5, got %f", w.GetFloat64("server.rate"))
	}
	if w.GetFloat64("missing.float", 3.14) != 3.14 {
		t.Errorf("expected 3.14")
	}

	// GetBool
	if !w.GetBool("server.enabled") {
		t.Errorf("expected server.enabled true")
	}
	if !w.GetBool("debug") { // parsed from "true" string
		t.Errorf("expected debug parsed from string to true")
	}
	if w.GetBool("missing.bool", true) != true {
		t.Errorf("expected default true")
	}

	// GetDuration
	if w.GetDuration("server.timeout") != 15*time.Second {
		t.Errorf("expected 15s duration, got %v", w.GetDuration("server.timeout"))
	}
	if w.GetDuration("missing.dur", time.Minute) != time.Minute {
		t.Errorf("expected default time.Minute")
	}

	// Struct Unmarshal
	type ServerConfig struct {
		Server struct {
			Port int    `json:"port"`
			Host string `json:"host"`
		} `json:"server"`
		Version string `json:"version"`
	}

	var sc ServerConfig
	if err := w.Unmarshal(&sc); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if sc.Server.Port != 8080 || sc.Version != "v1.0" {
		t.Errorf("unexpected unmarshal result: %+v", sc)
	}

	if err := w.Unmarshal(nil); err == nil {
		t.Errorf("expected error on unmarshal nil")
	}
}

func TestWatcher_SubscriptionsAndResilience(t *testing.T) {
	mp := watcher.NewMemoryProvider(map[string]any{
		"feature_x": false,
		"threshold": 10,
	})

	w := watcher.New(mp)

	var reloaded bool
	var keyChanged bool
	var oldV, newV any
	var errCaptured error

	w.OnReload(func(snapshot map[string]any) {
		reloaded = true
	})

	w.OnKeyChange("feature_x", func(oldVal, newVal any) {
		keyChanged = true
		oldV = oldVal
		newV = newVal
	})

	w.OnError(func(err error) {
		errCaptured = err
	})

	ctx := context.Background()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer w.Stop()

	// Update key
	mp.Set("feature_x", true)

	time.Sleep(10 * time.Millisecond)

	if !reloaded {
		t.Errorf("expected OnReload callback")
	}
	if !keyChanged || oldV != false || newV != true {
		t.Errorf("expected OnKeyChange callback with false -> true, got %v -> %v", oldV, newV)
	}

	// Simulate provider error (Last Known Good remains intact)
	snapBefore := w.Snapshot()
	var faultyCount int
	badProvider := watcher.NewPollingProvider("faulty", 5*time.Millisecond, func(ctx context.Context) (map[string]any, error) {
		faultyCount++
		if faultyCount == 1 {
			return map[string]any{"healthy": true}, nil
		}
		return nil, errors.New("upstream connection reset")
	})

	errCh := make(chan error, 1)
	wFaulty := watcher.New(mp, badProvider)
	wFaulty.OnError(func(err error) {
		select {
		case errCh <- err:
		default:
		}
	})
	if err := wFaulty.Start(context.Background()); err != nil {
		t.Fatalf("start wFaulty failed: %v", err)
	}
	defer wFaulty.Stop()

	select {
	case errCaptured = <-errCh:
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for OnError callback")
	}

	if errCaptured == nil {
		t.Errorf("expected OnError callback to be invoked")
	}
	// Verify LKG snapshot is retained
	if wFaulty.GetBool("feature_x") != snapBefore["feature_x"] {
		t.Errorf("expected LKG snapshot to be preserved during errors")
	}

	// Idempotent Stop
	if err := w.Stop(); err != nil {
		t.Errorf("stop error: %v", err)
	}
}

func TestWatcher_TypeConversionsAndErrors(t *testing.T) {
	mp := watcher.NewMemoryProvider(map[string]any{
		"int_from_int64":   int64(42),
		"int_from_float":   float64(99.0),
		"int_from_string":  "123",
		"int_bad_string":   "abc",
		"float_from_int":   int(10),
		"float_from_int64": int64(20),
		"float_from_str":   "3.1415",
		"float_bad_str":    "xyz",
		"bool_false_str":   "false",
		"bool_bad_str":     "not-a-bool",
		"dur_from_int":     int(5000),     // 5s
		"dur_from_float":   float64(2500), // 2.5s
		"dur_from_typed":   10 * time.Second,
		"dur_bad_str":      "invalid-duration",
		"nested": map[string]any{
			"deep": map[string]any{
				"val": "found",
			},
		},
	})

	w := watcher.New(mp).WithOptions(
		watcher.WithLogger(zap.NewNop()),
		watcher.WithLogger(nil),
	)

	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer w.Stop()

	// Int conversions
	if w.GetInt("int_from_int64") != 42 {
		t.Errorf("expected 42 from int64")
	}
	if w.GetInt("int_from_float") != 99 {
		t.Errorf("expected 99 from float64")
	}
	if w.GetInt("int_from_string") != 123 {
		t.Errorf("expected 123 from string")
	}
	if w.GetInt("int_bad_string", 7) != 7 {
		t.Errorf("expected default 7 for bad string int")
	}

	// Float conversions
	if w.GetFloat64("float_from_int") != 10.0 {
		t.Errorf("expected 10.0 from int")
	}
	if w.GetFloat64("float_from_int64") != 20.0 {
		t.Errorf("expected 20.0 from int64")
	}
	if w.GetFloat64("float_from_str") != 3.1415 {
		t.Errorf("expected 3.1415 from str")
	}
	if w.GetFloat64("float_bad_str", 1.23) != 1.23 {
		t.Errorf("expected default 1.23 for bad float str")
	}

	// Bool conversions
	if w.GetBool("bool_false_str", true) != false {
		t.Errorf("expected false for false string")
	}
	if w.GetBool("bool_bad_str", true) != true {
		t.Errorf("expected default true for bad bool str")
	}

	// Duration conversions
	if w.GetDuration("dur_from_int") != 5*time.Second {
		t.Errorf("expected 5s from ms int, got %v", w.GetDuration("dur_from_int"))
	}
	if w.GetDuration("dur_from_float") != 2500*time.Millisecond {
		t.Errorf("expected 2.5s from ms float, got %v", w.GetDuration("dur_from_float"))
	}
	if w.GetDuration("dur_from_typed") != 10*time.Second {
		t.Errorf("expected 10s from time.Duration, got %v", w.GetDuration("dur_from_typed"))
	}
	if w.GetDuration("dur_bad_str", 3*time.Second) != 3*time.Second {
		t.Errorf("expected default 3s for bad duration")
	}

	// Deep nested
	if w.GetString("nested.deep.val") != "found" {
		t.Errorf("expected 'found' for nested.deep.val, got %s", w.GetString("nested.deep.val"))
	}
	if w.GetString("nested.not_a_map.child") != "" {
		t.Errorf("expected empty string for invalid path segment")
	}

	// FileProvider error paths
	fpBadPath := watcher.NewFileProvider("/non/existent/path/config.yaml", watcher.WithDebounce(0))
	_, err := fpBadPath.Fetch(context.Background())
	if err == nil {
		t.Errorf("expected error fetching non-existent file")
	}

	tempDir := t.TempDir()
	badJSON := filepath.Join(tempDir, "bad.json")
	_ = os.WriteFile(badJSON, []byte("{invalid json"), 0600)
	fpBadJSON := watcher.NewFileProvider(badJSON)
	_, err = fpBadJSON.Fetch(context.Background())
	if err == nil {
		t.Errorf("expected error parsing invalid json")
	}

	badYAML := filepath.Join(tempDir, "bad.yaml")
	_ = os.WriteFile(badYAML, []byte("key: [unclosed list"), 0600)
	fpBadYAML := watcher.NewFileProvider(badYAML)
	_, err = fpBadYAML.Fetch(context.Background())
	if err == nil {
		t.Errorf("expected error parsing invalid yaml")
	}

	// PollingProvider default interval
	ppDefault := watcher.NewPollingProvider("p", 0, func(ctx context.Context) (map[string]any, error) {
		return map[string]any{"k": "v"}, nil
	})
	if ppDefault.Name() != "p" {
		t.Errorf("expected name p")
	}
}
