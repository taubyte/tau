package accounts

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// KV path layout. All structured blobs are CBOR-encoded; raw-byte values are
// called out explicitly. The ee build keeps its own keys in the ee
// store package (ee/services/accounts), not here.
//
//   /accounts/{id}/profile                                        → Account
//   /accounts/{id}/members/{member_id}/profile                    → Member (sans passkeys)
//   /accounts/{id}/members/{member_id}/passkeys/{credential_id}   → PasskeyCredential
//   /accounts/{id}/users/{user_id}/profile                        → User
//   /accounts/{id}/signing_key                                    → 32 raw random bytes
//
//   /lookup/account_slug/{slug}                                       → account_id (raw bytes)
//   /lookup/email/{sha256(lower(email))}/{account_id}/{member_id}     → 8-byte unixnano added-at
//   /lookup/external/{provider}/{subject}/{account_id}/{member_id}    → 8-byte unixnano added-at
//   /lookup/git_user/{provider}/{external_id}/{account_id}/{user_id}  → 8-byte unixnano added-at
//
// Lookup indexes are one KV key per entry (not a single CBOR slice) so
// concurrent writes from different nodes for distinct (account, member|user)
// pairs touch distinct keys — no read-modify-write blob, no CRDT loss. Same
// rationale for the per-element sub-collections (passkeys).

const (
	prefixAccounts = "/accounts/"
	prefixLookup   = "/lookup/"
)

func AccountProfilePath(accountID string) string {
	return prefixAccounts + accountID + "/profile"
}

func AccountMembersPrefix(accountID string) string {
	return prefixAccounts + accountID + "/members/"
}

func MemberProfilePath(accountID, memberID string) string {
	return AccountMembersPrefix(accountID) + memberID + "/profile"
}

func MemberPasskeysPrefix(accountID, memberID string) string {
	return AccountMembersPrefix(accountID) + memberID + "/passkeys/"
}

func MemberPasskeyPath(accountID, memberID, credentialID string) string {
	return MemberPasskeysPrefix(accountID, memberID) + credentialID
}

func AccountUsersPrefix(accountID string) string {
	return prefixAccounts + accountID + "/users/"
}

func UserProfilePath(accountID, userID string) string {
	return AccountUsersPrefix(accountID) + userID + "/profile"
}

func LookupAccountSlugPath(slug string) string {
	return prefixLookup + "account_slug/" + slug
}

func LookupEmailPrefix(email string) string {
	return prefixLookup + "email/" + hashEmail(email) + "/"
}

func LookupEmailEntryPath(email, accountID, memberID string) string {
	return LookupEmailPrefix(email) + accountID + "/" + memberID
}

func LookupExternalPrefix(provider, subject string) string {
	return prefixLookup + "external/" + provider + "/" + subject + "/"
}

func LookupExternalEntryPath(provider, subject, accountID, memberID string) string {
	return LookupExternalPrefix(provider, subject) + accountID + "/" + memberID
}

func LookupGitUserPrefix(provider, externalID string) string {
	return prefixLookup + "git_user/" + provider + "/" + externalID + "/"
}

func LookupGitUserEntryPath(provider, externalID, accountID, userID string) string {
	return LookupGitUserPrefix(provider, externalID) + accountID + "/" + userID
}

// hashEmail keeps the keyspace safe to log: emails never appear verbatim.
func hashEmail(email string) string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
