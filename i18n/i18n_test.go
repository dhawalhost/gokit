package i18n_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/dhawalhost/gokit/i18n"
)

func TestLocale_Parse(t *testing.T) {
	tests := []struct {
		input      string
		wantTag    string
		wantLang   string
		wantRegion string
		wantScript string
		wantChain  []string
		wantZero   bool
	}{
		{
			input:     "en",
			wantTag:   "en",
			wantLang:  "en",
			wantChain: []string{"en"},
		},
		{
			input:      "en-US",
			wantTag:    "en-US",
			wantLang:   "en",
			wantRegion: "US",
			wantChain:  []string{"en-US", "en"},
		},
		{
			input:      "pt_BR",
			wantTag:    "pt-BR",
			wantLang:   "pt",
			wantRegion: "BR",
			wantChain:  []string{"pt-BR", "pt"},
		},
		{
			input:      "zh-Hans-CN",
			wantTag:    "zh-Hans-CN",
			wantLang:   "zh",
			wantScript: "Hans",
			wantRegion: "CN",
			wantChain:  []string{"zh-Hans-CN", "zh-Hans", "zh"},
		},
		{
			input:    "",
			wantZero: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			loc := i18n.ParseLocale(tc.input)
			if tc.wantZero {
				if !loc.IsZero() {
					t.Errorf("expected zero locale for %q", tc.input)
				}
				if loc.FallbackChain() != nil {
					t.Errorf("expected nil fallback chain for zero locale")
				}
				return
			}

			if loc.Tag != tc.wantTag {
				t.Errorf("expected tag %q, got %q", tc.wantTag, loc.Tag)
			}
			if loc.Language != tc.wantLang {
				t.Errorf("expected language %q, got %q", tc.wantLang, loc.Language)
			}
			if loc.Region != tc.wantRegion {
				t.Errorf("expected region %q, got %q", tc.wantRegion, loc.Region)
			}
			if loc.Script != tc.wantScript {
				t.Errorf("expected script %q, got %q", tc.wantScript, loc.Script)
			}

			chain := loc.FallbackChain()
			if len(chain) != len(tc.wantChain) {
				t.Fatalf("expected fallback chain len %d, got %d: %v", len(tc.wantChain), len(chain), chain)
			}
			for i, v := range tc.wantChain {
				if chain[i] != v {
					t.Errorf("chain[%d] expected %q, got %q", i, v, chain[i])
				}
			}
		})
	}
}

func TestLocale_AcceptLanguage(t *testing.T) {
	header := "fr-CH, fr;q=0.9, en;q=0.8, *;q=0.5, de-DE;q=0.7"
	tags := i18n.ParseAcceptLanguage(header)

	expected := []string{"fr-CH", "fr", "en", "de-DE"}
	if len(tags) != len(expected) {
		t.Fatalf("expected %d tags, got %d: %v", len(expected), len(tags), tags)
	}
	for i, exp := range expected {
		if tags[i] != exp {
			t.Errorf("tags[%d] expected %q, got %q", i, exp, tags[i])
		}
	}

	// Empty header
	if len(i18n.ParseAcceptLanguage("")) != 0 {
		t.Errorf("expected empty slice for empty header")
	}
}

func TestPlural_Selection(t *testing.T) {
	tests := []struct {
		lang  string
		count int
		want  i18n.PluralCategory
	}{
		// English
		{"en", 1, i18n.PluralOne},
		{"en", 0, i18n.PluralOther},
		{"en", 2, i18n.PluralOther},
		{"en", -1, i18n.PluralOne},

		// French
		{"fr", 0, i18n.PluralOne},
		{"fr", 1, i18n.PluralOne},
		{"fr", 2, i18n.PluralOther},

		// Asian (no plural)
		{"zh", 0, i18n.PluralOther},
		{"zh", 1, i18n.PluralOther},
		{"ja", 5, i18n.PluralOther},

		// Russian
		{"ru", 1, i18n.PluralOne},
		{"ru", 21, i18n.PluralOne},
		{"ru", 2, i18n.PluralFew},
		{"ru", 4, i18n.PluralFew},
		{"ru", 5, i18n.PluralMany},
		{"ru", 11, i18n.PluralMany},

		// Polish
		{"pl", 1, i18n.PluralOne},
		{"pl", 2, i18n.PluralFew},
		{"pl", 5, i18n.PluralMany},

		// Arabic
		{"ar", 0, i18n.PluralZero},
		{"ar", 1, i18n.PluralOne},
		{"ar", 2, i18n.PluralTwo},
		{"ar", 3, i18n.PluralFew},
		{"ar", 10, i18n.PluralFew},
		{"ar", 11, i18n.PluralMany},
		{"ar", 99, i18n.PluralMany},
		{"ar", 100, i18n.PluralOther},
	}

	for _, tc := range tests {
		t.Run(tc.lang, func(t *testing.T) {
			got := i18n.SelectPlural(tc.lang, tc.count)
			if got != tc.want {
				t.Errorf("SelectPlural(%q, %d): expected %v, got %v", tc.lang, tc.count, tc.want, got)
			}
		})
	}
}

