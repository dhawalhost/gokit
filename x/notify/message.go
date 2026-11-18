package notify

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"path/filepath"
	"strings"
	"time"
)

var (
	// ErrMissingRecipient is returned when an email has no recipients.
	ErrMissingRecipient = errors.New("notify: email must have at least one recipient (To, Cc, or Bcc)")
	// ErrMissingSender is returned when an email has no From address.
	ErrMissingSender = errors.New("notify: email must have a From address")
	// ErrMissingSubject is returned when an email has no Subject.
	ErrMissingSubject = errors.New("notify: email must have a Subject")
	// ErrEmptyBody is returned when an email has neither Text nor HTML body.
	ErrEmptyBody = errors.New("notify: email must have either TextBody or HTMLBody")
	// ErrInvalidEmailAddress is returned when an email address cannot be parsed.
	ErrInvalidEmailAddress = errors.New("notify: invalid email address format")
	// ErrMissingSMSRecipient is returned when an SMS has no recipient phone number.
	ErrMissingSMSRecipient = errors.New("notify: SMS must have a To phone number")
	// ErrEmptySMSMessage is returned when an SMS has an empty message body.
	ErrEmptySMSMessage = errors.New("notify: SMS message cannot be empty")
)

// Attachment represents a file attached to an email.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Email represents a transactional or broadcast email message.
type Email struct {
	From        string
	To          []string
	Cc          []string
	Bcc         []string
	ReplyTo     string
	Subject     string
	TextBody    string
	HTMLBody    string
	Headers     map[string]string
	Attachments []Attachment
	SentAt      time.Time
}

// Validate checks whether the email contains all required fields and valid addresses.
func (e *Email) Validate() error {
	if strings.TrimSpace(e.From) == "" {
		return ErrMissingSender
	}
	if _, err := mail.ParseAddress(e.From); err != nil {
		return fmt.Errorf("%w: From %q: %v", ErrInvalidEmailAddress, e.From, err)
	}

	if len(e.To) == 0 && len(e.Cc) == 0 && len(e.Bcc) == 0 {
		return ErrMissingRecipient
	}

	for _, addr := range e.To {
		if _, err := mail.ParseAddress(addr); err != nil {
			return fmt.Errorf("%w: To %q: %v", ErrInvalidEmailAddress, addr, err)
		}
	}
	for _, addr := range e.Cc {
		if _, err := mail.ParseAddress(addr); err != nil {
			return fmt.Errorf("%w: Cc %q: %v", ErrInvalidEmailAddress, addr, err)
		}
	}
	for _, addr := range e.Bcc {
		if _, err := mail.ParseAddress(addr); err != nil {
			return fmt.Errorf("%w: Bcc %q: %v", ErrInvalidEmailAddress, addr, err)
		}
	}

	if strings.TrimSpace(e.Subject) == "" {
		return ErrMissingSubject
	}

	if strings.TrimSpace(e.TextBody) == "" && strings.TrimSpace(e.HTMLBody) == "" {
		return ErrEmptyBody
	}

	return nil
}

