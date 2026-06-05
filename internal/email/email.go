package email

import (
	"fmt"
	"strings"

	"gopkg.in/gomail.v2"
)

var emailUser string = "refLedger316@gmail.com"
var emailPwd string = "tzlgytjnsfcsvxbh"

type Email struct {
	To         []string
	From       string
	Subject    string
	Body       string
	Attachment string
}

func (e *Email) Initialize() {
	e.To = []string{}
	e.From = emailUser
}

func (e *Email) Send() error {
	// Implementation for sending email
	m := gomail.NewMessage()
	m.SetHeader("From", e.From)
	m.SetHeader("To", strings.Join(e.To, ","))
	m.SetHeader("Subject", e.Subject)

	m.SetBody("text/plain", e.Body)

	if e.Attachment != "" {
		m.Attach(e.Attachment)
	}

	d := gomail.NewDialer("smtp.gmail.com", 587, emailUser, emailPwd)

	fmt.Println("Sending email to", strings.Join(e.To, ","))
	if err := d.DialAndSend(m); err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}
	fmt.Println("Email sent successfully to", strings.Join(e.To, ","))
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
