package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	ds "github.com/ipfs/go-datastore"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/services/auth/password"
)

// store.go is every row auth keeps, laid out by AGENTS.md's kvdb rules. The
// store is a CRDT that merges by last-write-wins per key, so:
//
//   - A KEY TWO NODES COULD WRITE DIFFERENTLY DOES NOT EXIST. A user is a
//     handful of keys, one per field that changes on its own, so a password
//     change cannot overwrite a concurrent disable. An index is a key per
//     entry with the user id in the path, so two claims on one name or one
//     identity are two keys, both visible.
//   - TWO CLAIMS RESOLVE ON READ, the same way on every node: earliest
//     creation, user id breaking the tie (claims below).
//   - A DELETE IS A MARKER. A user is deleted by a tombstone, and a token is
//     revoked by a key of its own, so a concurrent write cannot resurrect
//     either.
//   - WHAT IS CASE-INSENSITIVE IS LOWERCASED before it is a key: names and
//     emails. An issuer and a subject are not, since OIDC compares them
//     exactly; they are hashed, so any byte they carry is a safe segment.
//
// Every row below is in its own prefix, and every prefix is a path segment.
const (
	prefixUsers    = "/users/"
	prefixNames    = "/names/"
	prefixEmails   = "/emails/"
	prefixLinks    = "/links/"
	prefixInvites  = "/invites/"
	prefixSessions = "/sessions/"
	prefixAPIKeys  = "/apikeys/"
	prefixRevoked  = "/revoked/"
	prefixPending  = "/pending/"
	prefixDevice   = "/device/"
	prefixKeys     = "/keys/"
)

// ErrNotFound is a row that is not there.
var ErrNotFound = errors.New("auth: not found")

