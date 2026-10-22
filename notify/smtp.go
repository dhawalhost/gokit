package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"time"
)

// SMTPConfig holds connection parameters for the SMTP server.
type SMTPConfig struct {
	Host               string
	Port               int
	Username           string
	Password           string
	InsecureSkipVerify bool
	RequireTLS         bool
	ConnectTimeout     time.Duration
}

// DefaultSMTPConfig returns default settings suitable for standard SMTP services.
func DefaultSMTPConfig() SMTPConfig {
	return SMTPConfig{
		Host:           "localhost",
		Port:           587,
		ConnectTimeout: 10 * time.Second,
		RequireTLS:     false,
	}
}

// SMTPOption configures an SMTPSender.
type SMTPOption func(*SMTPConfig)

// WithSMTPCredentials sets authentication credentials.
func WithSMTPCredentials(username, password string) SMTPOption {
	return func(c *SMTPConfig) {
		c.Username = username
		c.Password = password
	}
}

// WithSMTPInsecureSkipVerify allows skipping TLS certificate verification (useful for Mailpit / MailHog).
func WithSMTPInsecureSkipVerify(skip bool) SMTPOption {
	return func(c *SMTPConfig) {
		c.InsecureSkipVerify = skip
	}
}

// WithSMTPRequireTLS specifies whether TLS encryption is strictly mandatory.
func WithSMTPRequireTLS(require bool) SMTPOption {
	return func(c *SMTPConfig) {
		c.RequireTLS = require
	}
}

// WithSMTPConnectTimeout sets the network connection timeout.
func WithSMTPConnectTimeout(d time.Duration) SMTPOption {
	return func(c *SMTPConfig) {
		if d > 0 {
			c.ConnectTimeout = d
		}
	}
}

// SMTPSender delivers emails through a standard SMTP server.
type SMTPSender struct {
	cfg SMTPConfig
}

// NewSMTPSender creates a new SMTPSender.
func NewSMTPSender(host string, port int, opts ...SMTPOption) *SMTPSender {
	cfg := DefaultSMTPConfig()
	cfg.Host = host
	cfg.Port = port
	for _, opt := range opts {
		opt(&cfg)
	}
	return &SMTPSender{cfg: cfg}
}

// Send delivers an email via SMTP.
func (s *SMTPSender) Send(ctx context.Context, email Email) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := email.Validate(); err != nil {
		return err
	}

	mimePayload, err := email.BuildMIME()
	if err != nil {
		return fmt.Errorf("notify/smtp: build mime: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)

	dialer := &net.Dialer{
		Timeout: s.cfg.ConnectTimeout,
	}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("notify/smtp: dial %s: %w", addr, err)
	}
	defer func() {
		_ = conn.Close()
	}()

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("notify/smtp: new client: %w", err)
	}
	defer func() {
		_ = client.Quit()
	}()

	// Handle TLS / STARTTLS
	if ok, _ := client.Extension("STARTTLS"); ok {
		tlsConfig := &tls.Config{
			ServerName:         s.cfg.Host,
			InsecureSkipVerify: s.cfg.InsecureSkipVerify, //nolint:gosec // user configurable for dev
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("notify/smtp: starttls: %w", err)
		}
	} else if s.cfg.RequireTLS {
		return fmt.Errorf("notify/smtp: server %s does not support required STARTTLS", s.cfg.Host)
	}

	// Authenticate if credentials are provided
	if s.cfg.Username != "" && s.cfg.Password != "" {
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("notify/smtp: auth: %w", err)
		}
	}

	// Set sender
	if err := client.Mail(email.From); err != nil {
		return fmt.Errorf("notify/smtp: mail from: %w", err)
	}

	// Set all recipients (To, Cc, Bcc)
	recipients := append(append([]string{}, email.To...), email.Cc...)
	recipients = append(recipients, email.Bcc...)

	for _, rcpt := range recipients {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("notify/smtp: rcpt to %s: %w", rcpt, err)
		}
	}

	// Write payload
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("notify/smtp: data command: %w", err)
	}

	if _, err := w.Write(mimePayload); err != nil {
		_ = w.Close()
		return fmt.Errorf("notify/smtp: write payload: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("notify/smtp: close data: %w", err)
	}

	return nil
}
