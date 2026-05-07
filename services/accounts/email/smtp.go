package email

import (
	"context"
	"errors"
	"fmt"
	"net/smtp"
)

// SMTPSender delivers email via a configured SMTP server. Credentials are
// passed via PLAIN auth when User/Pass are set; otherwise unauthenticated
// localhost-style relay (used by some private cluster mail relays).
type SMTPSender struct {
	Host string
	Port int
	User string
	Pass string
	From string

	// dialer is overridable for tests.
	dialer func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error
}

// NewSMTPSender builds a SMTPSender from config. Returns an error when the
// minimum-required fields are unset and AllowStdoutFallback wouldn't apply
// (caller decides which sender to use).
func NewSMTPSender(host string, port int, user, pass, from string) (*SMTPSender, error) {
	if host == "" {
		return nil, errors.New("email: SMTP host required")
	}
	if from == "" {
		return nil, errors.New("email: SMTP From address required")
	}
	if port == 0 {
		port = 587
	}
	return &SMTPSender{
		Host:   host,
		Port:   port,
		User:   user,
		Pass:   pass,
		From:   from,
		dialer: smtp.SendMail,
	}, nil
}

// Send delivers a plain-text message. v1 ships text-only; HTML / MIME comes
// when we have richer templates.
func (s *SMTPSender) Send(ctx context.Context, to, subject, body string) error {
	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s\r\n", s.From, to, subject, body))
	var auth smtp.Auth
	if s.User != "" {
		auth = smtp.PlainAuth("", s.User, s.Pass, s.Host)
	}
	if err := s.dialer(addr, auth, s.From, []string{to}, msg); err != nil {
		return fmt.Errorf("email: SMTP send to %s: %w", to, err)
	}
	return nil
}
