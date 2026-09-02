package forge

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// DefaultMaxDeliveryBody bounds a delivery before any MAC is computed over it.
const DefaultMaxDeliveryBody = 8 << 20

// DefaultTimestampTolerance is the replay window a timestamped scheme allows in
// either direction.
const DefaultTimestampTolerance = 5 * time.Minute

// Scheme is a COMPLETE, NAMED VERIFICATION PROCEDURE, implemented once here:
// which header, which encoding, exactly which bytes are covered, which MAC, and
// what a match means.
//
// A SCHEME IS NOT A FORGE, and that is the whole reason it is a separate
// vocabulary. Gitea and Forgejo sign X-Hub-Signature-256 byte for byte the way
// GitHub does, so all three name SchemeHubSHA256 and there is one
// implementation between them. Bitbucket puts a SHA-256 MAC in X-Hub-Signature
// — the header GitHub uses for SHA-1 — so it is a different scheme even though
// the header name repeats, and a verifier dispatching on header names would
// accept SHA-1 where SHA-256 was meant. And one forge has two: GitLab signs
// Standard Webhooks when a signing token is set (19.0 and later) and otherwise
// replays a plaintext token, so "the host is gitlab.com" does not determine the
// procedure.
//
// THE SCHEME A HOOK WAS REGISTERED WITH IS STORED BESIDE ITS SECRET AND PINNED
// AT VERIFICATION, so an instance upgraded later keeps verifying its older
// hooks correctly. Scheme values are therefore written into a store: they are a
// wire format and are stable, and renaming one strands every hook registered
// before the rename.
type Scheme string

const (
	// SchemeHubSHA256 is X-Hub-Signature-256: "sha256=" + lowercase hex of
	// HMAC-SHA256 over the verbatim body, keyed by the WIRE STRING'S BYTES.
	// GitHub, Gitea, Forgejo, Codeberg. It is named after the header family
	// four forges share and not after the vendor that invented it, because
	// the name is what a fifth forge will look for.
	SchemeHubSHA256 Scheme = "hub-sha256"

	// SchemeBitbucketSHA256 is X-Hub-Signature: the same construction as
	// SchemeHubSHA256 in a different header — which is exactly why it is a
	// separate scheme rather than a header alias.
	SchemeBitbucketSHA256 Scheme = "bitbucket-sha256"

	// SchemeStandardWebhooks is webhook-signature: one or more space-separated
	// "v1,<std base64 HMAC-SHA256>" over "<webhook-id>.<webhook-timestamp>.<body>",
	// keyed by the RAW BYTES BEHIND the wire string's "whsec_" prefix — not by
	// the wire string itself. Matching ANY of the presented signatures is
	// correct, because the standard rotates secrets by sending two.
	// GitLab 19.0 and later.
	SchemeStandardWebhooks Scheme = "standard-webhooks"

	// SchemeSharedToken is X-Gitlab-Token: the wire string replayed verbatim,
	// compared for equality. It binds nothing — not the body, not a timestamp
	// — and it hands the secret itself to the endpoint on every delivery, so
	// anything that logs a request header logs the credential. GitLab before
	// 19.0 offers only this, which is why it exists here and why the zero
	// Policy refuses it.
	SchemeSharedToken Scheme = "shared-token"
)

// GitHub's SHA-1 sibling (X-Hub-Signature: sha1=...) is deliberately NOT
// implemented. Every forge that sends it also sends the SHA-256 header, so
// implementing it would add only a weaker signature to accept. That is a
// removal, not an omission.

func (s Scheme) implemented() bool {
	switch s {
	case SchemeHubSHA256, SchemeBitbucketSHA256, SchemeStandardWebhooks, SchemeSharedToken:
		return true
	}
	return false
}

// BindsBody reports whether a match proves the body is the one that was signed.
// It is false only for SchemeSharedToken. Policy is written against this, so a
// deployment refuses weak schemes in ONE place instead of per hook — and so
// that a scheme added later is classified rather than grandfathered.
func (s Scheme) BindsBody() bool { return s != SchemeSharedToken && s.implemented() }

