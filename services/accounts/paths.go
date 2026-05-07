package accounts

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// KV path layout for the Accounts subsystem. All structured blobs are
// CBOR-encoded (see kvcodec.go); raw-byte values are noted explicitly.
//
//   /accounts/{id}/profile                                       → Account
//   /accounts/{id}/plans/{plan_id}/profile                       → Plan
//   /accounts/{id}/members/{member_id}/profile                   → Member (sans passkeys)
//   /accounts/{id}/members/{member_id}/passkeys/{credential_id}  → PasskeyCredential
//   /accounts/{id}/users/{user_id}/profile                       → User (sans grants)
//   /accounts/{id}/users/{user_id}/grants/{plan_id}              → PlanGrant
//   /accounts/{id}/signing_key                                   → 32 raw random bytes
//
//   /lookup/account_slug/{slug}                                  → account_id (raw bytes)
//   /lookup/email/{sha256(lower(email))}                         → [{account_id, member_id}, ...]
//   /lookup/external/{provider}/{subject}                        → [{account_id, member_id}, ...]
//   /lookup/git_user/{provider}/{external_id}                    → [{account_id, user_id}, ...]
//
// Sub-collection items (passkeys, grants) are stored as one blob per element
// so individual entries can be added/revoked without rewriting the parent.

const (
	prefixAccounts = "/accounts/"
	prefixLookup   = "/lookup/"
)

// AccountProfilePath returns the KV path for an Account's JSON profile.
func AccountProfilePath(accountID string) string {
	return prefixAccounts + accountID + "/profile"
}

// AccountPlansPrefix returns the KV prefix for one Account's Plans.
func AccountPlansPrefix(accountID string) string {
	return prefixAccounts + accountID + "/plans/"
}

// PlanProfilePath returns the KV path for a Plan's JSON profile.
func PlanProfilePath(accountID, planID string) string {
	return AccountPlansPrefix(accountID) + planID + "/profile"
}

// AccountMembersPrefix returns the KV prefix for one Account's Members.
func AccountMembersPrefix(accountID string) string {
	return prefixAccounts + accountID + "/members/"
}

// MemberProfilePath returns the KV path for a Member's JSON profile (sans passkeys).
func MemberProfilePath(accountID, memberID string) string {
	return AccountMembersPrefix(accountID) + memberID + "/profile"
}

// MemberPasskeysPrefix returns the KV prefix under which a Member's passkeys live.
func MemberPasskeysPrefix(accountID, memberID string) string {
	return AccountMembersPrefix(accountID) + memberID + "/passkeys/"
}

// MemberPasskeyPath returns the KV path for one Member passkey credential.
func MemberPasskeyPath(accountID, memberID, credentialID string) string {
	return MemberPasskeysPrefix(accountID, memberID) + credentialID
}

// AccountUsersPrefix returns the KV prefix for one Account's Users.
func AccountUsersPrefix(accountID string) string {
	return prefixAccounts + accountID + "/users/"
}

// UserProfilePath returns the KV path for a User's JSON profile (sans grants).
func UserProfilePath(accountID, userID string) string {
	return AccountUsersPrefix(accountID) + userID + "/profile"
}

// UserGrantsPrefix returns the KV prefix for one User's plan grants.
func UserGrantsPrefix(accountID, userID string) string {
	return AccountUsersPrefix(accountID) + userID + "/grants/"
}

// UserGrantPath returns the KV path for one User's grant on one Plan.
func UserGrantPath(accountID, userID, planID string) string {
	return UserGrantsPrefix(accountID, userID) + planID
}

// LookupAccountSlugPath returns the KV path mapping slug → account_id.
func LookupAccountSlugPath(slug string) string {
	return prefixLookup + "account_slug/" + slug
}

// LookupEmailPath returns the KV path keyed by sha256(lower(email)) → JSON [(account_id, member_id), ...].
func LookupEmailPath(email string) string {
	return prefixLookup + "email/" + hashEmail(email)
}

// LookupExternalPath returns the KV path keyed by (provider, subject) → JSON [(account_id, member_id), ...].
func LookupExternalPath(provider, subject string) string {
	return prefixLookup + "external/" + provider + "/" + subject
}

// LookupGitUserPath returns the KV path keyed by (provider, external_id) → JSON [(account_id, user_id), ...].
func LookupGitUserPath(provider, externalID string) string {
	return prefixLookup + "git_user/" + provider + "/" + externalID
}

// hashEmail normalises and hashes an email so the keyspace is safe to log
// without exposing addresses (sha256(lower(trim(email)))).
func hashEmail(email string) string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
