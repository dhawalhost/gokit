package notify

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
)

var (
	// ErrNoEmailSenderConfigured is returned when attempting to send an email without a configured EmailSender.
	ErrNoEmailSenderConfigured = errors.New("notify: no email sender configured")
	// ErrNoSMSSenderConfigured is returned when attempting to send an SMS without a configured SMSSender.
	ErrNoSMSSenderConfigured = errors.New("notify: no SMS sender configured")
	// ErrNoTemplateEngine is returned when attempting to send a template without a configured TemplateEngine.
	ErrNoTemplateEngine = errors.New("notify: no template engine configured")
)

// Notifier coordinates email and SMS delivery, template rendering, and default sender addresses.
type Notifier struct {
	defaultFrom    string
	emailSender    EmailSender
	smsSender      SMSSender
	templateEngine *TemplateEngine
	logger         *zap.Logger
}

// Option configures a Notifier.
type Option func(*Notifier)

// WithDefaultFrom sets the fallback From address for outgoing emails.
func WithDefaultFrom(from string) Option {
	return func(n *Notifier) {
		n.defaultFrom = from
	}
}

// WithEmailSender configures the EmailSender backend.
func WithEmailSender(s EmailSender) Option {
	return func(n *Notifier) {
		n.emailSender = s
	}
}

// WithSMSSender configures the SMSSender backend.
func WithSMSSender(s SMSSender) Option {
	return func(n *Notifier) {
		n.smsSender = s
	}
}

// WithTemplateEngine configures the TemplateEngine.
func WithTemplateEngine(e *TemplateEngine) Option {
	return func(n *Notifier) {
		n.templateEngine = e
	}
}

// WithLogger configures a Zap logger.
func WithLogger(l *zap.Logger) Option {
	return func(n *Notifier) {
		if l != nil {
			n.logger = l
		}
	}
}

// NewNotifier creates a Notifier with the provided options.
func NewNotifier(opts ...Option) *Notifier {
	n := &Notifier{
		logger: zap.NewNop(),
	}
	for _, opt := range opts {
		opt(n)
	}
	return n
}

// SendOption configures individual email delivery in SendTemplate.
type SendOption func(*Email)

// WithCc adds CC recipients.
func WithCc(cc ...string) SendOption {
	return func(e *Email) {
		e.Cc = append(e.Cc, cc...)
	}
}

// WithBcc adds BCC recipients.
func WithBcc(bcc ...string) SendOption {
	return func(e *Email) {
		e.Bcc = append(e.Bcc, bcc...)
	}
}

// WithReplyTo sets a custom Reply-To header.
func WithReplyTo(replyTo string) SendOption {
	return func(e *Email) {
		e.ReplyTo = replyTo
	}
}

// WithAttachments attaches files to the email.
func WithAttachments(attachments ...Attachment) SendOption {
	return func(e *Email) {
		e.Attachments = append(e.Attachments, attachments...)
	}
}

// WithHeaders sets custom headers on the email.
func WithHeaders(headers map[string]string) SendOption {
	return func(e *Email) {
		if e.Headers == nil {
			e.Headers = make(map[string]string)
		}
		for k, v := range headers {
			e.Headers[k] = v
		}
	}
}

// WithFrom overrides the default From address for this email.
func WithFrom(from string) SendOption {
	return func(e *Email) {
		e.From = from
	}
}

// SendEmail delivers an email via the configured EmailSender.
func (n *Notifier) SendEmail(ctx context.Context, email Email) error {
	if n.emailSender == nil {
		return ErrNoEmailSenderConfigured
	}

	if email.From == "" {
		email.From = n.defaultFrom
	}

	if err := email.Validate(); err != nil {
		return err
	}

	if err := n.emailSender.Send(ctx, email); err != nil {
		n.logger.Error("notify: send email failed",
			zap.Strings("to", email.To),
			zap.String("subject", email.Subject),
			zap.Error(err),
		)
		return err
	}

	n.logger.Debug("notify: email sent successfully",
		zap.Strings("to", email.To),
		zap.String("subject", email.Subject),
	)
	return nil
}

// SendTemplate renders a named template with data and dispatches the resulting email.
func (n *Notifier) SendTemplate(ctx context.Context, to []string, templateName string, data any, opts ...SendOption) error {
	if n.templateEngine == nil {
		return ErrNoTemplateEngine
	}

	subject, textBody, htmlBody, err := n.templateEngine.Render(templateName, data)
	if err != nil {
		return fmt.Errorf("notify: render template %q: %w", templateName, err)
	}

	email := Email{
		From:     n.defaultFrom,
		To:       to,
		Subject:  subject,
		TextBody: textBody,
		HTMLBody: htmlBody,
	}

	for _, opt := range opts {
		opt(&email)
	}

	return n.SendEmail(ctx, email)
}

// SendSMS delivers an SMS message via the configured SMSSender.
func (n *Notifier) SendSMS(ctx context.Context, sms SMS) error {
	if n.smsSender == nil {
		return ErrNoSMSSenderConfigured
	}

	if err := sms.Validate(); err != nil {
		return err
	}

	if err := n.smsSender.SendSMS(ctx, sms); err != nil {
		n.logger.Error("notify: send sms failed",
			zap.String("to", sms.To),
			zap.Error(err),
		)
		return err
	}

	n.logger.Debug("notify: sms sent successfully",
		zap.String("to", sms.To),
	)
	return nil
}
