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
