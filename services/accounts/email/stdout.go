package email

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
)

// StdoutSender writes emails to a writer (default os.Stdout). Used in dev
// and dream installs where no real SMTP server is configured. Operators in
// production should set Accounts.Email.AllowStdoutFallback = false so the
// service refuses to start without SMTP.
//
// SentMessages records every outbound message so dream tests can assert on
// what was sent without scraping logs.
type StdoutSender struct {
	w io.Writer

	mu   sync.Mutex
	sent []SentMessage
}

// SentMessage is one captured outbound email. Exposed so tests can introspect.
type SentMessage struct {
	To      string
	Subject string
	Body    string
}

// NewStdoutSender returns a sender that writes to w (or os.Stdout when nil).
func NewStdoutSender(w io.Writer) *StdoutSender {
	if w == nil {
		w = os.Stdout
	}
	return &StdoutSender{w: w}
}

// Send writes a one-line marker plus the body to the configured writer and
// records the message for test introspection.
func (s *StdoutSender) Send(ctx context.Context, to, subject, body string) error {
	s.mu.Lock()
	s.sent = append(s.sent, SentMessage{To: to, Subject: subject, Body: body})
	s.mu.Unlock()

	_, err := fmt.Fprintf(s.w, "--- accounts email (stdout) ---\nTo: %s\nSubject: %s\n\n%s\n--- end ---\n", to, subject, body)
	return err
}

// Sent returns a snapshot of the messages sent so far.
func (s *StdoutSender) Sent() []SentMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SentMessage, len(s.sent))
	copy(out, s.sent)
	return out
}