func TestCatalog_JSON_YAML_Loading(t *testing.T) {
	loc := i18n.ParseLocale("en")
	cat := i18n.NewCatalog(loc)

	jsonDoc := []byte(`{
		"welcome": "Welcome!",
		"user": {
			"greeting": "Hello, {name}!",
			"items": {
				"one": "1 item",
				"other": "{count} items"
			}
		}
	}`)

	if err := cat.LoadJSON(jsonDoc); err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}

	if cat.Count() != 3 {
		t.Errorf("expected 3 entries, got %d", cat.Count())
	}

	val, ok := cat.Get("user.greeting")
	if !ok || val != "Hello, {name}!" {
		t.Errorf("expected 'Hello, {name}!', got %v", val)
	}

	// Invalid JSON
	if err := cat.LoadJSON([]byte(`invalid json`)); err == nil {
		t.Error("expected error loading invalid JSON")
	}

	// YAML Loading
	locFR := i18n.ParseLocale("fr")
	catFR := i18n.NewCatalog(locFR)

	yamlDoc := []byte(`
welcome: "Bienvenue!"
auth:
  login: "Connexion"
  notifications:
    one: "1 notification"
    other: "{count} notifications"
`)

	if err := catFR.LoadYAML(yamlDoc); err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}

	if val, ok := catFR.Get("auth.login"); !ok || val != "Connexion" {
		t.Errorf("expected 'Connexion', got %v", val)
	}

	// Invalid YAML
	if err := catFR.LoadYAML([]byte(`: : bad yaml`)); err == nil {
		t.Error("expected error loading invalid YAML")
	}
}

func TestBundle_LoadFS(t *testing.T) {
	fsys := fstest.MapFS{
		"en.json": &fstest.MapFile{
			Data: []byte(`{"hello": "Hello", "apples": {"one": "1 apple", "other": "{count} apples"}}`),
		},
		"es.yaml": &fstest.MapFile{
			Data: []byte("hello: \"Hola\"\napples:\n  one: \"1 manzana\"\n  other: \"{count} manzanas\"\n"),
		},
		"fr.yml": &fstest.MapFile{
			Data: []byte("hello: \"Bonjour\"\n"),
		},
	}

	b := i18n.NewBundle(i18n.WithDefaultLocale("en"))
	if err := b.LoadFS(fsys, "*.json"); err != nil {
		t.Fatalf("LoadFS json: %v", err)
	}
	if err := b.LoadFS(fsys, "*.y*ml"); err != nil {
		t.Fatalf("LoadFS yaml: %v", err)
	}

	// Test English
	lEN := b.NewLocalizer("en")
	if got := lEN.T("hello"); got != "Hello" {
		t.Errorf("expected 'Hello', got %q", got)
	}
	if got := lEN.Plural("apples", 1); got != "1 apple" {
		t.Errorf("expected '1 apple', got %q", got)
	}
	if got := lEN.Plural("apples", 5); got != "5 apples" {
		t.Errorf("expected '5 apples', got %q", got)
	}

	// Test Spanish
	lES := b.NewLocalizer("es")
	if got := lES.T("hello"); got != "Hola" {
		t.Errorf("expected 'Hola', got %q", got)
	}
	if got := lES.Plural("apples", 3); got != "3 manzanas" {
		t.Errorf("expected '3 manzanas', got %q", got)
	}

	// Test French
	lFR := b.NewLocalizer("fr")
	if got := lFR.T("hello"); got != "Bonjour" {
		t.Errorf("expected 'Bonjour', got %q", got)
	}
}

type UserPayload struct {
	Name string
	Role string
}

