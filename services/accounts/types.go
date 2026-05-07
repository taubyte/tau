package accounts

import (
	"context"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/peer"
	streams "github.com/taubyte/tau/p2p/streams/service"
	httpService "github.com/taubyte/tau/pkg/http"
)

// AccountsService is the in-process implementation of the Accounts subsystem.
//
// Hangs the Account / Member / User / Plan stores off this struct, plus the
// auth subsystems (sessions / magic-link / WebAuthn) wired by `auth_init.go`.
type AccountsService struct {
	ctx context.Context

	node      peer.Node
	dbFactory kvdb.Factory
	db        kvdb.KVDB

	stream streams.CommandService
	http   httpService.Service

	rootDomain  string
	accountsURL string
	devMode     bool

	// cfg is the in-process slice of accounts subsystem config; held verbatim
	// so handlers can read AccountsURL, magic-link rate limits, etc.
	cfg accountsConfig

	// client is the in-process Client used by callers within the same node.
	// In wire-mode, callers go through clients/p2p/accounts (the P2P client).
	client accounts.Client

	// Auth subsystems for managed-mode login. May be nil in unit tests
	// that exercise only the integration surface.
	sessions  *sessionStore
	magicLink *magicLinkStore
	webAuthn  *webauthnStore
}

// accountsConfig is the subset of pkg/config.Accounts the service needs at
// runtime, copied at construction so we don't keep a reference into the
// caller's mutable config.
type accountsConfig struct {
	sessionTTL string

	// WebAuthn relying-party identity is derived from `core/services/accounts.
	// InferWebAuthn(devMode, rootDomain)` and lives directly on the
	// `AccountsService` struct (see service.go), not in this snapshot.

	emailSMTPHost string
	emailSMTPPort int
	emailSMTPUser string
	emailSMTPPass string
	emailSMTPFrom string
}

// Compile-time check: AccountsService must satisfy core/services/accounts.Service.
var _ accounts.Service = (*AccountsService)(nil)
