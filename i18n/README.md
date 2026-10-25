# i18n

`i18n` provides an enterprise-ready internationalization, localization, and pluralization subsystem for Go microservices and web APIs. It supports standard BCP 47 language tags, hierarchy fallback chains (e.g. `fr-CA` -> `fr` -> `en`), CLDR plural rules, parameter interpolation, JSON/YAML message dictionaries, and HTTP content negotiation (`Accept-Language`, query parameters, cookies).

---

## Architecture Overview

```
                        HTTP / RPC Request
                               │
               ┌───────────────┴───────────────┐
               ▼                               ▼
       Query Parameter (?lang=es)       Cookie (locale=es)
               │                               │
               └───────────────┬───────────────┘
                               │
                               ▼
                    Accept-Language Header
               ("fr-CH, fr;q=0.9, en;q=0.8")
                               │
                               ▼
               ┌───────────────────────────────┐
               │    i18n.ParseAcceptLanguage   │
               │   (Sorted by Quality Weight)  │
               └───────────────┬───────────────┘
                               │
                               ▼
               ┌───────────────────────────────┐
               │     i18n.Bundle.NewLocalizer  │
               │  Candidates: [fr-CH, fr, en]  │
               └───────────────┬───────────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │   Request Context   │ ──> i18n.T(ctx, "auth.login")
                    │ (WithLocalizer(ctx))│ ──> i18n.Plural(ctx, "cart.items", 3)
                    └──────────┬──────────┘
                               │
                               ▼
                     Content-Language: fr
```

---

## Features

- **BCP 47 Language Tag Parsing**: Handles tags with language, script, and region codes (e.g., `en-US`, `pt-BR`, `zh-Hans-CN`, `es-419`), with case and separator normalization.
- **Hierarchy Fallback Chains**: Seamless resolution fallback from specific regional dialects to parent languages and global defaults (`zh-Hans-CN` -> `zh-Hans` -> `zh` -> `en`).
- **CLDR Pluralization Engine**: Built-in plural category selection (`zero`, `one`, `two`, `few`, `many`, `other`) covering English, French, Spanish, Russian, Polish, Arabic, and Asian non-inflecting languages.
- **Flexible Dictionary Loading**:
  - `LoadJSON` and `LoadYAML` with automatic nested map flattening (`errors.user.not_found`).
  - `LoadFS` to auto-discover locale files from embedded filesystems (`embed.FS`) or directories.
- **Smart Parameter Interpolation**: Replaces `{name}` or `{count}` placeholders from `map[string]any`, `map[string]string`, or struct fields via reflection.
- **HTTP Content Negotiation**: Drop-in middleware with precedence order:
  1. Query parameter (`?lang=fr` or custom name)
  2. Cookie (`locale=fr` or custom name)
  3. `Accept-Language` header (parsed with RFC 9110 quality weights)
  4. Bundle default fallback locale.
  - Automatically writes the `Content-Language` response header.
- **Context Integration**: First-class context helpers `i18n.T(ctx, ...)` and `i18n.Plural(ctx, ...)`.

---

## Installation

```bash
go get github.com/dhawalhost/gokit/i18n
```

---

## Quickstart

### 1. Initializing Bundle with Message Dictionaries

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/dhawalhost/gokit/i18n"
)

func main() {
	bundle := i18n.NewBundle(i18n.WithDefaultLocale("en"))

	// 1. Load English JSON dictionary
	err := bundle.LoadJSON("en", []byte(`{
		"welcome": "Welcome, {name}!",
		"notifications": {
			"one": "You have 1 unread notification",
			"other": "You have {count} unread notifications"
		}
	}`))
	if err != nil {
		log.Fatal(err)
	}

	// 2. Load Spanish YAML dictionary
	err = bundle.LoadYAML("es", []byte(`
welcome: "¡Bienvenido, {name}!"
notifications:
  one: "Tienes 1 notificación sin leer"
  other: "Tienes {count} notificaciones sin leer"
`))
	if err != nil {
		log.Fatal(err)
	}

	// 3. Localize for a Spanish user
	localizer := bundle.NewLocalizer("es-ES", "es")
	greeting := localizer.T("welcome", map[string]any{"name": "Carlos"})
	alerts := localizer.Plural("notifications", 5)

	fmt.Println(greeting) // ¡Bienvenido, Carlos!
	fmt.Println(alerts)   // Tienes 5 notificaciones sin leer
}
```

---

### 2. Loading from Embedded Filesystem (`embed.FS`)

Store locale files in `locales/en.json`, `locales/es.yaml`, `locales/fr.json`:

```go
//go:embed locales/*
var localeFS embed.FS

func initBundle() *i18n.Bundle {
	bundle := i18n.NewBundle(i18n.WithDefaultLocale("en"))
	_ = bundle.LoadFS(localeFS, "locales/*.json")
	_ = bundle.LoadFS(localeFS, "locales/*.yaml")
	return bundle
}
```

---

### 3. HTTP Middleware & Context Localization

```go
package main

import (
	"net/http"

	"github.com/dhawalhost/gokit/i18n"
	"github.com/go-chi/chi/v5"
)

func main() {
	bundle := i18n.NewBundle(i18n.WithDefaultLocale("en"))
	// ... load dictionaries ...

	r := chi.NewRouter()

	// Attach i18n middleware
	r.Use(i18n.Middleware(bundle,
		i18n.WithQueryParam("lang"),              // allows ?lang=es
		i18n.WithCookieName("locale"),            // allows cookie "locale=fr"
		i18n.WithContentLanguageHeader(true),     // writes Content-Language response header
	))

	r.Get("/profile", func(w http.ResponseWriter, r *http.Request) {
		// Use package-level context helper
		msg := i18n.T(r.Context(), "welcome", map[string]any{"name": "Alice"})
		w.Write([]byte(msg))
	})

	http.ListenAndServe(":8080", r)
}
```

---

### 4. Plural Categories

`i18n` maps plural rules according to the CLDR specification:

```yaml
# locales/en.yaml
items:
  one: "1 item"
  other: "{count} items"

# locales/ar.yaml (Arabic 6-category plural forms)
items:
  zero: "لا توجد عناصر"
  one: "عنصر واحد"
  two: "عنصران"
  few: "{count} عناصر"
  many: "{count} عنصرًا"
  other: "{count} عنصر"
```
