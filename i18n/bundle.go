package i18n

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
)

// Bundle coordinates multiple language catalogs with default fallback resolution.
type Bundle struct {
	mu            sync.RWMutex
	defaultLocale Locale
	catalogs      map[string]*Catalog
}

// BundleOption configures Bundle initialization.
type BundleOption func(*Bundle)

// WithDefaultLocale sets the fallback default locale (e.g. "en" or "en-US").
func WithDefaultLocale(tag string) BundleOption {
	return func(b *Bundle) {
		b.defaultLocale = ParseLocale(tag)
	}
}

// NewBundle creates a new message bundle configured with options.
func NewBundle(opts ...BundleOption) *Bundle {
	b := &Bundle{
		defaultLocale: ParseLocale("en"),
		catalogs:      make(map[string]*Catalog),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// DefaultLocale returns the bundle's fallback locale.
func (b *Bundle) DefaultLocale() Locale {
	return b.defaultLocale
}

// AddCatalog registers a pre-populated Catalog.
func (b *Bundle) AddCatalog(cat *Catalog) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.catalogs[cat.Locale().Tag] = cat
}

// GetCatalog retrieves the catalog for a given locale tag, or returns false.
func (b *Bundle) GetCatalog(tag string) (*Catalog, bool) {
	loc := ParseLocale(tag)
	b.mu.RLock()
	defer b.mu.RUnlock()
	cat, ok := b.catalogs[loc.Tag]
	return cat, ok
}

// EnsureCatalog returns the existing catalog for loc or creates a new one.
func (b *Bundle) EnsureCatalog(loc Locale) *Catalog {
	b.mu.Lock()
	defer b.mu.Unlock()

	cat, exists := b.catalogs[loc.Tag]
	if !exists {
		cat = NewCatalog(loc)
		b.catalogs[loc.Tag] = cat
	}
	return cat
}

// LoadJSON loads a JSON message dictionary directly into the specified locale catalog.
func (b *Bundle) LoadJSON(localeTag string, data []byte) error {
	loc := ParseLocale(localeTag)
	cat := b.EnsureCatalog(loc)
	return cat.LoadJSON(data)
}

// LoadYAML loads a YAML message dictionary directly into the specified locale catalog.
func (b *Bundle) LoadYAML(localeTag string, data []byte) error {
	loc := ParseLocale(localeTag)
	cat := b.EnsureCatalog(loc)
	return cat.LoadYAML(data)
}

// LoadFS scans fsys for locale-named files (e.g. "en.json", "fr-CA.yaml", "locales/es.yml")
// and automatically registers their catalogs into the bundle.
func (b *Bundle) LoadFS(fsys fs.FS, pattern string) error {
	entries, err := fs.Glob(fsys, pattern)
	if err != nil {
		return fmt.Errorf("i18n: glob %q: %w", pattern, err)
	}

	for _, file := range entries {
		base := filepath.Base(file)
		ext := filepath.Ext(base)
		tag := strings.TrimSuffix(base, ext)

		loc := ParseLocale(tag)
		if loc.IsZero() {
			continue
		}

		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return fmt.Errorf("i18n: read %s: %w", file, err)
		}

		switch strings.ToLower(ext) {
		case ".json":
			if err := b.LoadJSON(loc.Tag, data); err != nil {
				return err
			}
		case ".yaml", ".yml":
			if err := b.LoadYAML(loc.Tag, data); err != nil {
				return err
			}
		}
	}

	return nil
}

// NewLocalizer constructs a Localizer for the given requested locales,
// trying each in sequence and falling back to the bundle default.
func (b *Bundle) NewLocalizer(requestedLocales ...string) *Localizer {
	var candidates []Locale

	for _, req := range requestedLocales {
		loc := ParseLocale(req)
		if !loc.IsZero() {
			candidates = append(candidates, loc)
		}
	}

	// Append bundle default locale if not present
	candidates = append(candidates, b.defaultLocale)

	return &Localizer{
		bundle:     b,
		candidates: candidates,
	}
}

// Localizer executes localized message lookups and interpolation for a set of candidate locales.
type Localizer struct {
	bundle     *Bundle
	candidates []Locale
}

// Locale returns the primary active candidate locale.
func (l *Localizer) Locale() Locale {
	if len(l.candidates) > 0 {
		return l.candidates[0]
	}
	return l.bundle.defaultLocale
}

// T translates a key, performing parameter interpolation if arguments are provided.
// Returns the key itself if no translation is found across the fallback chain.
func (l *Localizer) T(key string, args ...any) string {
	raw, ok := l.lookup(key)
	if !ok {
		return key
	}

	switch v := raw.(type) {
	case string:
		return interpolate(v, args...)
	case PluralMessage:
		return interpolate(v.Other, args...)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// Plural selects the correct plural form based on count, then interpolates parameters.
func (l *Localizer) Plural(key string, count int, args ...any) string {
	raw, ok := l.lookup(key)
	if !ok {
		return key
	}

	// Inject count into interpolation args if not provided
	allArgs := append([]any{map[string]any{"count": count}}, args...)

	switch v := raw.(type) {
	case PluralMessage:
		cat := SelectPlural(l.Locale().Language, count)
		msg := v.Select(cat)
		return interpolate(msg, allArgs...)
	case string:
		return interpolate(v, allArgs...)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// lookup searches across candidate locales and their parent fallbacks.
func (l *Localizer) lookup(key string) (any, bool) {
	l.bundle.mu.RLock()
	defer l.bundle.mu.RUnlock()

	for _, cand := range l.candidates {
		for _, fallbackTag := range cand.FallbackChain() {
			if cat, ok := l.bundle.catalogs[fallbackTag]; ok {
				if val, found := cat.Get(key); found {
					return val, true
				}
			}
		}
	}

	return nil, false
}

// interpolate replaces {name} placeholders using maps, structs, or positional arguments.
func interpolate(msg string, args ...any) string {
	if len(args) == 0 || !strings.Contains(msg, "{") {
		return msg
	}

	for _, arg := range args {
		if arg == nil {
			continue
		}

		// Map replacement
		if m, ok := arg.(map[string]any); ok {
			for k, v := range m {
				msg = strings.ReplaceAll(msg, "{"+k+"}", fmt.Sprintf("%v", v))
			}
			continue
		}
		if m, ok := arg.(map[string]string); ok {
			for k, v := range m {
				msg = strings.ReplaceAll(msg, "{"+k+"}", v)
			}
			continue
		}

		// Struct replacement via reflection
		v := reflect.ValueOf(arg)
		if v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		if v.Kind() == reflect.Struct {
			t := v.Type()
			for i := 0; i < v.NumField(); i++ {
				fieldName := t.Field(i).Name
				fieldVal := fmt.Sprintf("%v", v.Field(i).Interface())
				msg = strings.ReplaceAll(msg, "{"+fieldName+"}", fieldVal)
				msg = strings.ReplaceAll(msg, "{"+strings.ToLower(fieldName)+"}", fieldVal)
			}
		}
	}

	return msg
}