// Wire is the string a driver hands the forge at registration. IT IS PAIRED
// WITH THE VERIFIER BY CONSTRUCTION: split them and you get a hook that
// registers cleanly and rejects every delivery it ever receives, which is a
// failure that only shows up in production and only as silence.
//
// The two derivations differ in what the MAC is keyed with — the wire string's
// bytes for the hub schemes, the decoded bytes for Standard Webhooks — which is
// precisely why both live in one file beside verify.
func (s Scheme) Wire(secret Secret) (string, error) {
	if secret.Empty() {
		return "", fmt.Errorf("forge: cannot derive a %s wire secret from an empty Secret", s)
	}
	switch s {
	case SchemeHubSHA256, SchemeBitbucketSHA256, SchemeSharedToken:
		return base64.RawURLEncoding.EncodeToString(secret.Expose()), nil
	case SchemeStandardWebhooks:
		// GitLab's own format check is /\Awhsec_[A-Za-z0-9+\/]{43}=\z/, which
		// is standard base64 of exactly 32 bytes.
		if len(secret.Expose()) != SecretSize {
			return "", fmt.Errorf("forge: %s needs a %d-byte secret", s, SecretSize)
		}
		return "whsec_" + base64.StdEncoding.EncodeToString(secret.Expose()), nil
	default:
		return "", fmt.Errorf("forge: scheme %q is not implemented", s)
	}
}

// key is what the MAC is computed with, given the same Secret Wire was given.
func (s Scheme) key(secret Secret) ([]byte, error) {
	wire, err := s.Wire(secret)
	if err != nil {
		return nil, err
	}
	switch s {
	case SchemeHubSHA256, SchemeBitbucketSHA256:
		// The forge stores the wire STRING and keys its HMAC with that
		// string's bytes. Keying with the raw secret here would verify
		// nothing any forge ever sends.
		return []byte(wire), nil
	case SchemeStandardWebhooks:
		return secret.Expose(), nil
	default:
		return nil, fmt.Errorf("forge: scheme %q has no MAC", s)
	}
}

// header is the one this scheme reads and writes.
func (s Scheme) header() string {
	switch s {
	case SchemeHubSHA256:
		return "X-Hub-Signature-256"
	case SchemeBitbucketSHA256:
		return "X-Hub-Signature"
	case SchemeStandardWebhooks:
		return "webhook-signature"
	case SchemeSharedToken:
		return "X-Gitlab-Token"
	}
	return ""
}

// Delivery is one inbound HTTP request captured BEFORE anything parsed it. Body
// is verbatim: re-serialising a decoded body changes every digest, so a
// receiver that unmarshals first has already lost the thing it needs to check.
//
// A receiving handler must bound its read at Policy.MaxDeliveryBody before it
// builds one of these; VerifyPush only ever sees what was already read.
type Delivery struct {
	Header http.Header
	Body   []byte
}

// Hook is everything needed to verify a delivery claiming to come from it, and
// nothing else. A consumer stores it whole; storing the secret without the
// scheme is the shape that lets a hook created under one procedure be checked
// under another.
type Hook struct {
	Key    repokey.Key
	Handle Handle
	Scheme Scheme
	Secret Secret
}

// VerifiedBody IS THE ENFORCEMENT MECHANISM, NOT A LABEL. Its fields are
// unexported, so no package outside this one can build a non-empty value: a
// driver's payload reader takes one and therefore cannot be handed bytes that
// did not come through Registry.VerifyPush. A consumer cannot "just parse the
// payload", and a driver cannot verify optionally.
type VerifiedBody struct {
	body   []byte
	scheme Scheme
}

func (v VerifiedBody) Bytes() []byte  { return v.body }
func (v VerifiedBody) Scheme() Scheme { return v.scheme }

// PushClaim is what a driver READ OUT OF a verified payload. It is a claim
// because a delivery carries a stranger's bytes: a signature proves who sent
// them, not that what they say is about the repository this hook belongs to.
type PushClaim struct {
	Repository repokey.Key
	Ref        string
	Commit     string
}

// Push is what a delivery MEANS: three facts, and structurally no room for a
// fourth. Everything else a payload carries — the numeric repository id, the
// changed-file lists, the pusher, the timestamps — is a stranger's bytes a
// forge relayed, and every surviving field is one more thing something later
// trusts. Key is the HOOK's key, never the payload's.
type Push struct {
	Key    repokey.Key
	Ref    string
	Commit string
}

