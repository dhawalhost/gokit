package notify

import (
	"bytes"
	"errors"
	"fmt"
	htmltmpl "html/template"
	"strings"
	"sync"
	texttmpl "text/template"
	"time"
)

var (
	// ErrTemplateNotFound is returned when attempting to render an unregistered template.
	ErrTemplateNotFound = errors.New("notify: template not found")
)

// TemplateDefinition groups subject, plain text, and HTML templates.
type TemplateDefinition struct {
	subject *texttmpl.Template
	text    *texttmpl.Template
	html    *htmltmpl.Template
}

// TemplateEngine manages and renders email templates with custom helper functions.
type TemplateEngine struct {
	mu        sync.RWMutex
	templates map[string]TemplateDefinition
	funcMap   texttmpl.FuncMap
}

// NewTemplateEngine creates an initialized TemplateEngine.
func NewTemplateEngine() *TemplateEngine {
	funcs := texttmpl.FuncMap{
		"upper": strings.ToUpper,
		"lower": strings.ToLower,
		"title": strings.Title, //nolint:staticcheck // standard helper
		"now":   time.Now,
		"formatDate": func(t time.Time, layout string) string {
			return t.Format(layout)
		},
	}

	return &TemplateEngine{
		templates: make(map[string]TemplateDefinition),
		funcMap:   funcs,
	}
}

// AddFunc adds a custom template helper function.
func (e *TemplateEngine) AddFunc(name string, fn any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.funcMap[name] = fn
}

// Register compiles and stores subject, text, and HTML templates under a template name.
func (e *TemplateEngine) Register(name, subjectStr, textStr, htmlStr string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	var def TemplateDefinition

	if subjectStr != "" {
		st, err := texttmpl.New(name + "_subj").Funcs(e.funcMap).Parse(subjectStr)
		if err != nil {
			return fmt.Errorf("notify: parse subject template: %w", err)
		}
		def.subject = st
	}

	if textStr != "" {
		tt, err := texttmpl.New(name + "_text").Funcs(e.funcMap).Parse(textStr)
		if err != nil {
			return fmt.Errorf("notify: parse text template: %w", err)
		}
		def.text = tt
	}

	if htmlStr != "" {
		ht, err := htmltmpl.New(name + "_html").Funcs(htmltmpl.FuncMap(e.funcMap)).Parse(htmlStr)
		if err != nil {
			return fmt.Errorf("notify: parse html template: %w", err)
		}
		def.html = ht
	}

	e.templates[name] = def
	return nil
}

// Render evaluates the named template with the provided view data.
func (e *TemplateEngine) Render(name string, data any) (subject, textBody, htmlBody string, err error) {
	e.mu.RLock()
	def, ok := e.templates[name]
	e.mu.RUnlock()

	if !ok {
		return "", "", "", fmt.Errorf("%w: %q", ErrTemplateNotFound, name)
	}

	if def.subject != nil {
		buf := &bytes.Buffer{}
		if err := def.subject.Execute(buf, data); err != nil {
			return "", "", "", fmt.Errorf("notify: execute subject template: %w", err)
		}
		subject = strings.TrimSpace(buf.String())
	}

	if def.text != nil {
		buf := &bytes.Buffer{}
		if err := def.text.Execute(buf, data); err != nil {
			return "", "", "", fmt.Errorf("notify: execute text template: %w", err)
		}
		textBody = buf.String()
	}

	if def.html != nil {
		buf := &bytes.Buffer{}
		if err := def.html.Execute(buf, data); err != nil {
			return "", "", "", fmt.Errorf("notify: execute html template: %w", err)
		}
		htmlBody = buf.String()
	}

	return subject, textBody, htmlBody, nil
}
