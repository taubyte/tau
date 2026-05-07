package accounts

import (
	"context"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
)

// Client is the consumer-side interface for the Accounts subsystem.
type Client interface {
	// Integration surface — the two methods the rest of tau actually calls.
	Verify(ctx context.Context, provider, externalID string) (*VerifyResponse, error)
	ResolvePlan(ctx context.Context, accountSlug, planSlug, provider, externalID string) (*ResolveResponse, error)

	// Management surface — requires a Member session.
	Accounts() Accounts
	Members(accountID string) Members
	Users(accountID string) Users
	Plans(accountID string) Plans

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
	Slug         string            `cbor:"slug"`
	Name         string            `cbor:"name"`
	Kind         AccountKind       `cbor:"kind"`
	AuthMode     AuthMode          `cbor:"auth_mode"`
	AuthConfig   *AuthConfig       `cbor:"auth_config,omitempty"`
	PlanTemplate string            `cbor:"plan_template,omitempty"`
	Metadata     map[string]string `cbor:"metadata,omitempty"`
}

// UpdateAccountInput is the partial-update payload for an Account.
type UpdateAccountInput struct {
	Name         *string           `cbor:"name,omitempty"`
	AuthMode     *AuthMode         `cbor:"auth_mode,omitempty"`
	AuthConfig   *AuthConfig       `cbor:"auth_config,omitempty"`
	PlanTemplate *string           `cbor:"plan_template,omitempty"`
	Status       *AccountStatus    `cbor:"status,omitempty"`
	Metadata     map[string]string `cbor:"metadata,omitempty"`
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
	Grant(ctx context.Context, userID string, in GrantPlanInput) error
	Revoke(ctx context.Context, userID, planID string) error
}

// AddUserInput links a git provider account to the Account.
type AddUserInput struct {
	Provider    string `cbor:"provider"`
	ExternalID  string `cbor:"external_id"`
	DisplayName string `cbor:"display_name,omitempty"`
}

// GrantPlanInput grants a Plan to a User.
type GrantPlanInput struct {
	PlanID    string `cbor:"plan_id"`
	IsDefault bool   `cbor:"is_default,omitempty"`
}

// Plans is the Plan CRUD surface for one Account.
type Plans interface {
	Create(ctx context.Context, in CreatePlanInput) (*Plan, error)
	Get(ctx context.Context, planID string) (*Plan, error)
	GetBySlug(ctx context.Context, slug string) (*Plan, error)
	List(ctx context.Context) ([]string, error)
	Update(ctx context.Context, planID string, in UpdatePlanInput) (*Plan, error)
	Delete(ctx context.Context, planID string) error
}

// CreatePlanInput is the payload for creating a new Plan.
type CreatePlanInput struct {
	Slug       string      `cbor:"slug"`
	Name       string      `cbor:"name"`
	Mode       PlanMode    `cbor:"mode"`
	Dimensions []Dimension `cbor:"dimensions,omitempty"`
	Period     string      `cbor:"period,omitempty"`
}

// UpdatePlanInput is the partial-update payload for a Plan.
type UpdatePlanInput struct {
	Name       *string     `cbor:"name,omitempty"`
	Mode       *PlanMode   `cbor:"mode,omitempty"`
	Dimensions []Dimension `cbor:"dimensions,omitempty"`
	Period     *string     `cbor:"period,omitempty"`
	Status     *PlanStatus `cbor:"status,omitempty"`
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
