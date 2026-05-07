package email

import (
	"bytes"
	"context"
	"errors"
	"net/smtp"
	"strings"
	"testing"
)

func TestStdoutSender_SendCapturesMessages(t *testing.T) {
	var buf bytes.Buffer
	s := NewStdoutSender(&buf)
	ctx := context.Background()

	if err := s.Send(ctx, "alice@example.com", "Subject", "Body line one"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := s.Send(ctx, "bob@example.com", "Other", "Body line two"); err != nil {
		t.Fatalf("Send 2: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "alice@example.com") || !strings.Contains(out, "Body line one") {
		t.Fatalf("buffer missing content: %s", out)
	}

	sent := s.Sent()
	if len(sent) != 2 {
		t.Fatalf("Sent() len = %d, want 2", len(sent))
	}
	if sent[0].To != "alice@example.com" || sent[1].To != "bob@example.com" {
		t.Fatalf("Sent() ordering wrong: %+v", sent)
	}
}

func TestStdoutSender_DefaultWriter(t *testing.T) {
	s := NewStdoutSender(nil)
	if s.w == nil {
		t.Fatalf("expected non-nil default writer")
	}
}

func TestSMTPSender_Validation(t *testing.T) {
	if _, err := NewSMTPSender("", 587, "u", "p", "from@x"); err == nil {
		t.Fatalf("expected error for empty host")
	}
	if _, err := NewSMTPSender("host", 587, "u", "p", ""); err == nil {
		t.Fatalf("expected error for empty from")
	}
	s, err := NewSMTPSender("host", 0, "", "", "from@x")
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	if s.Port != 587 {
		t.Fatalf("default port = %d, want 587", s.Port)
	}
}

func TestSMTPSender_DialerInjection(t *testing.T) {
	s, err := NewSMTPSender("smtp.example.com", 25, "user", "pass", "noreply@example.com")
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	called := false
	s.dialer = func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
		called = true
		if addr != "smtp.example.com:25" {
			t.Errorf("addr wrong: %s", addr)
		}
		if from != "noreply@example.com" {
			t.Errorf("from wrong: %s", from)
		}
		if len(to) != 1 || to[0] != "alice@example.com" {
			t.Errorf("to wrong: %v", to)
		}
		if !strings.Contains(string(msg), "Subject: Hello") {
			t.Errorf("msg missing Subject header: %s", string(msg))
		}
		return nil
	}
	if err := s.Send(context.Background(), "alice@example.com", "Hello", "Body text"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !called {
		t.Fatalf("dialer not invoked")
	}
}

func TestSMTPSender_DialerError(t *testing.T) {
	s, _ := NewSMTPSender("smtp.example.com", 25, "", "", "noreply@example.com")
	s.dialer = func(string, smtp.Auth, string, []string, []byte) error {
		return errors.New("connect refused")
	}
	if err := s.Send(context.Background(), "alice@example.com", "S", "B"); err == nil {
		t.Fatalf("expected error from dialer")
	}
}
