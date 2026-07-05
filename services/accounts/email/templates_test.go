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

func TestRenderMagicInvite_RendersAllFields(t *testing.T) {
	subject, body, err := RenderMagicInvite(MagicInviteTemplateData{
		InviterDisplay: "Alice (alice@example.com)",
		AccountName:    "Acme Corp",
		Role:           "admin",
		URL:            "https://accounts.example.com/invite?token=abc",
		TTLHours:       168,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Subject names the account so the inbox preview is meaningful.
	if !strings.Contains(subject, "Acme Corp") {
		t.Fatalf("subject must name account: %s", subject)
	}
	// Body surfaces the human-readable inviter, role, URL, TTL.
	for _, want := range []string{
		"Alice (alice@example.com)",
		"Acme Corp",
		"as\nadmin",
		"https://accounts.example.com/invite?token=abc",
		"168 hours",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}

func TestRenderMagicInvite_RequiresURLAndAccountName(t *testing.T) {
	if _, _, err := RenderMagicInvite(MagicInviteTemplateData{AccountName: "Acme"}); err == nil {
		t.Fatalf("expected error for empty URL")
	}
	if _, _, err := RenderMagicInvite(MagicInviteTemplateData{URL: "u"}); err == nil {
		t.Fatalf("expected error for empty AccountName")
	}
}

func TestRenderMagicGitLink_Default_NoConstraints(t *testing.T) {
	subject, body, err := RenderMagicGitLink(MagicGitLinkTemplateData{
		InviterDisplay: "Alice (alice@example.com)",
		AccountName:    "Acme Corp",
		Provider:       "GitHub",
		URL:            "https://accounts.example.com/git-link?token=abc",
		TTLHours:       168,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(subject, "GitHub") || !strings.Contains(subject, "Acme Corp") {
		t.Fatalf("subject must name provider + account: %s", subject)
	}
	for _, want := range []string{
		"Alice (alice@example.com)", "Acme Corp", "GitHub",
		"https://accounts.example.com/git-link?token=abc",
		"168 hours",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
	// Constraint lines must NOT show when both constraints are empty.
	if strings.Contains(body, "pinned this invite") {
		t.Fatalf("body should not mention login constraint when none set")
	}
	if strings.Contains(body, "must be a member") {
		t.Fatalf("body should not mention org constraint when none set")
	}
}

func TestRenderMagicGitLink_WithConstraints(t *testing.T) {
	_, body, err := RenderMagicGitLink(MagicGitLinkTemplateData{
		InviterDisplay:       "Admin",
		AccountName:          "Acme",
		Provider:             "GitHub",
		RequiredLogin:        "alice-dev",
		RequiredOrganization: "taubyte",
		URL:                  "u",
		TTLHours:             24,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(body, "alice-dev") || !strings.Contains(body, "taubyte") {
		t.Fatalf("body must surface pinned constraints: %s", body)
	}
}

func TestRenderMagicGitLink_RequiresURLAccountAndProvider(t *testing.T) {
	if _, _, err := RenderMagicGitLink(MagicGitLinkTemplateData{AccountName: "Acme", Provider: "GitHub"}); err == nil {
		t.Fatalf("expected error for empty URL")
	}
	if _, _, err := RenderMagicGitLink(MagicGitLinkTemplateData{URL: "u", Provider: "GitHub"}); err == nil {
		t.Fatalf("expected error for empty AccountName")
	}
	if _, _, err := RenderMagicGitLink(MagicGitLinkTemplateData{URL: "u", AccountName: "Acme"}); err == nil {
		t.Fatalf("expected error for empty Provider")
	}
}