// VerifyPush is the ONLY way to turn a delivery into a Push, and it is the
// whole of a webhook-receiving process's forge surface: no credential, no
// Session, no connection, so that process cannot degrade into making an API
// call. Every step is this package's:
//
//  1. the hook names a host this registry has a driver for
//  2. that driver can address the hook's key
//  3. the driver can read a payload at all
//  4. the recorded scheme is implemented, and the policy accepts it
//  5. the secret is non-empty — "no secret configured" never reaches a compare
//  6. the body is within the policy's cap
//  7. the signature verifies, in constant time, against the RECORDED scheme
//  8. only then does the driver read the body, out of a VerifiedBody
//  9. THE HOOK MAY ONLY SPEAK FOR ITS OWN REPOSITORY. Against an honest forge
//     this is redundant, which is exactly why it is here: one comparison turns
//     a forge bug, a driver bug or a confused-deputy delivery into a rejection
//     instead of a build of somebody else's code.
//  10. the ref is named and the commit is a whole object id that is not the
//     all-zero deletion sentinel
//
// THERE IS NO DEV-MODE BRANCH. v1's receiver skips verification entirely when a
// dev flag and a fixed fake secret line up, so the bypass is one
// misconfiguration away from production. A test that needs a delivery to pass
// signs it; Sign is exported for exactly that.
func (r *Registry) VerifyPush(h Hook, d Delivery) (Push, error) {
	driver, err := r.Driver(h.Key.Host)
	if err != nil {
		return Push{}, err
	}
	if err := driver.Addressable(h.Key); err != nil {
		return Push{}, &UnaddressableError{Host: h.Key.Host, Key: h.Key, Reason: err.Error()}
	}
	sig, ok := driver.(Signatures)
	if !ok {
		return Push{}, &UnsupportedError{Host: h.Key.Host, Capability: CapSignatures}
	}
	if !h.Scheme.implemented() {
		return Push{}, &VerificationError{Scheme: h.Scheme, Reason: "scheme is not implemented"}
	}
	if !h.Scheme.BindsBody() && !r.policy.AllowUnboundSchemes {
		return Push{}, &VerificationError{Scheme: h.Scheme, Reason: "scheme does not bind the body and the policy does not accept it"}
	}
	if h.Secret.Empty() {
		return Push{}, &VerificationError{Scheme: h.Scheme, Reason: "hook has no secret"}
	}
	if len(d.Body) > r.maxDeliveryBody() {
		return Push{}, &VerificationError{Scheme: h.Scheme, Reason: "delivery body exceeds the cap"}
	}
	if err := verify(h.Scheme, h.Secret, d, r.now(), r.tolerance()); err != nil {
		return Push{}, err
	}

	claim, err := sig.Push(VerifiedBody{body: d.Body, scheme: h.Scheme}, d.Header)
	if err != nil {
		return Push{}, err
	}
	if claim.Repository != h.Key {
		return Push{}, &VerificationError{
			Scheme: h.Scheme,
			Reason: fmt.Sprintf("delivery claims repository %q on a hook registered for %q", claim.Repository, h.Key),
		}
	}
	if claim.Ref == "" {
		return Push{}, &VerificationError{Scheme: h.Scheme, Reason: "delivery names no ref"}
	}
	if isZeroOID(claim.Commit) {
		return Push{}, ErrRefDeleted
	}
	if !isObjectID(claim.Commit) {
		return Push{}, &VerificationError{Scheme: h.Scheme, Reason: "delivery names no commit"}
	}
	return Push{Key: h.Key, Ref: claim.Ref, Commit: claim.Commit}, nil
}

func (r *Registry) now() time.Time {
	if r.policy.Now != nil {
		return r.policy.Now()
	}
	return time.Now()
}

func (r *Registry) tolerance() time.Duration {
	if r.policy.Tolerance > 0 {
		return r.policy.Tolerance
	}
	return DefaultTimestampTolerance
}

func (r *Registry) maxDeliveryBody() int {
	if r.policy.MaxDeliveryBody > 0 {
		return r.policy.MaxDeliveryBody
	}
	return DefaultMaxDeliveryBody
}

