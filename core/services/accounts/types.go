package accounts

import "time"

// AuthMode describes how an Account authenticates its Members.
//
// `managed` lives in core (community + EE) and uses passkey + email magic-link.
// `external_oidc` and `external_saml` are EE-only and stubbed in v1 — community
// rejects them with "external auth modes require Enterprise Edition" and EE
// returns "OIDC implementation not yet shipped" until the IdP integration lands.
type AuthMode string

const (
	AuthModeManaged      AuthMode = "managed"
	AuthModeExternalOIDC AuthMode = "external_oidc"
	AuthModeExternalSAML AuthMode = "external_saml"
)

// AccountKind distinguishes solo accounts from multi-Member organisations.
// Personal is just kind=org with max_members=1; same codepath everywhere.
type AccountKind string

const (
	AccountKindPersonal AccountKind = "personal"
	AccountKindOrg      AccountKind = "org"
)

// AccountStatus tracks lifecycle. `pending-claim` is set by the migration tool
// (when an Account is backfilled but no Member has registered a passkey yet).
type AccountStatus string

const (
	AccountStatusActive       AccountStatus = "active"
	AccountStatusPendingClaim AccountStatus = "pending-claim"
	AccountStatusSuspended    AccountStatus = "suspended"
)

// Role is the Account-side authority of a Member — it governs Account-management
// actions like inviting Members.
type Role string

const (
	RoleOwner   Role = "owner"
	RoleAdmin   Role = "admin"
	RoleViewer  Role = "viewer"
	RoleBilling Role = "billing"
)

// Account is the tenancy entity. It holds Members (login principals) and Users
// (linked git accounts).
type Account struct {
	ID         string            `json:"id"                       cbor:"id"`
	Slug       string            `json:"slug"                     cbor:"slug"`
	Name       string            `json:"name"                     cbor:"name"`
	Kind       AccountKind       `json:"kind"                     cbor:"kind"`
	Status     AccountStatus     `json:"status"                   cbor:"status"`
	AuthMode   AuthMode          `json:"auth_mode"                cbor:"auth_mode"`
	AuthConfig *AuthConfig       `json:"auth_config,omitempty"    cbor:"auth_config,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"       cbor:"metadata,omitempty"`
	CreatedAt  time.Time         `json:"created_at"               cbor:"created_at"`
}

// AuthConfig carries managed-mode magic-link sender hints or external-IdP
// configuration. Concrete shape depends on AuthMode.
type AuthConfig struct {
	// Managed-mode hints. Empty → fall back to global Accounts.Email config.
	MagicLinkFromEmail string `json:"magic_link_from_email,omitempty" cbor:"magic_link_from_email,omitempty"`

	// External-mode (OIDC/SAML) IdP config. Stubbed in v1; full shape lands
	// with the EE OIDC/SAML implementation.
	IssuerURL    string            `json:"issuer_url,omitempty"        cbor:"issuer_url,omitempty"`
	ClientID     string            `json:"client_id,omitempty"         cbor:"client_id,omitempty"`
	ClientSecret string            `json:"client_secret_ref,omitempty" cbor:"client_secret_ref,omitempty"`
	GroupToRole  map[string]Role   `json:"group_to_role,omitempty"     cbor:"group_to_role,omitempty"`
	GroupToPRef  map[string]string `json:"group_to_pref,omitempty"     cbor:"group_to_pref,omitempty"`
	JITProvision bool              `json:"jit_provision,omitempty"     cbor:"jit_provision,omitempty"`
}

// Member is a per-Account human seat with login state. Members hold passkey or
// external-IdP credentials and may invite other Members; they are independent
// from Users (which are linked git accounts).
type Member struct {
	ID                 string              `json:"id"                            cbor:"id"`
	AccountID          string              `json:"account_id"                    cbor:"account_id"`
	Role               Role                `json:"role"                          cbor:"role"`
	PrimaryEmail       string              `json:"primary_email"                 cbor:"primary_email"`
	PasskeyCredentials []PasskeyCredential `json:"passkey_credentials,omitempty" cbor:"passkey_credentials,omitempty"`
	ExternalSubject    string              `json:"external_subject,omitempty"    cbor:"external_subject,omitempty"`
	Status             string              `json:"status"                        cbor:"status"`
	AddedAt            time.Time           `json:"added_at"                      cbor:"added_at"`
	AddedByMemberID    string              `json:"added_by_member_id,omitempty"  cbor:"added_by_member_id,omitempty"`
	LastLoginAt        *time.Time          `json:"last_login_at,omitempty"       cbor:"last_login_at,omitempty"`
}

// PasskeyCredential is a single registered WebAuthn credential. The relying
// party (this auth service) stores the public key; private material lives on
// the user's device and is never sent.
type PasskeyCredential struct {
	CredentialID    []byte    `json:"credential_id"        cbor:"credential_id"`
	PublicKey       []byte    `json:"public_key"           cbor:"public_key"`
	AttestationType string    `json:"attestation_type"     cbor:"attestation_type"`
	SignCount       uint32    `json:"sign_count"           cbor:"sign_count"`
	Transports      []string  `json:"transports,omitempty" cbor:"transports,omitempty"`
	RegisteredAt    time.Time `json:"registered_at"        cbor:"registered_at"`
}

// User is a git provider account linked to an Account. In the community build, being
// linked to an active Account IS the access grant — `tau project new` works for
// any git user linked to the account. A User is **not** a login subject; logins
// are authenticated as Members.
type User struct {
	ID              string     `json:"id"                           cbor:"id"`
	AccountID       string     `json:"account_id"                   cbor:"account_id"`
	Provider        string     `json:"provider"                     cbor:"provider"`
	ExternalID      string     `json:"external_id"                  cbor:"external_id"`
	DisplayName     string     `json:"display_name"                 cbor:"display_name"`
	AddedAt         time.Time  `json:"added_at"                     cbor:"added_at"`
	AddedByMemberID string     `json:"added_by_member_id,omitempty" cbor:"added_by_member_id,omitempty"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"       cbor:"last_used_at,omitempty"`
}

// Session is an authenticated Member session, returned by managed or external
// login flows. Used only by the Accounts management surface — never seen by
// services/auth or by other tau services. Not stored in KV; the bearer is
// JWT-style (json + HMAC) so no cbor tags here.
type Session struct {
	ID        string    `json:"id"`
	MemberID  string    `json:"member_id"`
	AccountID string    `json:"account_id"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Token     string    `json:"token"` // signed (HMAC) compact representation
}

// VerifyResponse is the result of verifying a git provider account against
// the Accounts store. Returned by the verify endpoint that services/auth
// calls after validating a github OAuth token.
type VerifyResponse struct {
	Linked   bool                   `json:"linked"              cbor:"linked"`
	Accounts []VerifyAccountSummary `json:"accounts,omitempty"  cbor:"accounts,omitempty"`
}

// VerifyAccountSummary is one entry in VerifyResponse.Accounts: an Account the
// git user is linked to.
type VerifyAccountSummary struct {
	ID   string `json:"id"   cbor:"id"`
	Slug string `json:"slug" cbor:"slug"`
	Name string `json:"name" cbor:"name"`
}

// ResolveResponse is the result of resolving a git user against an account,
// called by the project compiler at compile time. In the community build this is a
// pure linkage check: Valid is true iff the account is active and the git user
// is linked to it.
type ResolveResponse struct {
	Valid  bool   `json:"valid"            cbor:"valid"`
	Reason string `json:"reason,omitempty" cbor:"reason,omitempty"` // typed: account not found | account not active | git user not linked to account
}
