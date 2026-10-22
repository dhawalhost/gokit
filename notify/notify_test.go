package notify_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/dhawalhost/gokit/notify"
)

// ---------------------------------------------------------------------
// Message & Validation Tests
// ---------------------------------------------------------------------

func TestEmail_Validation(t *testing.T) {
	tests := []struct {
		name    string
		email   notify.Email
		wantErr error
	}{
		{
			name: "missing from",
			email: notify.Email{
				To:       []string{"user@example.com"},
				Subject:  "Hello",
				TextBody: "World",
			},
			wantErr: notify.ErrMissingSender,
		},
		{
			name: "invalid from",
			email: notify.Email{
				From:     "invalid-email",
				To:       []string{"user@example.com"},
				Subject:  "Hello",
				TextBody: "World",
			},
			wantErr: notify.ErrInvalidEmailAddress,
		},
		{
			name: "missing recipients",
			email: notify.Email{
				From:     "sender@example.com",
				Subject:  "Hello",
				TextBody: "World",
			},
			wantErr: notify.ErrMissingRecipient,
		},
		{
			name: "invalid to",
			email: notify.Email{
				From:     "sender@example.com",
				To:       []string{"bad-address"},
				Subject:  "Hello",
				TextBody: "World",
			},
			wantErr: notify.ErrInvalidEmailAddress,
		},
		{
			name: "invalid cc",
			email: notify.Email{
				From:     "sender@example.com",
				To:       []string{"user@example.com"},
				Cc:       []string{"not an email"},
				Subject:  "Hello",
				TextBody: "World",
			},
			wantErr: notify.ErrInvalidEmailAddress,
		},
		{
			name: "invalid bcc",
			email: notify.Email{
				From:     "sender@example.com",
				To:       []string{"user@example.com"},
				Bcc:      []string{"bad@@email"},
				Subject:  "Hello",
				TextBody: "World",
			},
			wantErr: notify.ErrInvalidEmailAddress,
		},
		{
			name: "missing subject",
			email: notify.Email{
				From:     "sender@example.com",
				To:       []string{"user@example.com"},
				Subject:  "",
				TextBody: "World",
			},
			wantErr: notify.ErrMissingSubject,
		},
		{
			name: "missing body",
			email: notify.Email{
				From:    "sender@example.com",
				To:      []string{"user@example.com"},
				Subject: "Test",
			},
			wantErr: notify.ErrEmptyBody,
		},
		{
			name: "valid email with name",
			email: notify.Email{
				From:     "Support Team <support@example.com>",
				To:       []string{"Customer <customer@example.com>"},
				Cc:       []string{"manager@example.com"},
				Bcc:      []string{"archive@example.com"},
				Subject:  "Order Confirmed",
				TextBody: "Your order has been confirmed.",
			},
			wantErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.email.Validate()
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestSMS_Validation(t *testing.T) {
	tests := []struct {
		name    string
		sms     notify.SMS
		wantErr error
	}{
		{
			name:    "missing to",
			sms:     notify.SMS{Message: "OTP: 1234"},
			wantErr: notify.ErrMissingSMSRecipient,
		},
		{
			name:    "empty message",
			sms:     notify.SMS{To: "+1234567890", Message: "   "},
			wantErr: notify.ErrEmptySMSMessage,
		},
		{
			name:    "valid sms",
			sms:     notify.SMS{To: "+1234567890", Message: "Your code is 123456"},
			wantErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.sms.Validate()
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestEmail_BuildMIME(t *testing.T) {
	// 1. Text only
	e1 := notify.Email{
		From:     "sender@example.com",
		To:       []string{"user@example.com"},
		Subject:  "Simple Text",
		TextBody: "Hello Text",
	}
	mime1, err := e1.BuildMIME()
	if err != nil {
		t.Fatalf("build mime text: %v", err)
	}
	if !strings.Contains(string(mime1), "Content-Type: text/plain; charset=UTF-8") {
		t.Errorf("expected text/plain content type in mime1")
	}

	// 2. HTML only
	e2 := notify.Email{
		From:     "sender@example.com",
		To:       []string{"user@example.com"},
		Subject:  "Simple HTML",
		HTMLBody: "<h1>Hello HTML</h1>",
	}
	mime2, err := e2.BuildMIME()
	if err != nil {
		t.Fatalf("build mime html: %v", err)
	}
	if !strings.Contains(string(mime2), "Content-Type: text/html; charset=UTF-8") {
		t.Errorf("expected text/html in mime2")
	}

	// 3. Alternative (Text + HTML)
	e3 := notify.Email{
		From:     "sender@example.com",
		To:       []string{"user@example.com"},
		Subject:  "Both",
		TextBody: "Plain Version",
		HTMLBody: "<p>HTML Version</p>",
		ReplyTo:  "replies@example.com",
		Headers:  map[string]string{"X-Priority": "1"},
	}
	mime3, err := e3.BuildMIME()
	if err != nil {
		t.Fatalf("build mime alt: %v", err)
	}
	if !strings.Contains(string(mime3), "multipart/alternative") {
		t.Errorf("expected multipart/alternative in mime3")
	}
	if !strings.Contains(string(mime3), "Reply-To: replies@example.com") {
		t.Errorf("expected Reply-To header in mime3")
	}
	if !strings.Contains(string(mime3), "X-Priority: 1") {
		t.Errorf("expected custom header in mime3")
	}

	// 4. Attachments (Multipart/mixed)
	e4 := notify.Email{
		From:     "sender@example.com",
		To:       []string{"user@example.com"},
		Subject:  "Invoice",
		TextBody: "Here is your invoice.",
		HTMLBody: "<b>Here is your invoice.</b>",
		Attachments: []notify.Attachment{
			{
				Filename: "invoice.pdf",
				Data:     []byte("%PDF-1.4 test payload repeated to test base64 line wrapping over seventy-six characters per RFC specifications in MIME multipart formats"),
			},
		},
	}
	mime4, err := e4.BuildMIME()
	if err != nil {
		t.Fatalf("build mime mixed: %v", err)
	}
	if !strings.Contains(string(mime4), "multipart/mixed") {
		t.Errorf("expected multipart/mixed in mime4")
	}
	if !strings.Contains(string(mime4), "filename=\"invoice.pdf\"") {
		t.Errorf("expected attachment filename in mime4")
	}

	// 5. Invalid email fails BuildMIME
	eBad := notify.Email{}
	if _, err := eBad.BuildMIME(); err == nil {
		t.Errorf("expected error on invalid email MIME build")
	}
}

// ---------------------------------------------------------------------
// MemorySender Tests
// ---------------------------------------------------------------------

func TestMemorySender(t *testing.T) {
	mem := notify.NewMemorySender()

	ctx := context.Background()

	// Send email
	email := notify.Email{
		From:     "noreply@example.com",
		To:       []string{"alice@example.com", "bob@example.com"},
		Subject:  "Welcome",
		TextBody: "Welcome to our platform!",
	}
	if err := mem.Send(ctx, email); err != nil {
		t.Fatalf("send email: %v", err)
	}

	// Send SMS
	sms := notify.SMS{
		From:    "+1000",
		To:      "+1234567890",
		Message: "Verification: 8899",
	}
	if err := mem.SendSMS(ctx, sms); err != nil {
		t.Fatalf("send sms: %v", err)
	}

	// Verify Emails
	emails := mem.Emails()
	if len(emails) != 1 || emails[0].Subject != "Welcome" {
		t.Errorf("expected 1 email with Subject Welcome")
	}

	lastE, ok := mem.LastEmail()
	if !ok || lastE.Subject != "Welcome" {
		t.Errorf("expected LastEmail to return welcome email")
	}

	found, ok := mem.FindEmailByTo("alice@example.com")
	if !ok || found.From != "noreply@example.com" {
		t.Errorf("expected to find email to alice")
	}
	_, ok = mem.FindEmailByTo("unknown@example.com")
	if ok {
		t.Errorf("expected not to find email to unknown")
	}

	// Verify SMS
	smsList := mem.SMSMessages()
	if len(smsList) != 1 || smsList[0].Message != "Verification: 8899" {
		t.Errorf("expected 1 SMS message")
	}

	lastSMS, ok := mem.LastSMS()
	if !ok || lastSMS.To != "+1234567890" {
		t.Errorf("expected LastSMS to return sent SMS")
	}

	// Simulated error
	simErr := errors.New("provider down")
	mem.SetError(simErr)
	if err := mem.Send(ctx, email); !errors.Is(err, simErr) {
		t.Errorf("expected simulated error on Send")
	}
	if err := mem.SendSMS(ctx, sms); !errors.Is(err, simErr) {
		t.Errorf("expected simulated error on SendSMS")
	}

	// Reset
	mem.Reset()
	if len(mem.Emails()) != 0 || len(mem.SMSMessages()) != 0 {
		t.Errorf("expected empty lists after reset")
	}
	if _, ok := mem.LastEmail(); ok {
		t.Errorf("expected no last email after reset")
	}
	if _, ok := mem.LastSMS(); ok {
		t.Errorf("expected no last SMS after reset")
	}

	// Context cancel
	ctxCanceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := mem.Send(ctxCanceled, email); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on email send")
	}
	if err := mem.SendSMS(ctxCanceled, sms); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on sms send")
	}
}

// ---------------------------------------------------------------------
// TemplateEngine Tests
// ---------------------------------------------------------------------

func TestTemplateEngine(t *testing.T) {
	eng := notify.NewTemplateEngine()

	// Register template with custom helper
	eng.AddFunc("exclaim", func(s string) string {
		return s + "!"
	})

	err := eng.Register(
		"welcome",
		"Welcome, {{ .Name }}",
		"Hello {{ .Name | upper }}, your account ID is {{ .AccountID }}.",
		"<h1>Welcome {{ .Name | exclaim }}</h1><p>Account: {{ .AccountID }}</p>",
	)
	if err != nil {
		t.Fatalf("register template: %v", err)
	}

	data := map[string]string{
		"Name":      "john",
		"AccountID": "acc-999",
	}

	subj, text, html, err := eng.Render("welcome", data)
	if err != nil {
		t.Fatalf("render template: %v", err)
	}

	if subj != "Welcome, john" {
		t.Errorf("unexpected subject: %q", subj)
	}
	if !strings.Contains(text, "Hello JOHN, your account ID is acc-999.") {
		t.Errorf("unexpected text body: %q", text)
	}
	if !strings.Contains(html, "<h1>Welcome john!</h1>") {
		t.Errorf("unexpected html body: %q", html)
	}

	// Unregistered template error
	_, _, _, err = eng.Render("non_existent", nil)
	if !errors.Is(err, notify.ErrTemplateNotFound) {
		t.Errorf("expected ErrTemplateNotFound, got %v", err)
	}

	// Template syntax error
	err = eng.Register("bad", "{{ unclosed", "", "")
	if err == nil {
		t.Errorf("expected error on invalid template syntax")
	}
	err = eng.Register("bad_text", "", "{{ bad func }}", "")
	if err == nil {
		t.Errorf("expected error on invalid text syntax")
	}
	err = eng.Register("bad_html", "", "", "{{ bad html }}")
	if err == nil {
		t.Errorf("expected error on invalid html syntax")
	}
}

// ---------------------------------------------------------------------
// SMTPSender & Mock Server Tests
// ---------------------------------------------------------------------

func TestSMTPSender(t *testing.T) {
	// Start a mock SMTP listener
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start mock smtp listener: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	var wg sync.WaitGroup
	wg.Add(1)

	// Mock SMTP Server Session
	go func() {
		defer wg.Done()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		tp := textproto.NewConn(conn)
		_ = tp.Writer.PrintfLine("220 smtp.mock.local Service ready")

		for {
			line, err := tp.ReadLine()
			if err != nil {
				return
			}
			parts := strings.Split(line, " ")
			cmd := strings.ToUpper(parts[0])

			switch cmd {
			case "HELO", "EHLO":
				_ = tp.Writer.PrintfLine("250-smtp.mock.local")
				_ = tp.Writer.PrintfLine("250 AUTH PLAIN")
			case "AUTH":
				_ = tp.Writer.PrintfLine("235 2.7.0 Authentication successful")
			case "MAIL":
				_ = tp.Writer.PrintfLine("250 2.1.0 Sender OK")
			case "RCPT":
				_ = tp.Writer.PrintfLine("250 2.1.5 Recipient OK")
			case "DATA":
				_ = tp.Writer.PrintfLine("354 Start mail input; end with <CRLF>.<CRLF>")
				r := tp.DotReader()
				s := bufio.NewScanner(r)
				for s.Scan() {
					// Read message payload
				}
				_ = tp.Writer.PrintfLine("250 2.0.0 OK: queued")
			case "QUIT":
				_ = tp.Writer.PrintfLine("221 2.0.0 Bye")
				return
			default:
				_ = tp.Writer.PrintfLine("502 Command not implemented")
			}
		}
	}()

	sender := notify.NewSMTPSender("127.0.0.1", port,
		notify.WithSMTPCredentials("smtpuser", "smtppass"),
		notify.WithSMTPConnectTimeout(2*time.Second),
		notify.WithSMTPInsecureSkipVerify(true),
		notify.WithSMTPRequireTLS(false),
	)

	email := notify.Email{
		From:     "sender@example.com",
		To:       []string{"recipient@example.com"},
		Subject:  "Test SMTP",
		TextBody: "SMTP body content",
	}

	ctx := context.Background()
	if err := sender.Send(ctx, email); err != nil {
		t.Fatalf("smtp send failed: %v", err)
	}

	wg.Wait()
}

// ---------------------------------------------------------------------
// Notifier Coordinator Tests
// ---------------------------------------------------------------------

func TestNotifier(t *testing.T) {
	memEmail := notify.NewMemorySender()
	memSMS := notify.NewMemorySender()
	tmpl := notify.NewTemplateEngine()

	_ = tmpl.Register("reset_password",
		"Password Reset Request",
		"Click here to reset: {{ .URL }}",
		"<a href=\"{{ .URL }}\">Reset Password</a>",
	)

	n := notify.NewNotifier(
		notify.WithDefaultFrom("noreply@service.com"),
		notify.WithEmailSender(memEmail),
		notify.WithSMSSender(memSMS),
		notify.WithTemplateEngine(tmpl),
		notify.WithLogger(zap.NewNop()),
	)

	ctx := context.Background()

	// 1. SendEmail with default From
	err := n.SendEmail(ctx, notify.Email{
		To:       []string{"user@example.com"},
		Subject:  "Direct Email",
		TextBody: "Direct message",
	})
	if err != nil {
		t.Fatalf("send email: %v", err)
	}

	lastE, ok := memEmail.LastEmail()
	if !ok || lastE.From != "noreply@service.com" {
		t.Errorf("expected default From address, got %q", lastE.From)
	}

	// 2. SendTemplate with options
	err = n.SendTemplate(ctx, []string{"user@example.com"}, "reset_password", map[string]string{
		"URL": "https://auth.example.com/reset?token=xyz",
	},
		notify.WithCc("audit@example.com"),
		notify.WithBcc("sec@example.com"),
		notify.WithReplyTo("support@example.com"),
		notify.WithFrom("custom-sender@service.com"),
		notify.WithHeaders(map[string]string{"X-Priority": "high"}),
		notify.WithAttachments(notify.Attachment{Filename: "notice.txt", Data: []byte("info")}),
	)
	if err != nil {
		t.Fatalf("send template: %v", err)
	}

	lastTmpl, _ := memEmail.LastEmail()
	if lastTmpl.From != "custom-sender@service.com" {
		t.Errorf("expected custom from on template")
	}
	if len(lastTmpl.Cc) != 1 || lastTmpl.Cc[0] != "audit@example.com" {
		t.Errorf("expected CC on template")
	}
	if len(lastTmpl.Attachments) != 1 || lastTmpl.Attachments[0].Filename != "notice.txt" {
		t.Errorf("expected attachment on template")
	}

	// 3. SendSMS
	err = n.SendSMS(ctx, notify.SMS{
		To:      "+15551234567",
		Message: "Your OTP is 9988",
	})
	if err != nil {
		t.Fatalf("send sms: %v", err)
	}
	lastS, ok := memSMS.LastSMS()
	if !ok || lastS.To != "+15551234567" {
		t.Errorf("expected sent SMS")
	}

	// 4. Unconfigured sender errors
	emptyNotifier := notify.NewNotifier()
	if err := emptyNotifier.SendEmail(ctx, notify.Email{From: "a@b.com", To: []string{"c@d.com"}, Subject: "s", TextBody: "t"}); !errors.Is(err, notify.ErrNoEmailSenderConfigured) {
		t.Errorf("expected ErrNoEmailSenderConfigured, got %v", err)
	}
	if err := emptyNotifier.SendTemplate(ctx, []string{"c@d.com"}, "t", nil); !errors.Is(err, notify.ErrNoTemplateEngine) {
		t.Errorf("expected ErrNoTemplateEngine, got %v", err)
	}
	if err := emptyNotifier.SendSMS(ctx, notify.SMS{To: "+1", Message: "m"}); !errors.Is(err, notify.ErrNoSMSSenderConfigured) {
		t.Errorf("expected ErrNoSMSSenderConfigured, got %v", err)
	}

	// 5. Validation errors in SendEmail and SendSMS
	badEmail := notify.Email{From: "invalid"}
	if err := n.SendEmail(ctx, badEmail); err == nil {
		t.Errorf("expected validation error on bad email")
	}
	badSMS := notify.SMS{To: ""}
	if err := n.SendSMS(ctx, badSMS); err == nil {
		t.Errorf("expected validation error on bad sms")
	}

	// 6. Sender failure logging
	memEmail.SetError(errors.New("smtp connection refused"))
	if err := n.SendEmail(ctx, notify.Email{To: []string{"a@b.com"}, Subject: "s", TextBody: "t"}); err == nil {
		t.Errorf("expected error when email sender fails")
	}
	memSMS.SetError(errors.New("sms quota exceeded"))
	if err := n.SendSMS(ctx, notify.SMS{To: "+1000", Message: "test"}); err == nil {
		t.Errorf("expected error when sms sender fails")
	}
}

func TestEmail_BuildMIME_SinglePartAttachments(t *testing.T) {
	// Attachment with HTML-only body
	eHTML := notify.Email{
		From:     "sender@example.com",
		To:       []string{"user@example.com"},
		Subject:  "HTML Attachment",
		HTMLBody: "<h1>HTML Report</h1>",
		Attachments: []notify.Attachment{
			{
				Filename: "data_no_ext",
				Data:     []byte("binary data"),
			},
		},
	}
	mimeHTML, err := eHTML.BuildMIME()
	if err != nil {
		t.Fatalf("build mime html with attachment: %v", err)
	}
	if !strings.Contains(string(mimeHTML), "application/octet-stream") {
		t.Errorf("expected octet-stream fallback for extensionless attachment")
	}

	// Attachment with Text-only body
	eText := notify.Email{
		From:     "sender@example.com",
		To:       []string{"user@example.com"},
		Subject:  "Text Attachment",
		TextBody: "Text Report",
		Attachments: []notify.Attachment{
			{
				Filename:    "log.txt",
				ContentType: "text/plain",
				Data:        []byte("log entry"),
			},
		},
	}
	mimeText, err := eText.BuildMIME()
	if err != nil {
		t.Fatalf("build mime text with attachment: %v", err)
	}
	if !strings.Contains(string(mimeText), "name=\"log.txt\"") {
		t.Errorf("expected attachment name log.txt")
	}
}

func TestSMTPSender_ErrorBranches(t *testing.T) {
	sender := notify.NewSMTPSender("127.0.0.1", 1) // Unreachable port 1

	ctx := context.Background()
	email := notify.Email{
		From:     "a@b.com",
		To:       []string{"c@d.com"},
		Subject:  "Subj",
		TextBody: "Body",
	}

	// Dial failure
	err := sender.Send(ctx, email)
	if err == nil {
		t.Errorf("expected dial error on unreachable port")
	}

	// Context canceled
	ctxCanceled, cancel := context.WithCancel(context.Background())
	cancel()
	err = sender.Send(ctxCanceled, email)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled error")
	}

	// Invalid email
	err = sender.Send(ctx, notify.Email{})
	if err == nil {
		t.Errorf("expected validation error on empty email")
	}
}

func TestTemplateEngine_BuiltinFuncs(t *testing.T) {
	eng := notify.NewTemplateEngine()

	err := eng.Register(
		"helpers",
		"{{ .Title | title }}",
		"{{ .Word | lower }} at {{ formatDate now \"2006\" }}",
		"<p>{{ .Word | lower }}</p>",
	)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	subj, text, html, err := eng.Render("helpers", map[string]string{
		"Title": "hello world",
		"Word":  "ABC",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if subj != "Hello World" {
		t.Errorf("expected title case, got %q", subj)
	}
	if !strings.Contains(text, "abc") {
		t.Errorf("expected lower case in text: %q", text)
	}
	if !strings.Contains(html, "<p>abc</p>") {
		t.Errorf("expected lower case in html: %q", html)
	}
}