// BuildMIME constructs an RFC-5322 compliant MIME message payload.
func (e *Email) BuildMIME() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}

	buf := &bytes.Buffer{}

	// Write Headers
	fmt.Fprintf(buf, "From: %s\r\n", e.From)
	if len(e.To) > 0 {
		fmt.Fprintf(buf, "To: %s\r\n", strings.Join(e.To, ", "))
	}
	if len(e.Cc) > 0 {
		fmt.Fprintf(buf, "Cc: %s\r\n", strings.Join(e.Cc, ", "))
	}
	if e.ReplyTo != "" {
		fmt.Fprintf(buf, "Reply-To: %s\r\n", e.ReplyTo)
	}

	fmt.Fprintf(buf, "Subject: =?UTF-8?B?%s?=\r\n", base64.StdEncoding.EncodeToString([]byte(e.Subject)))
	buf.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(buf, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))

	for k, v := range e.Headers {
		fmt.Fprintf(buf, "%s: %s\r\n", k, v)
	}

	hasAttachments := len(e.Attachments) > 0
	hasBothBodies := e.TextBody != "" && e.HTMLBody != ""

	if !hasAttachments && !hasBothBodies {
		// Single part message
		if e.HTMLBody != "" {
			buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
			buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
			buf.WriteString(e.HTMLBody)
		} else {
			buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
			buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
			buf.WriteString(e.TextBody)
		}
		return buf.Bytes(), nil
	}

	// Multipart message
	var mixedWriter *multipart.Writer
	var altWriter *multipart.Writer

	if hasAttachments {
		mixedWriter = multipart.NewWriter(buf)
		fmt.Fprintf(buf, "Content-Type: multipart/mixed; boundary=%s\r\n\r\n", mixedWriter.Boundary())

		if hasBothBodies {
			altBuf := &bytes.Buffer{}
			altWriter = multipart.NewWriter(altBuf)
			header := textproto.MIMEHeader{
				"Content-Type": {fmt.Sprintf("multipart/alternative; boundary=%s", altWriter.Boundary())},
			}
			part, err := mixedWriter.CreatePart(header)
			if err != nil {
				return nil, err
			}
			if err := writeAlternativeParts(altWriter, e.TextBody, e.HTMLBody); err != nil {
				return nil, err
			}
			_ = altWriter.Close()
			_, _ = part.Write(altBuf.Bytes())
		} else {
			partHeader := textproto.MIMEHeader{}
			if e.HTMLBody != "" {
				partHeader.Set("Content-Type", "text/html; charset=UTF-8")
				part, err := mixedWriter.CreatePart(partHeader)
				if err != nil {
					return nil, err
				}
				_, _ = part.Write([]byte(e.HTMLBody))
			} else {
				partHeader.Set("Content-Type", "text/plain; charset=UTF-8")
				part, err := mixedWriter.CreatePart(partHeader)
				if err != nil {
					return nil, err
				}
				_, _ = part.Write([]byte(e.TextBody))
			}
		}

		// Write Attachments
		for _, att := range e.Attachments {
			cType := att.ContentType
			if cType == "" {
				cType = mime.TypeByExtension(filepath.Ext(att.Filename))
				if cType == "" {
					cType = "application/octet-stream"
				}
			}
			partHeader := textproto.MIMEHeader{
				"Content-Type":              {fmt.Sprintf("%s; name=\"%s\"", cType, att.Filename)},
				"Content-Disposition":       {fmt.Sprintf("attachment; filename=\"%s\"", att.Filename)},
				"Content-Transfer-Encoding": {"base64"},
			}
			part, err := mixedWriter.CreatePart(partHeader)
			if err != nil {
				return nil, err
			}
			encoded := base64.StdEncoding.EncodeToString(att.Data)
			// Split base64 into lines of 76 characters per RFC 2045
			for len(encoded) > 76 {
				_, _ = part.Write([]byte(encoded[:76] + "\r\n"))
				encoded = encoded[76:]
			}
			if len(encoded) > 0 {
				_, _ = part.Write([]byte(encoded + "\r\n"))
			}
		}

		_ = mixedWriter.Close()
		return buf.Bytes(), nil
	}

	// Just multipart/alternative (no attachments)
	altWriter = multipart.NewWriter(buf)
	fmt.Fprintf(buf, "Content-Type: multipart/alternative; boundary=%s\r\n\r\n", altWriter.Boundary())
	if err := writeAlternativeParts(altWriter, e.TextBody, e.HTMLBody); err != nil {
		return nil, err
	}
	_ = altWriter.Close()
	return buf.Bytes(), nil
}

func writeAlternativeParts(w *multipart.Writer, textBody, htmlBody string) error {
	if textBody != "" {
		textPart, err := w.CreatePart(textproto.MIMEHeader{
			"Content-Type": {"text/plain; charset=UTF-8"},
		})
		if err != nil {
			return err
		}
		if _, err := textPart.Write([]byte(textBody)); err != nil {
			return err
		}
	}

	if htmlBody != "" {
		htmlPart, err := w.CreatePart(textproto.MIMEHeader{
			"Content-Type": {"text/html; charset=UTF-8"},
		})
		if err != nil {
			return err
		}
		if _, err := htmlPart.Write([]byte(htmlBody)); err != nil {
			return err
		}
	}
	return nil
}

// SMS represents a short message service text message.
type SMS struct {
	From    string
	To      string
	Message string
	SentAt  time.Time
}

// Validate checks whether the SMS contains required fields.
func (s *SMS) Validate() error {
	if strings.TrimSpace(s.To) == "" {
		return ErrMissingSMSRecipient
	}
	if strings.TrimSpace(s.Message) == "" {
		return ErrEmptySMSMessage
	}
	return nil
}
