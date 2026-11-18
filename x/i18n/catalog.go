package i18n

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// PluralMessage contains localized strings for different CLDR plural categories.
type PluralMessage struct {
	Zero  string `json:"zero,omitempty" yaml:"zero,omitempty"`
	One   string `json:"one,omitempty" yaml:"one,omitempty"`
	Two   string `json:"two,omitempty" yaml:"two,omitempty"`
	Few   string `json:"few,omitempty" yaml:"few,omitempty"`
	Many  string `json:"many,omitempty" yaml:"many,omitempty"`
	Other string `json:"other" yaml:"other"`
}

// Select returns the string corresponding to the given category, falling back to Other.
func (p PluralMessage) Select(cat PluralCategory) string {
	switch cat {
	case PluralZero:
		if p.Zero != "" {
			return p.Zero
		}
	case PluralOne:
		if p.One != "" {
			return p.One
		}
	case PluralTwo:
		if p.Two != "" {
			return p.Two
		}
	case PluralFew:
		if p.Few != "" {
			return p.Few
		}
	case PluralMany:
		if p.Many != "" {
			return p.Many
		}
	}
	return p.Other
}

// Catalog holds the key-value message strings and plural forms for a single locale.
type Catalog struct {
	mu       sync.RWMutex
	locale   Locale
	messages map[string]any
}

// NewCatalog creates a new empty message catalog for the given locale.
func NewCatalog(loc Locale) *Catalog {
	return &Catalog{
		locale:   loc,
		messages: make(map[string]any),
	}
}

// Locale returns the locale for which this catalog was created.
func (c *Catalog) Locale() Locale {
	return c.locale
}

// Set stores a raw message string or PluralMessage under key.
func (c *Catalog) Set(key string, val any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages[key] = val
}

// Get retrieves the message or PluralMessage stored under key.
func (c *Catalog) Get(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.messages[key]
	return val, ok
}

// Count returns the number of registered keys in the catalog.
func (c *Catalog) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.messages)
}

// LoadJSON parses a JSON document, flattens nested keys, and registers messages.
func (c *Catalog) LoadJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("i18n: invalid JSON for locale %s: %w", c.locale.Tag, err)
	}
	return c.mergeRawMap(raw)
}

// LoadYAML parses a YAML document, flattens nested keys, and registers messages.
func (c *Catalog) LoadYAML(data []byte) error {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("i18n: invalid YAML for locale %s: %w", c.locale.Tag, err)
	}
	return c.mergeRawMap(raw)
}

// mergeRawMap flattens and stores the nested key-value tree.
func (c *Catalog) mergeRawMap(raw map[string]any) error {
	flattened := make(map[string]any)
	flattenTree("", raw, flattened)

	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range flattened {
		c.messages[k] = v
	}
	return nil
}

// flattenTree recursively flattens nested maps into dot-separated keys,
// recognizing plural message dictionaries.
func flattenTree(prefix string, in map[string]any, out map[string]any) {
	// Check if this map itself is a PluralMessage
	if isPluralMap(in) {
		pm := PluralMessage{
			Zero:  toString(in["zero"]),
			One:   toString(in["one"]),
			Two:   toString(in["two"]),
			Few:   toString(in["few"]),
			Many:  toString(in["many"]),
			Other: toString(in["other"]),
		}
		if prefix != "" {
			out[prefix] = pm
		}
		return
	}

	for k, v := range in {
		fullKey := k
		if prefix != "" {
			fullKey = prefix + "." + k
		}

		switch child := v.(type) {
		case map[string]any:
			flattenTree(fullKey, child, out)
		case map[any]any:
			// Handle yaml unmarshaling into map[any]any
			converted := make(map[string]any, len(child))
			for ck, cv := range child {
				converted[fmt.Sprintf("%v", ck)] = cv
			}
			flattenTree(fullKey, converted, out)
		default:
			out[fullKey] = fmt.Sprintf("%v", v)
		}
	}
}

// isPluralMap checks if the map contains standard plural keys ("other", and optionally "one", etc.).
func isPluralMap(m map[string]any) bool {
	if _, hasOther := m["other"]; !hasOther {
		return false
	}
	// Verify that keys are among the plural categories
	allowed := map[string]bool{
		"zero": true, "one": true, "two": true, "few": true, "many": true, "other": true,
	}
	for k := range m {
		if !allowed[k] {
			return false
		}
	}
	return true
}

func toString(val any) string {
	if val == nil {
		return ""
	}
	return fmt.Sprintf("%v", val)
}

// LoadFS scans the filesystem matching pattern (e.g. "*.json" or "*.yaml") and loads catalog entries.
func (c *Catalog) LoadFS(fsys fs.FS, pattern string) error {
	entries, err := fs.Glob(fsys, pattern)
	if err != nil {
		return fmt.Errorf("i18n: glob error: %w", err)
	}

	for _, file := range entries {
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return fmt.Errorf("i18n: read %s: %w", file, err)
		}

		ext := strings.ToLower(filepath.Ext(file))
		switch ext {
		case ".json":
			if err := c.LoadJSON(data); err != nil {
				return err
			}
		case ".yaml", ".yml":
			if err := c.LoadYAML(data); err != nil {
				return err
			}
		default:
			return fmt.Errorf("i18n: unsupported file format %q for %s", ext, file)
		}
	}

	return nil
}