func TestLocalizer_InterpolationAndFallbacks(t *testing.T) {
	b := i18n.NewBundle(i18n.WithDefaultLocale("en"))

	_ = b.LoadJSON("en", []byte(`{
		"welcome": "Welcome, {name}!",
		"role_info": "{Name} is a {Role}",
		"only_in_en": "Global default message"
	}`))

	_ = b.LoadJSON("fr", []byte(`{
		"welcome": "Bienvenue, {name}!"
	}`))

	_ = b.LoadJSON("fr-CA", []byte(`{
		"welcome": "Bienvenue au Canada, {name}!"
	}`))

	// 1. Exact match fr-CA
	lCA := b.NewLocalizer("fr-CA")
	if got := lCA.T("welcome", map[string]any{"name": "Jean"}); got != "Bienvenue au Canada, Jean!" {
		t.Errorf("expected Canadian French welcome, got %q", got)
	}

	// 2. Fallback to fr, then to en
	// "only_in_en" is not in fr-CA or fr, must fallback to en
	if got := lCA.T("only_in_en"); got != "Global default message" {
		t.Errorf("expected fallback to en, got %q", got)
	}

	// 3. Struct interpolation
	user := UserPayload{Name: "Alice", Role: "Admin"}
	lEN := b.NewLocalizer("en")
	if got := lEN.T("role_info", user); got != "Alice is a Admin" {
		t.Errorf("expected 'Alice is a Admin', got %q", got)
	}

	// 4. Missing key returns key
	if got := lEN.T("non.existent.key"); got != "non.existent.key" {
		t.Errorf("expected key itself, got %q", got)
	}
}

func TestHTTP_Middleware(t *testing.T) {
	b := i18n.NewBundle(i18n.WithDefaultLocale("en"))
	_ = b.LoadJSON("en", []byte(`{"greeting": "Hello", "tasks": {"one": "1 task", "other": "{count} tasks"}}`))
	_ = b.LoadJSON("es", []byte(`{"greeting": "Hola", "tasks": {"one": "1 tarea", "other": "{count} tareas"}}`))
	_ = b.LoadJSON("de", []byte(`{"greeting": "Hallo"}`))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		greeting := i18n.T(r.Context(), "greeting")
		tasks := i18n.Plural(r.Context(), "tasks", 3)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(greeting + " | " + tasks))
	})

	mw := i18n.Middleware(b,
		i18n.WithQueryParam("lang"),
		i18n.WithCookieName("locale"),
		i18n.WithContentLanguageHeader(true),
	)
	server := mw(handler)

	// 1. Query parameter resolution (?lang=es)
	req1 := httptest.NewRequest(http.MethodGet, "/?lang=es", nil)
	rec1 := httptest.NewRecorder()
	server.ServeHTTP(rec1, req1)

	if rec1.Header().Get("Content-Language") != "es" {
		t.Errorf("expected Content-Language 'es', got %q", rec1.Header().Get("Content-Language"))
	}
	if rec1.Body.String() != "Hola | 3 tareas" {
		t.Errorf("expected 'Hola | 3 tareas', got %q", rec1.Body.String())
	}

	// 2. Cookie resolution (locale=de)
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(&http.Cookie{Name: "locale", Value: "de"})
	rec2 := httptest.NewRecorder()
	server.ServeHTTP(rec2, req2)

	if rec2.Header().Get("Content-Language") != "de" {
		t.Errorf("expected Content-Language 'de', got %q", rec2.Header().Get("Content-Language"))
	}
	if rec2.Body.String() != "Hallo | 3 tasks" { // tasks falls back to en
		t.Errorf("expected 'Hallo | 3 tasks', got %q", rec2.Body.String())
	}

	// 3. Accept-Language header resolution
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.Header.Set("Accept-Language", "es-MX, es;q=0.9, en;q=0.8")
	rec3 := httptest.NewRecorder()
	server.ServeHTTP(rec3, req3)

	if rec3.Body.String() != "Hola | 3 tareas" {
		t.Errorf("expected 'Hola | 3 tareas', got %q", rec3.Body.String())
	}

	// 4. Default fallback (no headers, cookies, or query)
	req4 := httptest.NewRequest(http.MethodGet, "/", nil)
	rec4 := httptest.NewRecorder()
	server.ServeHTTP(rec4, req4)

	if rec4.Header().Get("Content-Language") != "en" {
		t.Errorf("expected Content-Language 'en', got %q", rec4.Header().Get("Content-Language"))
	}
	if rec4.Body.String() != "Hello | 3 tasks" {
		t.Errorf("expected 'Hello | 3 tasks', got %q", rec4.Body.String())
	}
}

func TestContext_WithoutLocalizer(t *testing.T) {
	ctx := context.Background()

	// Calling T and Plural on context without localizer returns the key
	if got := i18n.T(ctx, "hello"); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
	if got := i18n.Plural(ctx, "items", 5); got != "items" {
		t.Errorf("expected 'items', got %q", got)
	}
	if l := i18n.FromContext(ctx); l != nil {
		t.Errorf("expected nil localizer from empty context")
	}
	if l := i18n.FromContext(nil); l != nil {
		t.Errorf("expected nil localizer from nil context")
	}
}

