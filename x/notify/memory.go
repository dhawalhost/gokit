package notify

import (
	"context"
	"sync"
	"time"
)

// MemorySender is a thread-safe in-memory sink for emails and SMS messages.
// Designed specifically for unit testing, integration tests, and local dev environments.
type MemorySender struct {
	mu     sync.RWMutex
	emails []Email
	sms    []SMS
	err    error
}

// NewMemorySender creates a new MemorySender instance.
func NewMemorySender() *MemorySender {
	return &MemorySender{
		emails: make([]Email, 0),
		sms:    make([]SMS, 0),
	}
}

// SetError instructs the MemorySender to simulate an error on subsequent send operations.
func (m *MemorySender) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

// Send records an email in memory.
func (m *MemorySender) Send(ctx context.Context, email Email) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := email.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return m.err
	}

	if email.SentAt.IsZero() {
		email.SentAt = time.Now().UTC()
	}

	m.emails = append(m.emails, email)
	return nil
}

// SendSMS records an SMS message in memory.
func (m *MemorySender) SendSMS(ctx context.Context, sms SMS) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sms.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return m.err
	}

	if sms.SentAt.IsZero() {
		sms.SentAt = time.Now().UTC()
	}

	m.sms = append(m.sms, sms)
	return nil
}

// Emails returns a copy of all emails sent so far.
func (m *MemorySender) Emails() []Email {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make([]Email, len(m.emails))
	copy(res, m.emails)
	return res
}

// LastEmail returns the most recently sent email, or false if none have been sent.
func (m *MemorySender) LastEmail() (Email, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.emails) == 0 {
		return Email{}, false
	}
	return m.emails[len(m.emails)-1], true
}

// SMSMessages returns a copy of all SMS messages sent so far.
func (m *MemorySender) SMSMessages() []SMS {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make([]SMS, len(m.sms))
	copy(res, m.sms)
	return res
}

// LastSMS returns the most recently sent SMS, or false if none have been sent.
func (m *MemorySender) LastSMS() (SMS, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.sms) == 0 {
		return SMS{}, false
	}
	return m.sms[len(m.sms)-1], true
}

// FindEmailByTo searches for the first sent email addressed to the specified recipient.
func (m *MemorySender) FindEmailByTo(to string) (Email, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, e := range m.emails {
		for _, recipient := range e.To {
			if recipient == to {
				return e, true
			}
		}
	}
	return Email{}, false
}

// Reset clears all recorded emails and SMS messages and resets simulated errors.
func (m *MemorySender) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.emails = make([]Email, 0)
	m.sms = make([]SMS, 0)
	m.err = nil
}
