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

// Role is the Account-side authority of a Member. Plan grants are a separate
// concern (they live on User records) — Role only governs Account-management
// actions like inviting Members, creating Plans, etc.
type Role string

const (
	RoleOwner   Role = "owner"
	RoleAdmin   Role = "admin"
	RoleViewer  Role = "viewer"
	RoleBilling Role = "billing"
)

// Account is the tenancy entity. It holds Members (login principals), Users
// (linked git accounts) and PRefs (account-scoped pointers to Plans).
// Plans themselves are global, not owned by an Account.
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

// User is a git provider account linked to an Account. It is the entity that
// holds plan grants — `tau project new` works as long as the calling git
// user has at least one grant. A User is **not** a login subject; logins are
// authenticated as Members.
type User struct {
	ID              string      `json:"id"                           cbor:"id"`
	AccountID       string      `json:"account_id"                   cbor:"account_id"`
	Provider        string      `json:"provider"                     cbor:"provider"`
	ExternalID      string      `json:"external_id"                  cbor:"external_id"`
	DisplayName     string      `json:"display_name"                 cbor:"display_name"`
	PlanGrants      []PlanGrant `json:"plan_grants"                  cbor:"plan_grants"`
	AddedAt         time.Time   `json:"added_at"                     cbor:"added_at"`
	AddedByMemberID string      `json:"added_by_member_id,omitempty" cbor:"added_by_member_id,omitempty"`
	LastUsedAt      *time.Time  `json:"last_used_at,omitempty"       cbor:"last_used_at,omitempty"`
}

// PlanGrant attaches a PRef to a User. Exactly one grant per User is marked
// IsDefault (used when project config doesn't explicitly disambiguate).
//
// Grants are keyed by PRef name (account-scoped), not by plan ID. When the
// PRef's pointer swaps to a new plan, the user automatically follows the
// upgrade — no re-grant needed.
type PlanGrant struct {
	PRefName  string `json:"pref_name"  cbor:"pref_name"`
	IsDefault bool   `json:"is_default" cbor:"is_default"`
}

// Plan is an immutable, undeletable, global usage-capacity record. Plans are
// not scoped to an Account; the only link between a Plan and an Account is a
// PRef. Modifying a Plan = creating a new immutable Plan record (versioning is
// emergent from the PRef event log, not encoded on the record).
type Plan struct {
	ID          string `json:"id"                     cbor:"id"`
	Name        string `json:"name"                   cbor:"name"`
	DisplayName string `json:"display_name,omitempty" cbor:"display_name,omitempty"`
	Data        []byte `json:"data,omitempty"         cbor:"data,omitempty"` // opaque metadata blob; schema TBD
}

// PRefStatus tracks the lifecycle of a PRef. PRefs cannot be deleted; they go
// `active` ↔ `disabled` via events.
type PRefStatus string

const (
	PRefStatusActive   PRefStatus = "active"
	PRefStatusDisabled PRefStatus = "disabled"
)

// PRef is an account-scoped named pointer to a Plan. Its Name is immortal once
// created; its DisplayName is cosmetic and mutable; its Status is reflected
// from the latest disable/enable event; its current plan is the PlanID of the
// latest `assign` event.
type PRef struct {
	Name        string     `json:"name"                   cbor:"name"`
	AccountID   string     `json:"account_id"             cbor:"account_id"`
	DisplayName string     `json:"display_name,omitempty" cbor:"display_name,omitempty"`
	Status      PRefStatus `json:"status"                 cbor:"status"`
	CreatedAt   time.Time  `json:"created_at"             cbor:"created_at"`
}

// PRefEventKind enumerates the operations recorded in a PRef's event log.
type PRefEventKind string

const (
	PRefEventKindAssign  PRefEventKind = "assign"
	PRefEventKindDisable PRefEventKind = "disable"
	PRefEventKindEnable  PRefEventKind = "enable"
)

// PRefEvent is one entry in a PRef's append-only event log. Each event carries
// the server-stamped time, who initiated it (Member session or `system:<actor>`),
// the kind, and an optional human-readable note. Assign events also carry the
// PlanID newly bound to the PRef.
type PRefEvent struct {
	At       time.Time     `json:"at"                cbor:"at"`
	Kind     PRefEventKind `json:"kind"              cbor:"kind"`
	PlanID   string        `json:"plan_id,omitempty" cbor:"plan_id,omitempty"`
	MemberID string        `json:"member_id"         cbor:"member_id"`
	Note     string        `json:"note,omitempty"    cbor:"note,omitempty"`
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
// git user is linked to, plus that User's PRef grants on the Account.
type VerifyAccountSummary struct {
	ID    string              `json:"id"    cbor:"id"`
	Slug  string              `json:"slug"  cbor:"slug"`
	Name  string              `json:"name"  cbor:"name"`
	PRefs []VerifyPRefSummary `json:"prefs" cbor:"prefs"`
}

// VerifyPRefSummary is one PRef grant in a VerifyAccountSummary.
type VerifyPRefSummary struct {
	Name        string `json:"name"                   cbor:"name"`
	DisplayName string `json:"display_name,omitempty" cbor:"display_name,omitempty"`
	IsDefault   bool   `json:"is_default"             cbor:"is_default"`
}

// ResolveResponse is the result of resolving an account/pref pair against a
// git user, called by the project compiler at compile time.
type ResolveResponse struct {
	Valid  bool   `json:"valid"             cbor:"valid"`
	Reason string `json:"reason,omitempty"  cbor:"reason,omitempty"` // typed: account not active | pref not found | pref disabled | pref has no plan assigned | plan not found | git user not linked | git user has no grant
	PRef   *PRef  `json:"pref,omitempty"    cbor:"pref,omitempty"`
	Plan   *Plan  `json:"plan,omitempty"    cbor:"plan,omitempty"`
}
