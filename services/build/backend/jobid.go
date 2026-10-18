package backend

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"regexp"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// jobID names the build of one (repository, ref, commit) triple. It is
// sha256 over a DOMAIN-SEPARATED, LENGTH-FRAMED encoding of the three, in
// unpadded base64url — 43 characters, alphabet A-Z a-z 0-9 - _.
//
// FRAMING IS NOT CEREMONY. Concatenated with a separator, the triple collides:
//
//	("github.com/o/n", "refs/heads/x:deadbeef", "c")
//	("github.com/o/n", "refs/heads/x",          "deadbeef:c")
//
// both hash to mRPa_1vW1rMGzO8xhI_pQOiKYsqLgYdDWvScXW7jknc, because a
// variable-length field's edge can move (jobid_test.go runs exactly that pair).
// A ref is attacker-chosen for any repository an attacker can push to, so the
// collision is a way to forge another push's job id — and since Enqueue returns
// the EXISTING job for an id it already holds, a forged id is a way to make a
// victim's push resolve to an attacker's job, or to occupy an id a victim's
// push will later need. Framed, the three cannot be re-cut.
//
// It is the same class as v1's own state keys —
// `mh.Hash(c.ProjectId + c.ApplicationId + c.Matcher)`, where ("ab","c") and
// ("a","bc") hash identically — and the same class as the auth takeover that
// produced store.go's segment(): a boundary that can move.
//
// IT IS DETERMINISTIC ON PURPOSE. That makes queue.Push's id deduplication and
// Enqueue's idempotency ONE mechanism rather than two that have to agree, and
// it satisfies root AGENTS.md rule 1 — two nodes handed the same push write the
// same key with identical content — which a random id behind a separate /dedup/
// row would not.
//
// Being deterministic it is guessable, so IT IS NEVER A BEARER TOKEN: every
// JobService RPC authorizes, and requireJobID refuses a malformed one before
// any store read.
func jobID(key repokey.Key, ref, commitSHA string) string {
	h := sha256.New()
	h.Write([]byte(jobIDDomain))
	for _, field := range []string{key.String(), ref, commitSHA} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		h.Write(length[:])
		h.Write([]byte(field))
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// jobIDDomain separates this hash from every other sha256 in the tree, so that
// a digest computed for some other purpose over the same bytes is a different
// value. The NUL is part of it: it cannot occur in any of the three fields, so
// the prefix cannot be absorbed into the first one.
const jobIDDomain = "tau.build.v2/job\x00"

// refPattern is what this service will accept as a git ref.
//
// The frontend has already decided branch eligibility (build.proto: "a push to
// an ineligible ref is refused at the HTTP handler and never reaches this
// RPC"), so this is not policy — it is the last check before a stranger's
// string becomes part of a hash and a key segment. Refs are restricted to
// heads because that is the only namespace a push event builds from, and
// nothing about ".." or an empty segment is meaningful in one.
var refPattern = regexp.MustCompile(`^refs/heads/[A-Za-z0-9._/-]{1,255}$`)

// shaPattern is a full 40-character lowercase git object name. v1 accepted
// whatever the payload said.
var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// validRef refuses anything git itself would refuse to check out, plus the two
// shapes that are dangerous in a path even though seg() already neutralizes
// them: a ".." segment and an empty one. Belt and braces, deliberately —
// seg() is what makes the key safe, and this is what keeps a nonsense ref from
// ever becoming a job.
func validRef(ref string) bool {
	if !refPattern.MatchString(ref) {
		return false
	}
	for _, part := range splitSegments(ref) {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func validCommitSHA(sha string) bool { return shaPattern.MatchString(sha) }

// parseRepositoryKey accepts only a key already in repokey's canonical form.
//
// The wire field carries a KEY (`github.com/acme/pay`), not a clone URL, so
// repokey.Parse cannot be called on it directly — it requires a scheme. The
// check instead re-derives: a key is valid exactly when parsing it as an https
// URL yields itself back. That refuses every non-canonical spelling ("Acme/Pay",
// a ".git" suffix, a trailing slash, a default port, a query, a userinfo) —
// which matters because two spellings of one repository would be two job ids
// for one push, and the idempotency this service promises rests on there being
// one.
func parseRepositoryKey(key string) (repokey.Key, bool) {
	if key == "" {
		return repokey.Key{}, false
	}
	parsed, err := repokey.Parse("https://" + key)
	if err != nil || parsed.String() != key {
		return repokey.Key{}, false
	}
	return parsed, true
}

func splitSegments(s string) []string {
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == '/' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
