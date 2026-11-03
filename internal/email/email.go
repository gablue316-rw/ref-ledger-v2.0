package email

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/gomail.v2"
)

type Email struct {
	To         []string
	From       string
	Subject    string
	Body       string
	Attachment string
}

func (e *Email) Initialize() {
	e.To = []string{}
	e.From = strings.TrimSpace(os.Getenv("EMAIL_USERNAME"))
}

func (e *Email) Send() error {
	// Keep credentials local to avoid shared state between sends.
	password := strings.TrimSpace(
		os.Getenv("EMAIL_APP_PASSWORD"),
	)

	if strings.TrimSpace(e.From) == "" {
		return fmt.Errorf("email sender is missing")
	}

	if password == "" {
		return fmt.Errorf("EMAIL_APP_PASSWORD is missing")
	}

	if len(e.To) == 0 {
		return fmt.Errorf("email recipient is missing")
	}

	recipients := make([]string, 0, len(e.To))

	for _, recipient := range e.To {
		recipient = strings.TrimSpace(recipient)

		if recipient == "" {
			return fmt.Errorf("email recipient is empty")
		}

		recipients = append(recipients, recipient)
	}

	message := gomail.NewMessage()

	message.SetHeader("From", e.From)
	message.SetHeader("To", recipients...)
	message.SetHeader("Subject", e.Subject)
	message.SetBody("text/plain", e.Body)

	if e.Attachment != "" {
		message.Attach(e.Attachment)
	}

	dialer := gomail.NewDialer(
		"smtp.gmail.com",
		587,
		e.From,
		password,
	)

	if err := dialer.DialAndSend(message); err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}

	return nil
}

func (e *Email) AddAttachment(attachment string) {
	e.Attachment = attachment
}

func (e *Email) SetBody(body string) {
	e.Body = body
}

func (e *Email) SetTo(to []string) {
	e.To = to
}

func (e *Email) SetFrom(from string) {
	e.From = from
}

func (e *Email) SetSubject(subject string) {
	e.Subject = subject
}