// digest is a fixed-width, segment-safe stand-in for a value that is either
// secret (a token) or arbitrary (an issuer, a subject).
func digest(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%d:%s", len(p), p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// store is auth's rows over kvdb.
type store struct{ db kvdb.KVDB }

func (s store) put(ctx context.Context, key string, v any) error {
	b, err := cbor.Marshal(v)
	if err != nil {
		return fmt.Errorf("auth: encoding %s: %w", key, err)
	}
	if err := s.db.Put(ctx, key, b); err != nil {
		return fmt.Errorf("auth: writing %s: %w", key, err)
	}
	return nil
}

// get reads key into v; a missing row is ErrNotFound and anything else is an
// error, so "not there" and "could not look" stay apart.
func (s store) get(ctx context.Context, key string, v any) error {
	b, err := s.db.Get(ctx, key)
	switch {
	case errors.Is(err, ds.ErrNotFound):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("auth: reading %s: %w", key, err)
	}
	if err := cbor.Unmarshal(b, v); err != nil {
		return fmt.Errorf("auth: decoding %s: %w", key, err)
	}
	return nil
}

func (s store) has(ctx context.Context, key string) (bool, error) {
	_, err := s.db.Get(ctx, key)
	switch {
	case errors.Is(err, ds.ErrNotFound):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("auth: reading %s: %w", key, err)
	}
	return true, nil
}

func (s store) del(ctx context.Context, key string) error {
	if err := s.db.Delete(ctx, key); err != nil && !errors.Is(err, ds.ErrNotFound) {
		return fmt.Errorf("auth: deleting %s: %w", key, err)
	}
	return nil
}

// children lists the segment directly under prefix of every key below it,
// once each, sorted.
func (s store) children(ctx context.Context, prefix string) ([]string, error) {
	keys, err := s.db.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("auth: listing %s: %w", prefix, err)
	}
	var out []string
	for _, k := range keys {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok || rest == "" {
			continue
		}
		seg, _, _ := strings.Cut(rest, "/")
		out = append(out, seg)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// ─────────────────────────────── claims ───────────────────────────────

// claim is one index entry: a user claiming a name, an email or an identity.
type claim struct {
	Created int64 `cbor:"created"` // unix nanoseconds
	// Issuer, Subject and Login are an identity link's, kept so a user's
	// links can be listed without reversing a hash.
	Issuer  string `cbor:"issuer,omitempty"`
	Subject string `cbor:"subject,omitempty"`
	Login   string `cbor:"login,omitempty"`
}

// winner resolves the claims under prefix to one user: earliest creation,
// lowest user id on a tie, skipping users that are gone. Every node computes
// the same answer from the same replicated rows.
func (s store) winner(ctx context.Context, prefix string, live func(string) bool) (string, claim, error) {
	ids, err := s.children(ctx, prefix)
	if err != nil {
		return "", claim{}, err
	}
	var (
		best   string
		bestAt claim
	)
	for _, id := range ids {
		var c claim
		if err := s.get(ctx, prefix+id, &c); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return "", claim{}, err
		}
		if live != nil && !live(id) {
			continue
		}
		if best == "" || c.Created < bestAt.Created || c.Created == bestAt.Created && id < best {
			best, bestAt = id, c
		}
	}
	if best == "" {
		return "", claim{}, ErrNotFound
	}
	return best, bestAt, nil
}

func nameKey(name string) string   { return prefixNames + strings.ToLower(name) + "/" }
func emailKey(email string) string { return prefixEmails + strings.ToLower(email) + "/" }
func linkKey(issuer, subject string) string {
	return prefixLinks + digest(issuer) + "/" + digest(subject) + "/"
}

// ─────────────────────────────── users ───────────────────────────────

// A user's fields, one key each.
const (
	fieldProfile  = "profile"
	fieldRole     = "role"
	fieldDisabled = "disabled"
	fieldPassword = "password"
	fieldDeleted  = "deleted"
	fieldLinks    = "links"
	fieldAPIKeys  = "apikeys"
)

func userKey(id, field string) string { return prefixUsers + id + "/" + field }

type profileRow struct {
	Name         string   `cbor:"name"`
	Email        string   `cbor:"email,omitempty"`
	Local        bool     `cbor:"local,omitempty"`
	Capabilities []string `cbor:"caps,omitempty"`
	Created      int64    `cbor:"created"`
}

type roleRow struct {
	Role string `cbor:"role"`
}

type flagRow struct {
	Set bool `cbor:"set"`
}

type passwordRow = password.Hash

// ─────────────────────────────── credentials ───────────────────────────────

// sessionRow is a refresh token, keyed by its digest.
type sessionRow struct {
	User    string `cbor:"user"`
	Created int64  `cbor:"created"`
	Expires int64  `cbor:"expires"`
}

// apiKeyRow is an API token, keyed by its id. Sum is the SHA-256 of the
// secret; the secret itself is never stored.
type apiKeyRow struct {
	User    string `cbor:"user"`
	Name    string `cbor:"name"`
	Sum     []byte `cbor:"sum"`
	Created int64  `cbor:"created"`
	Expires int64  `cbor:"expires,omitempty"`
}

// revokedRow marks a credential revoked. Expires says when the marker may be
// forgotten, since the credential stops verifying then anyway.
type revokedRow struct {
	At      int64 `cbor:"at"`
	Expires int64 `cbor:"expires,omitempty"`
}

// Revocations are namespaced by what they revoke.
const (
	revokedAccess  = "access/"
	revokedSession = "session/"
	revokedAPIKey  = "apikey/"
)

func (s store) revoked(ctx context.Context, kind, id string) (bool, error) {
	return s.has(ctx, prefixRevoked+kind+id)
}

func (s store) revoke(ctx context.Context, kind, id string, now time.Time, expires int64) error {
	return s.put(ctx, prefixRevoked+kind+id, revokedRow{At: now.UnixNano(), Expires: expires})
}

// inviteRow is an invite, keyed by the digest of its code.
type inviteRow struct {
	ID        string `cbor:"id"`
	Role      string `cbor:"role"`
	Note      string `cbor:"note,omitempty"`
	CreatedBy string `cbor:"by"`
	Created   int64  `cbor:"created"`
	Expires   int64  `cbor:"expires"`
}

// pendingRow is a browser sign-in between Authorize and Exchange.
type pendingRow struct {
	Provider  string `cbor:"provider"`
	Redirect  string `cbor:"redirect"`
	Verifier  string `cbor:"verifier"`
	Nonce     string `cbor:"nonce,omitempty"`
	Binding   string `cbor:"binding"`
	Invite    string `cbor:"invite,omitempty"`
	ClientID  string `cbor:"client"`
	Created   int64  `cbor:"created"`
	Expires   int64  `cbor:"expires"`
	Exchanged bool   `cbor:"exchanged,omitempty"`
}

// deviceRow is a device sign-in between DeviceStart and DevicePoll.
type deviceRow struct {
	Provider string `cbor:"provider"`
	Interval int64  `cbor:"interval"`
	Expires  int64  `cbor:"expires"`
}

// keyRow is a node's published signing key.
type keyRow struct {
	Public  []byte `cbor:"pub"`
	Node    string `cbor:"node"`
	Created int64  `cbor:"created"`
}