func TestCatalog_MethodsAndLoadFS(t *testing.T) {
	loc := i18n.ParseLocale("es-419") // Latin America Spanish (UN M.49 numeric code)
	if loc.String() != "es-419" {
		t.Errorf("expected 'es-419', got %q", loc.String())
	}

	cat := i18n.NewCatalog(loc)
	if cat.Locale().Tag != "es-419" {
		t.Errorf("expected 'es-419', got %q", cat.Locale().Tag)
	}

	cat.Set("custom.title", "Titulo")
	if val, ok := cat.Get("custom.title"); !ok || val != "Titulo" {
		t.Errorf("expected 'Titulo', got %v", val)
	}

	fsys := fstest.MapFS{
		"messages.json": &fstest.MapFile{Data: []byte(`{"ping": "pong"}`)},
		"messages.yaml": &fstest.MapFile{Data: []byte("foo: \"bar\"\n")},
		"bad.txt":       &fstest.MapFile{Data: []byte("raw text")},
	}

	if err := cat.LoadFS(fsys, "*.json"); err != nil {
		t.Fatalf("LoadFS json: %v", err)
	}
	if val, ok := cat.Get("ping"); !ok || val != "pong" {
		t.Errorf("expected 'pong', got %v", val)
	}

	if err := cat.LoadFS(fsys, "*.yaml"); err != nil {
		t.Fatalf("LoadFS yaml: %v", err)
	}
	if val, ok := cat.Get("foo"); !ok || val != "bar" {
		t.Errorf("expected 'bar', got %v", val)
	}

	// Unsupported extension
	if err := cat.LoadFS(fsys, "*.txt"); err == nil {
		t.Error("expected error for unsupported extension")
	}
}

func TestBundle_Methods(t *testing.T) {
	b := i18n.NewBundle(i18n.WithDefaultLocale("pt-BR"))
	if b.DefaultLocale().Tag != "pt-BR" {
		t.Errorf("expected 'pt-BR', got %q", b.DefaultLocale().Tag)
	}

	cat := i18n.NewCatalog(i18n.ParseLocale("pt-BR"))
	cat.Set("btn_save", "Salvar")
	b.AddCatalog(cat)

	retrieved, ok := b.GetCatalog("pt-BR")
	if !ok || retrieved == nil {
		t.Fatal("expected to retrieve catalog for pt-BR")
	}
	if val, found := retrieved.Get("btn_save"); !found || val != "Salvar" {
		t.Errorf("expected 'Salvar', got %v", val)
	}

	// Localizer with empty requested locales falls back to default
	loc := b.NewLocalizer()
	if loc.Locale().Tag != "pt-BR" {
		t.Errorf("expected default 'pt-BR', got %q", loc.Locale().Tag)
	}
	if got := loc.T("btn_save"); got != "Salvar" {
		t.Errorf("expected 'Salvar', got %q", got)
	}
}

func TestPluralMessage_Categories(t *testing.T) {
	pm := i18n.PluralMessage{
		Zero:  "zero items",
		One:   "one item",
		Two:   "two items",
		Few:   "few items",
		Many:  "many items",
		Other: "other items",
	}

	if got := pm.Select(i18n.PluralZero); got != "zero items" {
		t.Errorf("expected zero items, got %q", got)
	}
	if got := pm.Select(i18n.PluralOne); got != "one item" {
		t.Errorf("expected one item, got %q", got)
	}
	if got := pm.Select(i18n.PluralTwo); got != "two items" {
		t.Errorf("expected two items, got %q", got)
	}
	if got := pm.Select(i18n.PluralFew); got != "few items" {
		t.Errorf("expected few items, got %q", got)
	}
	if got := pm.Select(i18n.PluralMany); got != "many items" {
		t.Errorf("expected many items, got %q", got)
	}
	if got := pm.Select(i18n.PluralOther); got != "other items" {
		t.Errorf("expected other items, got %q", got)
	}
}

func TestLocalizer_MapStringString(t *testing.T) {
	b := i18n.NewBundle()
	_ = b.LoadJSON("en", []byte(`{"alert": "Warning: {service} failed on {node}"}`))

	l := b.NewLocalizer("en")
	params := map[string]string{
		"service": "authsvc",
		"node":    "worker-1",
	}
	got := l.T("alert", params)
	expected := "Warning: authsvc failed on worker-1"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}
