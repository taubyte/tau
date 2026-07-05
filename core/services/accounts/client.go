package accounts

import (
	"context"
	"time"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
)

// Client is the consumer-side interface for the Accounts subsystem.
type Client interface {
	// Integration surface — methods the rest of tau actually calls.
	Verify(ctx context.Context, provider, externalID string) (*VerifyResponse, error)
	ResolvePRef(ctx context.Context, accountSlug, prefName, provider, externalID string) (*ResolveResponse, error)

	// LookupAccountsByEmail returns the IDs of every Account on the cluster
	// that has a Member with this primary_email (case-insensitive, trimmed).
	// Empty input → error. No matches → (empty slice, nil). Result is
	// deduplicated and unordered. No filtering: suspended accounts and
	// pending-claim members are included. Callers fetch details via the
	// existing Accounts() / Members() surfaces and apply their own policy.
	LookupAccountsByEmail(ctx context.Context, email string) ([]string, error)

	// Management surface — requires a Member session (per-account ops) or
	// operator authority (Plans).
	Accounts() Accounts
	Members(accountID string) Members
	Users(accountID string) Users
	PRefs(accountID string) PRefs

	// Plans is the global, immutable Plan catalogue. Not scoped to an account.
	Plans() Plans

	// Login surface — managed (passkey + magic-link); external is EE.
	Login() Login

	Peers(...peerCore.ID) Client
	Close()
}

// Accounts is the Account CRUD surface.
type Accounts interface {
	Create(ctx context.Context, in CreateAccountInput) (*Account, error)
	Get(ctx context.Context, accountID string) (*Account, error)
	GetBySlug(ctx context.Context, slug string) (*Account, error)
	List(ctx context.Context) ([]string, error)
	Update(ctx context.Context, accountID string, in UpdateAccountInput) (*Account, error)
	Delete(ctx context.Context, accountID string) error
}

// CreateAccountInput is the payload for creating a new Account.
type CreateAccountInput struct {
	Slug       string            `cbor:"slug"`
	Name       string            `cbor:"name"`
	Kind       AccountKind       `cbor:"kind"`
	AuthMode   AuthMode          `cbor:"auth_mode"`
	AuthConfig *AuthConfig       `cbor:"auth_config,omitempty"`
	Metadata   map[string]string `cbor:"metadata,omitempty"`
}

// UpdateAccountInput is the partial-update payload for an Account.
type UpdateAccountInput struct {
	Name       *string           `cbor:"name,omitempty"`
	AuthMode   *AuthMode         `cbor:"auth_mode,omitempty"`
	AuthConfig *AuthConfig       `cbor:"auth_config,omitempty"`
	Status     *AccountStatus    `cbor:"status,omitempty"`
	Metadata   map[string]string `cbor:"metadata,omitempty"`
}

// Members is the Member CRUD + invite surface for one Account.
type Members interface {
	Invite(ctx context.Context, in InviteMemberInput) (*Member, error)
	Get(ctx context.Context, memberID string) (*Member, error)
	List(ctx context.Context) ([]string, error)
	Update(ctx context.Context, memberID string, in UpdateMemberInput) (*Member, error)
	Remove(ctx context.Context, memberID string) error
}

// InviteMemberInput is the payload for inviting a new Member to an Account.
// The invite triggers a magic-link email so the user can complete passkey
// registration.
type InviteMemberInput struct {
	PrimaryEmail string `cbor:"primary_email"`
	Role         Role   `cbor:"role"`
}

// UpdateMemberInput is the partial-update payload for a Member.
type UpdateMemberInput struct {
	Role *Role `cbor:"role,omitempty"`
}

// Users is the linked-git-account surface for one Account.
type Users interface {
	Add(ctx context.Context, in AddUserInput) (*User, error)
	Get(ctx context.Context, userID string) (*User, error)
	GetByExternal(ctx context.Context, provider, externalID string) (*User, error)
	List(ctx context.Context) ([]string, error)
	Remove(ctx context.Context, userID string) error
	Grant(ctx context.Context, userID string, in GrantPRefInput) error
	Revoke(ctx context.Context, userID, prefName string) error
}

// AddUserInput links a git provider account to the Account.
type AddUserInput struct {
	Provider    string `cbor:"provider"`
	ExternalID  string `cbor:"external_id"`
	DisplayName string `cbor:"display_name,omitempty"`
}

// GrantPRefInput grants a PRef to a User.
type GrantPRefInput struct {
	PRefName  string `cbor:"pref_name"`
	IsDefault bool   `cbor:"is_default,omitempty"`
}

// Plans is the global Plan catalogue. Plans are immutable and undeletable;
// the only ops are Create, Get, and List.
type Plans interface {
	Create(ctx context.Context, in CreatePlanInput) (*Plan, error)
	Get(ctx context.Context, planID string) (*Plan, error)
	List(ctx context.Context) ([]string, error)
}

// CreatePlanInput is the payload for creating a new Plan record. Name is the
// admin-facing label; DisplayName is cosmetic (defaults to Name when empty).
// Data is an opaque metadata blob; its schema is TBD.
type CreatePlanInput struct {
	Name        string `cbor:"name"`
	DisplayName string `cbor:"display_name,omitempty"`
	Data        []byte `cbor:"data,omitempty"`
}

