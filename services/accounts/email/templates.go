package email

import (
	"bytes"
	"errors"
	"fmt"
	"text/template"
)

// Embedded text templates for outbound emails. Kept inline so operators don't
// have to ship template files alongside the binary; can be moved to embedded
// FS-backed assets later if richer / HTML-aware templates land.
//
// Variables passed to MagicLinkTemplate:
//   - Code:       the raw magic-link code (~64 hex chars)
//   - URL:        full deep-link URL with the code in the query
//   - TTLMinutes: integer minutes until the code expires (e.g. 15)
//   - AccountName: optional Account display name; rendered when non-empty

const magicLinkBody = `Hi,

A sign-in to tau was just requested for this email address.

To sign in from your terminal (` + "`tau accounts login`" + `) — copy the code below
and paste it into the prompt:

    {{.Code}}

Or click this link to sign in via your browser:

    {{.URL}}

This works once and expires in {{.TTLMinutes}} minutes.

If you didn't request this, you can safely ignore this email — your session
won't be affected.

— tau{{if .AccountName}} ({{.AccountName}}){{end}}
`

const magicLinkSubject = `Your tau sign-in code`

// MagicLinkTemplateData is the variable bag for the magic-link email.
type MagicLinkTemplateData struct {
	Code        string
	URL         string
	TTLMinutes  int
	AccountName string
}

// RenderMagicLink renders the magic-link email body. Subject is constant.
// Errors are surfaced (template parsing/execution); the caller should not
// drop the email on a render failure — that would be a regression.
func RenderMagicLink(d MagicLinkTemplateData) (subject, body string, err error) {
	if d.Code == "" {
		return "", "", errors.New("email: magic-link code required")
	}
	tpl, err := template.New("magic-link").Parse(magicLinkBody)
	if err != nil {
		return "", "", fmt.Errorf("email: parse magic-link template: %w", err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, d); err != nil {
		return "", "", fmt.Errorf("email: render magic-link template: %w", err)
	}
	return magicLinkSubject, buf.String(), nil
}

// Variables passed to MagicInviteTemplate:
//   - InviterDisplay: name/email of the admin who issued the invite (best-effort)
//   - AccountName: target account display name
//   - Role: cluster-side Member role (owner/admin/viewer/billing)
//   - URL: full invite landing URL (PublicURL + /invite?token=...)
//   - TTLHours: integer hours from issue to expiry

const magicInviteBody = `Hi,

{{.InviterDisplay}} has invited you to join {{.AccountName}} as
{{.Role}} on tau.

Open this link to accept or decline:

    {{.URL}}

This invitation expires in {{.TTLHours}} hours. If you weren't
expecting it, you can safely ignore — declining is one click.

— tau
`

const magicInviteSubjectTpl = `You've been invited to {{.AccountName}}`

// MagicInviteTemplateData is the variable bag for the member-invite email.
type MagicInviteTemplateData struct {
	InviterDisplay string
	AccountName    string
	Role           string
	URL            string
	TTLHours       int
}

// Variables passed to MagicGitLinkTemplate:
//   - InviterDisplay: admin display string ("name (email)" or bare email)
//   - AccountName: target account display name
//   - Provider: human-facing provider name (e.g. "GitHub")
//   - RequiredLogin: optional pinned provider handle ("alice-dev") — empty when no constraint
//   - RequiredOrganization: optional pinned provider org / group — empty when no constraint
//   - URL: full landing URL (PublicURL + /git-link?token=...)
//   - TTLHours: integer hours from issue to expiry

const magicGitLinkBody = `Hi,

{{.InviterDisplay}} has invited you to link your {{.Provider}} account
to {{.AccountName}} on tau.{{if .RequiredLogin}}

Please sign in as {{.RequiredLogin}} — the admin pinned this invite to
that specific {{.Provider}} handle.{{end}}{{if .RequiredOrganization}}

Your {{.Provider}} account must be a member of {{.RequiredOrganization}}.{{end}}

Open this link and sign in with {{.Provider}} to complete the link:

    {{.URL}}

This link expires in {{.TTLHours}} hours. If you weren't expecting it,
you can safely ignore — declining is one click.

— tau
`

const magicGitLinkSubjectTpl = `Link your {{.Provider}} to {{.AccountName}}`

// MagicGitLinkTemplateData is the variable bag for the git-link email.
type MagicGitLinkTemplateData struct {
	InviterDisplay       string
	AccountName          string
	Provider             string
	RequiredLogin        string
	RequiredOrganization string
	URL                  string
	TTLHours             int
}

// RenderMagicGitLink renders the git-link invitation email. Subject and
// body both templated so the recipient's inbox preview names the account +
// provider before they open.
func RenderMagicGitLink(d MagicGitLinkTemplateData) (subject, body string, err error) {
	if d.URL == "" {
		return "", "", errors.New("email: git-link URL required")
	}
	if d.AccountName == "" {
		return "", "", errors.New("email: git-link AccountName required")
	}
	if d.Provider == "" {
		return "", "", errors.New("email: git-link Provider required")
	}
	bodyTpl, err := template.New("magic-gitlink-body").Parse(magicGitLinkBody)
	if err != nil {
		return "", "", fmt.Errorf("email: parse magic-gitlink body template: %w", err)
	}
	subjTpl, err := template.New("magic-gitlink-subject").Parse(magicGitLinkSubjectTpl)
	if err != nil {
		return "", "", fmt.Errorf("email: parse magic-gitlink subject template: %w", err)
	}
	var bodyBuf, subjBuf bytes.Buffer
	if err := bodyTpl.Execute(&bodyBuf, d); err != nil {
		return "", "", fmt.Errorf("email: render magic-gitlink body: %w", err)
	}
	if err := subjTpl.Execute(&subjBuf, d); err != nil {
		return "", "", fmt.Errorf("email: render magic-gitlink subject: %w", err)
	}
	return subjBuf.String(), bodyBuf.String(), nil
}

// RenderMagicInvite renders the member-invite email. Both subject and body
// are templated (subject substitutes AccountName so the recipient sees what
// they're being invited to before opening the message).
func RenderMagicInvite(d MagicInviteTemplateData) (subject, body string, err error) {
	if d.URL == "" {
		return "", "", errors.New("email: invite URL required")
	}
	if d.AccountName == "" {
		return "", "", errors.New("email: invite AccountName required")
	}
	bodyTpl, err := template.New("magic-invite-body").Parse(magicInviteBody)
	if err != nil {
		return "", "", fmt.Errorf("email: parse magic-invite body template: %w", err)
	}
	subjTpl, err := template.New("magic-invite-subject").Parse(magicInviteSubjectTpl)
	if err != nil {
		return "", "", fmt.Errorf("email: parse magic-invite subject template: %w", err)
	}
	var bodyBuf, subjBuf bytes.Buffer
	if err := bodyTpl.Execute(&bodyBuf, d); err != nil {
		return "", "", fmt.Errorf("email: render magic-invite body: %w", err)
	}
	if err := subjTpl.Execute(&subjBuf, d); err != nil {
		return "", "", fmt.Errorf("email: render magic-invite subject: %w", err)
	}
	return subjBuf.String(), bodyBuf.String(), nil
}
