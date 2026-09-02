package frontend

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// The headers GitHub sends. Named here so no handler types one.
const (
	eventHeader = "X-GitHub-Event"
	// signatureHeader is HMAC-SHA-256 over the raw body.
	signatureHeader = "X-Hub-Signature-256"
	// legacySignatureHeader is HMAC-SHA-1. It is never read. It is named only
	// so that signature_test.go can send it and prove it does not work.
	legacySignatureHeader = "X-Hub-Signature"

	signaturePrefix = "sha256="
)

var (
	// errNoSecret is a MISCONFIGURATION, not a caller's failure, and it is the
	// most important error in this file.
	//
	// gopkg.in/go-playground/webhooks.v5 — the library v1's webhook handler is
	// built on — SKIPS THE MAC ENTIRELY when the configured secret is empty,
	// so a hook registered without one accepts any body from anyone. That is
	// not a hypothetical for this cloud: services/auth/github.go's p2p and
	// dev-mode registration path hands out the hard-coded secret "mock-secret"
	// rather than an absent one, and the same file's FakeSecret path in patrick
	// substitutes "taubyte_secret" outright. An empty secret reaching this
	// function is a hook this cloud cannot authenticate, and the only safe
	// answer is to fail closed and say so in the log.
	errNoSecret = errors.New("build: hook has no secret to verify with")

	// errBadSignature covers absent, malformed and wrong alike. They are one
	// answer on purpose: distinguishing them tells a caller how close it got.
	errBadSignature = errors.New("build: signature does not verify")
)

// verifySignature checks HMAC-SHA-256 over the exact bytes that were read.
//
// SHA-256 ONLY. GitHub still sends X-Hub-Signature (HMAC-SHA-1) beside it, and
// v1 verifies that one — its route even lists X-Hub-Signature as a REQUIRED
// variable — through a library that reads `signature[5:]` after checking only
// that the header is non-empty. Accepting the weaker header when the stronger
// one is present is a downgrade a caller chooses, and there is no version of
// this cloud that needs it.
//
// The comparison is hmac.Equal: a byte-at-a-time comparison of a MAC leaks,
// through timing, how many leading bytes a guess got right, which turns
// forging a signature into 32 sequential searches instead of one impossible
// one.
func verifySignature(secret string, body []byte, header string) error {
	if secret == "" {
		return errNoSecret
	}

	hexDigest, ok := strings.CutPrefix(header, signaturePrefix)
	if !ok {
		return errBadSignature
	}
	// Decoding before comparing is what keeps a caller from spending this
	// process's time on a header of arbitrary length: the digest is 32 bytes
	// and anything else is refused without hashing anything.
	got, err := hex.DecodeString(hexDigest)
	if err != nil || len(got) != sha256.Size {
		return errBadSignature
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return errBadSignature
	}
	return nil
}