// PRefs is the PRef surface for one Account. PRef names are immortal once
// created; PRefs cannot be deleted, only disabled.
type PRefs interface {
	Create(ctx context.Context, in CreatePRefInput) (*PRef, error)
	Get(ctx context.Context, name string) (*PRef, error)
	List(ctx context.Context) ([]string, error)
	SetDisplayName(ctx context.Context, name, displayName string) (*PRef, error)
	Assign(ctx context.Context, in AssignPRefInput) (*PRefEvent, error)
	Disable(ctx context.Context, in DisablePRefInput) (*PRefEvent, error)
	Enable(ctx context.Context, in EnablePRefInput) (*PRefEvent, error)
	Events(ctx context.Context, name string, from, to time.Time) ([]PRefEvent, error)
	LatestEvent(ctx context.Context, name string) (*PRefEvent, error)
}

// CreatePRefInput creates a new PRef envelope. Name is immortal and must match
// varname rules ([a-zA-Z_][a-zA-Z0-9_]*); DisplayName defaults to Name if empty.
type CreatePRefInput struct {
	Name        string `cbor:"name"`
	DisplayName string `cbor:"display_name,omitempty"`
	MemberID    string `cbor:"member_id"` // server-resolved from session; "system:<actor>" for non-human
}

// AssignPRefInput records an `assign` event on a PRef.
type AssignPRefInput struct {
	Name     string `cbor:"name"`
	PlanID   string `cbor:"plan_id"`
	MemberID string `cbor:"member_id"`
	Note     string `cbor:"note,omitempty"`
}

// DisablePRefInput records a `disable` event on a PRef.
type DisablePRefInput struct {
	Name     string `cbor:"name"`
	MemberID string `cbor:"member_id"`
	Note     string `cbor:"note,omitempty"`
}

// EnablePRefInput records an `enable` event on a PRef.
type EnablePRefInput struct {
	Name     string `cbor:"name"`
	MemberID string `cbor:"member_id"`
	Note     string `cbor:"note,omitempty"`
}

// Login is the login dispatcher — routes to managed (passkey/magic-link) or
// external (OIDC/SAML, EE) flows depending on the target Account's auth_mode.
type Login interface {
	StartManaged(ctx context.Context, in StartManagedLoginInput) (*ManagedLoginChallenge, error)
	FinishManagedPasskey(ctx context.Context, in FinishPasskeyInput) (*Session, error)
	FinishManagedMagicLink(ctx context.Context, in FinishMagicLinkInput) (*Session, error)
	StartExternal(ctx context.Context, accountSlug string) (*ExternalLoginRedirect, error)
	FinishExternal(ctx context.Context, in FinishExternalLoginInput) (*Session, error)
	VerifySession(ctx context.Context, token string) (*Session, error)
	Logout(ctx context.Context, token string) error
}

// StartManagedLoginInput begins a managed login. Either Email (resolves to one
// or more Account/Member pairs) or AccountSlug (exact target) must be set.
type StartManagedLoginInput struct {
	Email       string `cbor:"email,omitempty"`
	AccountSlug string `cbor:"account_slug,omitempty"`
}

// ManagedLoginChallenge is returned by StartManaged. It carries either the
// WebAuthn challenge (when the resolved Member has a registered passkey) or
// a flag indicating that a magic-link email was sent as the fallback.
//
// SessionID, when present, must be returned in FinishPasskeyInput so the
// server can match the assertion to the originating challenge. Candidates is
// populated when an Email matched multiple (Account, Member) pairs and the
// caller still needs to pick which one to authenticate against.
type ManagedLoginChallenge struct {
	SessionID         string                 `json:"session_id,omitempty"         cbor:"session_id,omitempty"`
	WebAuthnChallenge []byte                 `json:"webauthn_challenge,omitempty" cbor:"webauthn_challenge,omitempty"`
	MagicLinkSent     bool                   `json:"magic_link_sent,omitempty"    cbor:"magic_link_sent,omitempty"`
	Candidates        []VerifyAccountSummary `json:"candidates,omitempty"         cbor:"candidates,omitempty"`
}

// FinishPasskeyInput is the WebAuthn-assertion finishing payload. SessionID
// matches the value returned by StartManaged. Assertion is the raw bytes of
// the browser's `PublicKeyCredential` (JSON-serialized AuthenticatorAssertion).
type FinishPasskeyInput struct {
	SessionID string `json:"session_id" cbor:"session_id"`
	Assertion []byte `json:"assertion"  cbor:"assertion"`
}

// FinishMagicLinkInput is the magic-link finishing payload. ClientIP is
// optional but recommended — the server uses it to per-IP rate-limit failed
// verify attempts so the 6-digit code search space isn't brute-forceable.
// HTTP transports should populate it from the request remote-address; P2P
// transports may leave it empty (peer-id auth provides equivalent isolation).
type FinishMagicLinkInput struct {
	Code     string `json:"code"                 cbor:"code"`
	ClientIP string `json:"client_ip,omitempty"  cbor:"client_ip,omitempty"`
}

// ExternalLoginRedirect carries an OIDC/SAML redirect URL plus state.
type ExternalLoginRedirect struct {
	RedirectURL string `cbor:"redirect_url"`
	State       string `cbor:"state"`
}

// FinishExternalLoginInput is the IdP callback payload.
type FinishExternalLoginInput struct {
	State string `cbor:"state"`
	Code  string `cbor:"code"`
}
