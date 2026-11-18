// Package i18n provides internationalization, localization, pluralization, and
// Accept-Language resolution for Go microservices and HTTP APIs.
package i18n

import (
	"sort"
	"strconv"
	"strings"
)

// Locale represents a parsed, normalized BCP 47 language tag (e.g., "en-US", "fr-CA", "zh-Hans").
type Locale struct {
	Tag      string // Full normalized tag (e.g., "en-US", "pt-BR")
	Language string // Primary language subtag (e.g., "en", "pt")
	Region   string // Region subtag if present (e.g., "US", "BR")
	Script   string // Script subtag if present (e.g., "Hans", "Latn")
}

// String returns the canonical string tag.
func (l Locale) String() string {
	return l.Tag
}

// IsZero returns true if the locale is uninitialized.
func (l Locale) IsZero() bool {
	return l.Tag == ""
}

// ParseLocale parses and canonicalizes a BCP 47 language tag or ISO code.
// Handles separators like '-' and '_', case normalization, and subtag extraction.
func ParseLocale(tag string) Locale {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return Locale{}
	}

	// Normalize separators
	normalized := strings.ReplaceAll(tag, "_", "-")
	parts := strings.Split(normalized, "-")

	loc := Locale{
		Language: strings.ToLower(parts[0]),
	}

	for i := 1; i < len(parts); i++ {
		p := parts[i]
		if len(p) == 4 && isAlpha(p) {
			// TitleCase script (e.g. Hans, Hant, Latn)
			loc.Script = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		} else if (len(p) == 2 && isAlpha(p)) || (len(p) == 3 && isDigit(p)) {
			// Uppercase region (e.g. US, GB, 419)
			loc.Region = strings.ToUpper(p)
		}
	}

	// Build normalized tag
	var b strings.Builder
	b.WriteString(loc.Language)
	if loc.Script != "" {
		b.WriteString("-")
		b.WriteString(loc.Script)
	}
	if loc.Region != "" {
		b.WriteString("-")
		b.WriteString(loc.Region)
	}
	loc.Tag = b.String()

	return loc
}

// FallbackChain returns the hierarchy of candidate locales to search.
// For example, "zh-Hans-CN" -> ["zh-Hans-CN", "zh-Hans", "zh"].
func (l Locale) FallbackChain() []string {
	if l.IsZero() {
		return nil
	}

	chain := []string{l.Tag}

	if l.Script != "" && l.Region != "" {
		chain = append(chain, l.Language+"-"+l.Script)
	}
	if l.Region != "" && l.Script == "" {
		chain = append(chain, l.Language)
	} else if l.Script != "" && l.Region != "" {
		chain = append(chain, l.Language)
	}

	return chain
}

// acceptLangItem represents an Accept-Language tag with its quality value.
type acceptLangItem struct {
	tag string
	q   float64
}

// ParseAcceptLanguage parses an RFC 9110 Accept-Language header value,
// sorting tags by quality factor (q-value) in descending order.
// E.g. "fr-CH, fr;q=0.9, en;q=0.8, *;q=0.5".
func ParseAcceptLanguage(header string) []string {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil
	}

	var items []acceptLangItem

	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		params := strings.Split(part, ";")
		tag := strings.TrimSpace(params[0])
		if tag == "*" || tag == "" {
			continue
		}

		q := 1.0
		for _, param := range params[1:] {
			param = strings.TrimSpace(param)
			if strings.HasPrefix(param, "q=") {
				valStr := strings.TrimSpace(param[2:])
				if val, err := strconv.ParseFloat(valStr, 64); err == nil {
					q = val
				}
			}
		}

		items = append(items, acceptLangItem{tag: tag, q: q})
	}

	// Stable sort by q descending
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].q > items[j].q
	})

	results := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))

	for _, item := range items {
		loc := ParseLocale(item.tag)
		if !loc.IsZero() {
			if _, ok := seen[loc.Tag]; !ok {
				seen[loc.Tag] = struct{}{}
				results = append(results, loc.Tag)
			}
		}
	}

	return results
}

func isAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return len(s) > 0
}

func isDigit(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}
