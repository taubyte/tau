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
//   /lookup/account/slug/{slug}/{account_id}                          → 8-byte unixnano created-at
//   /lookup/email/{sha256(lower(email))}/{account_id}/{member_id}     → 8-byte unixnano added-at
//   /lookup/external/{provider}/{subject}/{account_id}/{member_id}    → 8-byte unixnano added-at
//   /lookup/git/user/{provider}/{external_id}/{account_id}/{user_id} → 8-byte unixnano added-at
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

// LookupAccountSlugPrefix / LookupAccountSlugEntryPath: one key per claimant
// rather than a single key per slug holding the owning account id. A contended
// key would let two nodes creating accounts with the same slug both write it,
// last-write-wins discard one, and leave the losing account existing with a
// slug that resolves to somebody else — an orphan nothing can detect. Per
// claimant, both claims survive and lookupIDBySlug settles them
// deterministically. See AGENTS.md, "Designing around kvdb".
func LookupAccountSlugPrefix(slug string) string {
	return prefixLookup + "account/slug/" + slug + "/"
}

func LookupAccountSlugEntryPath(slug, accountID string) string {
	return LookupAccountSlugPrefix(slug) + accountID
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
	return prefixLookup + "git/user/" + provider + "/" + externalID + "/"
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