// Sign produces the headers a forge would send for a body under a scheme. It
// exists so a fake forge and the conformance suite can produce REAL deliveries
// — never so verification can be tested by round-tripping this package's own
// output. The scheme tests assert verify against vectors computed OUTSIDE this
// package, and Sign is asserted against those same vectors, so the two
// directions cannot be wrong together and still pass.
//
// id and at are read only by a scheme that signs them; the others ignore both.
func Sign(s Scheme, secret Secret, body []byte, id string, at time.Time) (http.Header, error) {
	hdr := make(http.Header)
	switch s {
	case SchemeHubSHA256, SchemeBitbucketSHA256:
		k, err := s.key(secret)
		if err != nil {
			return nil, err
		}
		mac := hmac.New(sha256.New, k)
		mac.Write(body)
		hdr.Set(s.header(), "sha256="+hex.EncodeToString(mac.Sum(nil)))
	case SchemeStandardWebhooks:
		k, err := s.key(secret)
		if err != nil {
			return nil, err
		}
		ts := strconv.FormatInt(at.Unix(), 10)
		mac := hmac.New(sha256.New, k)
		mac.Write([]byte(id + "." + ts + "." + string(body)))
		hdr.Set("webhook-id", id)
		hdr.Set("webhook-timestamp", ts)
		hdr.Set("webhook-signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	case SchemeSharedToken:
		wire, err := s.Wire(secret)
		if err != nil {
			return nil, err
		}
		hdr.Set(s.header(), wire)
	default:
		return nil, fmt.Errorf("forge: scheme %q is not implemented", s)
	}
	return hdr, nil
}

func verify(s Scheme, secret Secret, d Delivery, now time.Time, tolerance time.Duration) error {
	fail := func(reason string) error { return &VerificationError{Scheme: s, Reason: reason} }

	switch s {
	case SchemeHubSHA256, SchemeBitbucketSHA256:
		presented := d.Header.Get(s.header())
		if presented == "" {
			return fail("no signature header")
		}
		// A prefix is NOT stripped unconditionally: Gitea's own
		// X-Gitea-Signature carries a bare hex digest while its
		// X-Hub-Signature-256 carries the prefix, so a verifier that cuts
		// blindly on "=" reads one of them wrong.
		digest, ok := strings.CutPrefix(presented, "sha256=")
		if !ok {
			return fail("signature is not sha256-prefixed")
		}
		k, err := s.key(secret)
		if err != nil {
			return fail("secret cannot key this scheme")
		}
		got, err := hex.DecodeString(digest)
		if err != nil {
			return fail("signature is not hex")
		}
		mac := hmac.New(sha256.New, k)
		mac.Write(d.Body)
		if !hmac.Equal(got, mac.Sum(nil)) {
			return fail("signature does not match")
		}
		return nil

	case SchemeStandardWebhooks:
		id := d.Header.Get("webhook-id")
		ts := d.Header.Get("webhook-timestamp")
		sigs := d.Header.Get("webhook-signature")
		if id == "" || ts == "" || sigs == "" {
			return fail("delivery is missing webhook-id, webhook-timestamp or webhook-signature")
		}
		secs, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			return fail("webhook-timestamp is not a unix time")
		}
		delta := now.Sub(time.Unix(secs, 0))
		if delta < 0 {
			delta = -delta
		}
		if delta > tolerance {
			return fail("webhook-timestamp is outside the replay window")
		}
		k, err := s.key(secret)
		if err != nil {
			return fail("secret cannot key this scheme")
		}
		mac := hmac.New(sha256.New, k)
		mac.Write([]byte(id + "." + ts + "." + string(d.Body)))
		want := mac.Sum(nil)
		// The header MAY carry several space-separated signatures — that is
		// how the standard rotates a secret — and matching any one is correct.
		for _, candidate := range strings.Fields(sigs) {
			raw, ok := strings.CutPrefix(candidate, "v1,")
			if !ok {
				continue
			}
			got, err := base64.StdEncoding.DecodeString(raw)
			if err != nil {
				continue
			}
			if hmac.Equal(got, want) {
				return nil
			}
		}
		return fail("no signature matched")

	case SchemeSharedToken:
		presented := d.Header.Get(s.header())
		if presented == "" {
			return fail("no token header")
		}
		wire, err := s.Wire(secret)
		if err != nil {
			return fail("secret cannot key this scheme")
		}
		if subtle.ConstantTimeCompare([]byte(presented), []byte(wire)) != 1 {
			return fail("token does not match")
		}
		return nil
	}
	return fail("scheme is not implemented")
}

// isZeroOID is git's deletion sentinel — an object id of all zeros, at whatever
// width the repository's hash is.
func isZeroOID(s string) bool {
	if s == "" {
		return false
	}
	return strings.Trim(s, "0") == ""
}

// isObjectID accepts a WHOLE object id and nothing shorter: 40 hex characters
// for SHA-1, 64 for SHA-256. An abbreviation is not accepted because nothing
// here can resolve one, and a partial id that later matches two objects is a
// build of the wrong commit.
func isObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
