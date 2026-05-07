// Package email is the abstract sender used by the magic-link flow. SMTP is
// the production sender; stdout is the dev / dream fallback that logs the
// magic-link URL where the operator can read it without a real mail server.
//
// # Backend support
//
// v1 ships only **SMTP** as the built-in production sender, plus the dev
// stdout sender. The interface below is the single seam — operators who
// need a different backend (Amazon SES via SDK, SendGrid HTTP API, Mailgun,
// Postmark, Resend, internal email gateway, or anything else) implement
// `Sender` themselves and inject it into the AccountsService at start time.
//
// Why SMTP-only built-in:
//   - SMTP is universal: every transactional-email provider (Mailgun,
//     SendGrid, SES, Postmark, Resend, Mailtrap, Postfix, …) exposes an
//     SMTP relay. Configuring `Accounts.Email.SMTP` against any of them
//     works without taking a vendor dep.
//   - Adding HTTP-API senders ties the binary to vendor-specific SDKs and
//     auth flows. Operators with strong vendor preferences plug their own.
//   - The seam is one method (`Send`); no wider interface to discover.
//
// Plug-in pattern (typical):
//
//	type sesSender struct { client *ses.Client; from string }
//	func (s *sesSender) Send(ctx context.Context, to, subject, body string) error {
//	    _, err := s.client.SendEmail(ctx, &ses.SendEmailInput{ ... })
//	    return err
//	}
//
// Then construct an AccountsService with a custom-built sender. A future
// PR will expose a sender-injection option on the service config so this
// is a one-line override at startup; until then it requires forking the
// auth-init wiring.
package email

import "context"

// Sender is the minimal contract magic-link send needs from the email layer.
// Subject and body are rendered upstream by services/accounts/email
// templates; this interface just delivers the bytes.
//
// Implementations should be safe for concurrent use — Send is invoked from
// the magic-link store, which can be hit by multiple inbound HTTP requests
// in parallel.
type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
}
