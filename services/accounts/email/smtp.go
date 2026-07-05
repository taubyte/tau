package email

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/smtp"
	"time"
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

// Send delivers a plain-text message with the full transactional-email
// header set gmail/microsoft expect before they stop classifying as bulk:
// MIME-Version + Content-Type so the body isn't sniffed, Date so the message
// isn't "no temporal reference", and a friendly From if the operator gave
// one. We parse s.From with net/mail so display-name forms like
// `tau <noreply@ac.dz>` set the header correctly while the SMTP envelope
// still uses just the address — sending the display form on MAIL FROM
// would 501 every relay on the path.
func (s *SMTPSender) Send(ctx context.Context, to, subject, body string) error {
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("email: parse From %q: %w", s.From, err)
	}
	msg := []byte(
		"From: " + from.String() + "\r\n" +
			"To: " + to + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: text/plain; charset=UTF-8\r\n" +
			"Content-Transfer-Encoding: 8bit\r\n" +
			"\r\n" +
			body + "\r\n",
	)
	var auth smtp.Auth
	if s.User != "" {
		auth = smtp.PlainAuth("", s.User, s.Pass, s.Host)
	}
	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	if err := s.dialer(addr, auth, from.Address, []string{to}, msg); err != nil {
		return fmt.Errorf("email: SMTP send to %s: %w", to, err)
	}
	return nil
}
