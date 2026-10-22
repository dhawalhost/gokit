package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

// FileProvider reads and watches local configuration files, supporting JSON, YAML,
// and atomic symlink swaps common in Kubernetes ConfigMaps.
type FileProvider struct {
	path     string
	debounce time.Duration

	mu       sync.Mutex
	lastData map[string]any
}

// NewFileProvider constructs a FileProvider for a given file path.
func NewFileProvider(path string, opts ...FileOption) *FileProvider {
	fp := &FileProvider{
		path:     path,
		debounce: 50 * time.Millisecond,
	}
	for _, opt := range opts {
		opt(fp)
	}
	return fp
}

// FileOption configures a FileProvider.
type FileOption func(*FileProvider)

// WithDebounce sets the debounce quiet window before triggering a reload after file events.
func WithDebounce(d time.Duration) FileOption {
	return func(fp *FileProvider) {
		if d > 0 {
			fp.debounce = d
		}
	}
}

// Name returns the provider identifier.
func (f *FileProvider) Name() string {
	return "file"
}

// Fetch reads and parses the configuration file.
func (f *FileProvider) Fetch(ctx context.Context) (map[string]any, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return nil, fmt.Errorf("watcher: read config file: %w", err)
	}

	parsed, err := parseConfigData(f.path, data)
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	f.lastData = parsed
	f.mu.Unlock()

	return parsed, nil
}

// Watch starts an fsnotify directory watcher to detect updates, file rewrites,
// and Kubernetes atomic symlink swaps (..data symlink rotation).
func (f *FileProvider) Watch(ctx context.Context, onChange func(values map[string]any, err error)) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watcher: create fsnotify watcher: %w", err)
	}

	dir := filepath.Dir(f.path)
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}

	if err := watcher.Add(absDir); err != nil {
		_ = watcher.Close()
		return fmt.Errorf("watcher: add watch on dir %s: %w", absDir, err)
	}
	if realDir, err := filepath.EvalSymlinks(absDir); err == nil && realDir != absDir {
		_ = watcher.Add(realDir)
	}
	_ = watcher.Add(f.path)
	if realFile, err := filepath.EvalSymlinks(f.path); err == nil && realFile != f.path {
		_ = watcher.Add(realFile)
	}

	go func() {
		defer func() {
			_ = watcher.Close()
		}()

		var (
			debounceTimer *time.Timer
			timerActive   bool
			timerMu       sync.Mutex
		)

		triggerReload := func() {
			data, err := f.Fetch(ctx)
			onChange(data, err)
		}

		targetBase := filepath.Base(f.path)

		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}

				// Check if event affects target file or Kubernetes ..data symlink
				evBase := filepath.Base(event.Name)
				if evBase != targetBase && evBase != "..data" && !strings.HasPrefix(evBase, "..") {
					continue
				}

				// Debounce to collapse rapid editor saves or atomic multi-step renames
				timerMu.Lock()
				if timerActive {
					debounceTimer.Stop()
				}
				debounceTimer = time.AfterFunc(f.debounce, func() {
					timerMu.Lock()
					timerActive = false
					timerMu.Unlock()
					triggerReload()
				})
				timerActive = true
				timerMu.Unlock()

			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}

			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}

func parseConfigData(path string, data []byte) (map[string]any, error) {
	ext := strings.ToLower(filepath.Ext(path))
	res := make(map[string]any)

	switch ext {
	case ".json":
		if err := json.Unmarshal(data, &res); err != nil {
			return nil, fmt.Errorf("watcher: parse json: %w", err)
		}
	default:
		// Default to YAML (YAML parser also parses JSON natively)
		if err := yaml.Unmarshal(data, &res); err != nil {
			return nil, fmt.Errorf("watcher: parse yaml: %w", err)
		}
	}
	return res, nil
}
