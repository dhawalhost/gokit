# notify

`notify` provides a production-grade, extensible notification abstraction for Go microservices, supporting multipart MIME email generation (Plain text + HTML, attachments, custom headers), template rendering, secure SMTP delivery with STARTTLS, SMS delivery interfaces, and an in-memory mock driver designed for unit tests and CI pipelines.

---

## Architecture Overview

```
                      ┌────────────────────────┐
                      │    notify.Notifier     │
                      │  (Coordination Layer)  │
                      └───────────┬────────────┘
                                  │
         ┌────────────────────────┼────────────────────────┐
         ▼                        ▼                        ▼
┌──────────────────┐    ┌──────────────────┐    ┌──────────────────┐
│  TemplateEngine  │    │   EmailSender    │    │    SMSSender     │
│ (Subject/Text/   │    │   (Interface)    │    │   (Interface)    │
│  HTML Rendering) │    └─────────┬────────┘    └─────────┬────────┘
└──────────────────┘              │                       │
                       ┌──────────┴──────────┐            ▼
                       ▼                     ▼     ┌──────────────┐
              ┌─────────────────┐   ┌────────────┐ │ MemorySender │
              │   SMTPSender    │   │MemorySender│ │ (Mock SMS)   │
              │(STARTTLS, Auth, │   │(Mock Email)│ └──────────────┘
              │ RFC-5322 MIME)  │   └────────────┘
              └─────────────────┘
```

---

## Features

- **Unified Models & Validation**: Validates RFC-5322 compliant recipient lists (`To`, `Cc`, `Bcc`), sender addresses, subjects, message bodies, and attachments.
- **Multipart RFC-5322 MIME Builder**: Generates compliant MIME messages with boundary separators, base64 encoded attachments, custom headers (`Reply-To`, `X-Priority`, etc.), and `multipart/alternative` body support.
- **Production SMTP Driver**: Built-in TLS and STARTTLS negotiation, PLAIN authentication, connection timeouts, and custom dialers.
- **Template Engine**: Compiles and renders subject lines, text, and HTML bodies using Go `html/template` with built-in helper functions (`upper`, `lower`, `title`, `trim`, `now`, `formatDate`) and support for custom `template.FuncMap`.
- **Testing Driver (`MemorySender`)**: Thread-safe in-memory recorder capturing all sent emails and SMS messages for deterministic unit testing and CI pipelines, with query helpers like `FindEmailByTo()` and simulated failure hooks.

---

## Installation

```bash
go get github.com/dhawalhost/gokit/notify
```

---

## Quickstart

### 1. High-Level Notifier Coordinator

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/dhawalhost/gokit/notify"
)

func main() {
	// Configure SMTP driver
	emailSender := notify.NewSMTPSender(notify.SMTPConfig{
		Host:     "smtp.sendgrid.net",
		Port:     587,
		Username: "apikey",
		Password: "your-api-key",
		UseTLS:   true, // Uses STARTTLS on port 587
		Timeout:  10 * time.Second,
	})

	// Initialize the Notifier coordinator
	notifier := notify.NewNotifier(
		notify.WithEmailSender(emailSender),
		notify.WithDefaultFrom("noreply@example.com"),
	)

	// Send an email
	err := notifier.SendEmail(context.Background(), notify.Email{
		To:       []string{"user@example.com"},
		Subject:  "Welcome to Gokit!",
		TextBody: "Thank you for registering with us.",
		HTMLBody: "<h1>Welcome!</h1><p>Thank you for registering with us.</p>",
	})
	if err != nil {
		log.Fatalf("failed to send email: %v", err)
	}
}
```

---

### 2. Rendering Dynamic Templates

The `TemplateEngine` renders subject, plain text, and HTML templates using the provided view data:

```go
engine := notify.NewTemplateEngine()

// Register a template
err := engine.Register(notify.Template{
	Name:            "user_welcome",
	SubjectTemplate: "Welcome {{.Name}} to {{.AppName}}!",
	TextTemplate:    "Hi {{.Name}},\n\nWelcome to {{.AppName}}. Your activation code is: {{.Code}}.",
	HTMLTemplate:    "<h2>Hi {{.Name}}</h2><p>Welcome to <strong>{{.AppName}}</strong>.</p><p>Code: <code>{{.Code}}</code></p>",
})
if err != nil {
	log.Fatalf("failed to register template: %v", err)
}

notifier := notify.NewNotifier(
	notify.WithEmailSender(emailSender),
	notify.WithTemplateEngine(engine),
	notify.WithDefaultFrom("noreply@example.com"),
)

// Send using registered template
err = notifier.SendTemplate(context.Background(), "user_welcome", map[string]any{
	"Name":    "Alice",
	"AppName": "WardSeal",
	"Code":    "481920",
}, notify.Email{
	To: []string{"alice@example.com"},
})
```

---

### 3. Adding Attachments and Custom Headers

```go
email := notify.Email{
	From:     "invoicing@example.com",
	To:       []string{"finance@client.com"},
	Cc:       []string{"audits@example.com"},
	Subject:  "Invoice #INV-2026-001",
	TextBody: "Please find attached your invoice.",
	Headers: map[string]string{
		"X-Invoice-ID": "INV-2026-001",
		"X-Priority":   "1",
	},
	Attachments: []notify.Attachment{
		{
			Filename:    "invoice.pdf",
			ContentType: "application/pdf",
			Content:     pdfBytes,
		},
	},
}

if err := emailSender.SendEmail(ctx, email); err != nil {
	log.Printf("failed to send: %v", err)
}
```

---

### 4. Unit Testing with `MemorySender`

`MemorySender` acts as an in-memory email and SMS sink that requires zero external network connections or credentials:

```go
func TestUserRegistration_SendsEmail(t *testing.T) {
	memSender := notify.NewMemorySender()
	notifier := notify.NewNotifier(
		notify.WithEmailSender(memSender),
		notify.WithDefaultFrom("test@example.com"),
	)

	// Invoke your business logic
	err := notifier.SendEmail(context.Background(), notify.Email{
		To:       []string{"alice@example.com"},
		Subject:  "Welcome!",
		TextBody: "Hello Alice!",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Assertions
	if memSender.Count() != 1 {
		t.Fatalf("expected 1 email, got %d", memSender.Count())
	}

	last := memSender.LastEmail()
	if last.Subject != "Welcome!" {
		t.Errorf("expected subject 'Welcome!', got '%s'", last.Subject)
	}

	matches := memSender.FindEmailByTo("alice@example.com")
	if len(matches) != 1 {
		t.Errorf("expected 1 email to alice, found %d", len(matches))
	}
}
```

---

## Built-In Template Helper Functions

Templates have access to built-in convenience functions:

| Function | Usage | Description |
| :--- | :--- | :--- |
| `upper` | `{{ upper .Role }}` | Converts string to uppercase |
| `lower` | `{{ lower .Email }}` | Converts string to lowercase |
| `title` | `{{ title .Name }}` | Converts string to title case |
| `trim` | `{{ trim .Input }}` | Trims leading and trailing whitespace |
| `now` | `{{ now }}` | Returns current `time.Time` |
| `formatDate` | `{{ formatDate .CreatedAt "2006-01-02" }}` | Formats `time.Time` with Go layout |

Additional custom functions can be passed via `template.FuncMap`:

```go
engine := notify.NewTemplateEngineWithFuncs(template.FuncMap{
	"currency": func(amount int64) string {
		return fmt.Sprintf("$%.2f", float64(amount)/100.0)
	},
})
```
