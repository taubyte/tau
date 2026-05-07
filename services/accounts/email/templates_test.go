package email

import (
	"strings"
	"testing"
)

func TestRenderMagicLink_RendersCodeAndURLProminently(t *testing.T) {
	subject, body, err := RenderMagicLink(MagicLinkTemplateData{
		Code:       "abcdef0123456789",
		URL:        "https://accounts.tau.example.com/auth/magic?code=abcdef0123456789",
		TTLMinutes: 15,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if subject == "" {
		t.Fatalf("subject empty")
	}
	// Code is on its own line so users can copy-paste.
	if !strings.Contains(body, "    abcdef0123456789\n") {
		t.Fatalf("body should display the code on its own indented line; got: %s", body)
	}
	// URL is also present for browser users.
	if !strings.Contains(body, "https://accounts.tau.example.com/auth/magic?code=abcdef0123456789") {
		t.Fatalf("body missing URL: %s", body)
	}
	// TTL surfaced.
	if !strings.Contains(body, "15 minutes") {
		t.Fatalf("body missing TTL: %s", body)
	}
}

func TestRenderMagicLink_AccountNameOptional(t *testing.T) {
	_, body, err := RenderMagicLink(MagicLinkTemplateData{
		Code: "x", URL: "u", TTLMinutes: 15, AccountName: "Acme",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(body, "(Acme)") {
		t.Fatalf("expected account name in signature: %s", body)
	}

	_, body, _ = RenderMagicLink(MagicLinkTemplateData{
		Code: "x", URL: "u", TTLMinutes: 15,
	})
	if strings.Contains(body, "()") {
		t.Fatalf("empty AccountName should not produce empty parens: %s", body)
	}
}

func TestRenderMagicLink_RequiresCode(t *testing.T) {
	if _, _, err := RenderMagicLink(MagicLinkTemplateData{URL: "u"}); err == nil {
		t.Fatalf("expected error for empty code")
	}
}
