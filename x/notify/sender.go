package notify

import (
	"context"
)

// EmailSender abstracts the delivery mechanism for email messages
// (e.g., SMTP, AWS SES, Resend, SendGrid, or Memory).
type EmailSender interface {
	Send(ctx context.Context, email Email) error
}

// SMSSender abstracts the delivery mechanism for SMS text messages
// (e.g., Twilio, AWS SNS, Sinch, or Memory).
type SMSSender interface {
	SendSMS(ctx context.Context, sms SMS) error
}
