package jobs

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"regexp"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// jobID names the build of one (repository, ref, commit): sha256 over a
// domain-separated, length-framed encoding, in lowercase hex (no leading "-").
// Unframed, the triple collides when a field edge moves, and an attacker
// controlling a ref could forge another push's id:
//
//	("github.com/o/n", "refs/heads/x:deadbeef", "c")
//	("github.com/o/n", "refs/heads/x",          "deadbeef:c")
//
// Deterministic so queue dedup and Enqueue idempotency are one mechanism; being
// guessable, it is never a bearer token.
func jobID(key repokey.Key, ref, commitSHA string) string {
	return framed(key.String(), ref, commitSHA)
}

// environmentJobID names the build of one environment of one push: the same framing
// over a fourth field, so it is never the push's own plan job.
func environmentJobID(key repokey.Key, ref, commitSHA, environment string) string {
	return framed(key.String(), ref, commitSHA, environment)
}

func framed(fields ...string) string {
	h := sha256.New()
	h.Write([]byte(jobIDDomain))
	for _, field := range fields {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		h.Write(length[:])
		h.Write([]byte(field))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// jobIDDomain separates this hash from every other sha256 in the tree, so that
// a digest computed for some other purpose over the same bytes is a different
// value. The NUL is part of it: it cannot occur in any of the three fields, so
// the prefix cannot be absorbed into the first one.
const jobIDDomain = "tau.build.v2/job\x00"

// refPattern is the last check before a stranger's ref becomes part of a hash and
// a key segment; eligibility policy is ingress's.
var refPattern = regexp.MustCompile(`^refs/heads/[A-Za-z0-9._/-]{1,255}$`)

// shaPattern is a full 40-character lowercase git object name. v1 accepted
// whatever the payload said.
var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// validRef refuses what git would refuse to check out, plus ".." and empty
// segments, though seg() already neutralizes those.
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

// parseRepositoryKey accepts only repokey's canonical form: parsing the key as an
// https URL must yield itself. Two spellings of one repository would be two job
// ids for one push.
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
