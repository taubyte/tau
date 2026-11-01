package forge

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// DefaultMaxDeliveryBody bounds a delivery before any MAC is computed over it.
const DefaultMaxDeliveryBody = 8 << 20

// DefaultTimestampTolerance is the replay window a timestamped scheme allows in
// either direction.
const DefaultTimestampTolerance = 5 * time.Minute

// Scheme is a complete, named verification procedure implemented here: which
// header, which encoding, which bytes, which MAC. It is not a forge: Gitea and
// Forgejo sign exactly as GitHub does, while Bitbucket's SHA-256 MAC in GitHub's
// SHA-1 header is its own scheme. Every scheme binds the body; a forge that only
// replays a shared secret has no push hooks. The scheme a hook was registered
// with is stored beside its secret, so these values are a stable wire format.
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

	// SchemeGogsSHA256 is X-Gogs-Signature: lowercase hex of HMAC-SHA256 over
	// the verbatim body, keyed by the wire string's bytes, with NO "sha256="
	// prefix. Gogs sends nothing else — no X-Hub-Signature-256 beside it — so
	// it cannot name SchemeHubSHA256, and a verifier that accepted either
	// spelling in either header would be two schemes pretending to be one.
	SchemeGogsSHA256 Scheme = "gogs-sha256"
	// SchemePayloadEd25519 is X-Payload-Signature: std base64 of an Ed25519
	// signature over the body followed by the X-Payload-Nonce header's value, made
	// with the forge's own key (sourcehut). The signature proves who sent a delivery,
	// not which subscription it is for: anyone can subscribe a hook whose query
	// aliases a victim's repository into a push's shape. So Wire derives a GraphQL
	// alias from the hook's Secret, the driver writes it into the registered query,
	// and a delivery verifies only if its data carries that alias. The alias rides
	// the body in cleartext, weaker than a MAC key. sourcehut signs no timestamp,
	// so a captured delivery replays the push it already named.
	SchemePayloadEd25519 Scheme = "payload-ed25519"
)

// PayloadKey is the face a driver whose forge signs deliveries with its OWN
// key implements, naming that key. It is a deployment fact, like a host, and it
// is the only thing the driver contributes: the verification is this package's.
// A driver that names SchemePayloadEd25519 without this face is refused at New.
type PayloadKey interface {
	PayloadKey() ed25519.PublicKey
}

// maxPayloadNonce bounds the nonce a stranger's header can make this package
// append to a body before verifying it.
const maxPayloadNonce = 256

// GitHub's SHA-1 sibling (X-Hub-Signature: sha1=...) is deliberately NOT
// implemented. Every forge that sends it also sends the SHA-256 header, so
// implementing it would add only a weaker signature to accept. That is a
// removal, not an omission.

func (s Scheme) implemented() bool {
	switch s {
	case SchemeHubSHA256, SchemeBitbucketSHA256, SchemeStandardWebhooks, SchemeGogsSHA256, SchemePayloadEd25519:
		return true
	}
	return false
}

// secretDomain is the KDF label every scheme's material is derived under. IT IS
// A WIRE FORMAT — the string a forge stores is derived through it — so it is
// versioned, and changing it strands every hook registered before the change
// exactly the way renaming a Scheme would.
const secretDomain = "taubyte/forge/webhook/v1/"

// material is the domain separation: one Secret derives different bytes under
// every scheme, HKDF's info carrying the scheme's name, so a MAC under one is
// not valid under another and a wire string reveals nothing of the Secret.
// Whether one Secret may back several hooks under the same scheme is the
// consumer's policy: the safe habit is one NewSecret per hook.
func (s Scheme) material(secret Secret) ([]byte, error) {
	if secret.Empty() {
		return nil, fmt.Errorf("forge: cannot derive a %s wire secret from an empty Secret", s)
	}
	if secret.tooShort() {
		return nil, fmt.Errorf("forge: a %s secret must be at least %d bytes; a shorter key is brute-forceable offline from one captured delivery, after which every push to the repository can be forged", s, SecretSize)
	}
	if !s.implemented() {
		return nil, fmt.Errorf("forge: scheme %q is not implemented", s)
	}
	return hkdf.Key(sha256.New, secret.Expose(), nil, secretDomain+string(s), SecretSize)
}

// Wire is the string a driver hands the forge at registration, paired with the
// verifier by construction. The spellings differ only in the alphabet and
// prefix the forge insists on; what the MAC is keyed with differs too, which is
// why Wire and key live beside verify.
func (s Scheme) Wire(secret Secret) (string, error) {
	k, err := s.material(secret)
	if err != nil {
		return "", err
	}
	if s == SchemeStandardWebhooks {
		// GitLab's own format check is /\Awhsec_[A-Za-z0-9+\/]{43}=\z/, which
		// is standard base64 of exactly 32 bytes. The derivation produces
		// exactly SecretSize bytes whatever it was given, so the shape now
		// holds BY CONSTRUCTION instead of by demanding the stored secret be
		// that length — which is what used to make this the only scheme that
		// refused a one-byte secret, and it refused it for the wrong reason.
		return "whsec_" + base64.StdEncoding.EncodeToString(k), nil
	}
	if s == SchemePayloadEd25519 {
		// A GraphQL alias: /[_A-Za-z][_0-9A-Za-z]*/, which lowercase hex
		// behind a letter prefix is by construction.
		return "tau_" + hex.EncodeToString(k), nil
	}
	return base64.RawURLEncoding.EncodeToString(k), nil
}

// key is what the MAC is computed with, given the same Secret Wire was given.
func (s Scheme) key(secret Secret) ([]byte, error) {
	switch s {
	case SchemeHubSHA256, SchemeBitbucketSHA256, SchemeGogsSHA256:
		// The forge stores the wire STRING and keys its HMAC with that
		// string's bytes. Keying with the material here would verify nothing
		// any forge ever sends. The two schemes no longer collide because the
		// material behind those strings is derived per scheme.
		wire, err := s.Wire(secret)
		if err != nil {
			return nil, err
		}
		return []byte(wire), nil
	case SchemeStandardWebhooks:
		// The standard keys with the raw bytes BEHIND the whsec_ prefix, which
		// are exactly this scheme's material.
		return s.material(secret)
	default:
		return nil, fmt.Errorf("forge: scheme %q has no MAC", s)
	}
}

func (s Scheme) header() string {
	switch s {
	case SchemeHubSHA256:
		return "X-Hub-Signature-256"
	case SchemeBitbucketSHA256:
		return "X-Hub-Signature"
	case SchemeStandardWebhooks:
		return "webhook-signature"
	case SchemeGogsSHA256:
		return "X-Gogs-Signature"
	case SchemePayloadEd25519:
		return "X-Payload-Signature"
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

// VerifiedBody's fields are unexported, so no package outside this one can
// build a non-empty value: a driver's payload reader cannot be handed bytes
// that did not come through Registry.VerifyPush.
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

// VerifyPush is the only way to turn a delivery into a Push, and needs no
// credential, Session or connection. Its steps:
//
//  1. the hook names a host this registry has a driver for
//  2. that driver can address the hook's key
//  3. the driver can read a payload at all
//  4. the recorded scheme is implemented, and the policy accepts it
//  5. the secret is non-empty and long enough to be a MAC key, checked here for
//     stored hooks too
//  6. the body is within the policy's cap
//  7. the signature verifies, in constant time, against the recorded scheme
//  8. only then does the driver read the body, out of a VerifiedBody
//  9. the hook may only speak for its own repository
//  10. the ref is a refname and the commit a whole object id, not the all-zero
//     deletion sentinel
//
// There is no dev-mode branch: a test that needs a delivery to pass signs it.
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
	if h.Secret.Empty() {
		return Push{}, &VerificationError{Scheme: h.Scheme, Reason: "hook has no secret"}
	}
	if h.Secret.tooShort() {
		return Push{}, &VerificationError{Scheme: h.Scheme, Reason: "hook secret is shorter than a MAC key may be"}
	}
	if len(d.Body) > r.maxDeliveryBody() {
		return Push{}, &VerificationError{Scheme: h.Scheme, Reason: "delivery body exceeds the cap"}
	}
	var signer ed25519.PublicKey
	if h.Scheme == SchemePayloadEd25519 {
		pk, ok := driver.(PayloadKey)
		if !ok {
			return Push{}, &VerificationError{Scheme: h.Scheme, Reason: "the driver names no key the forge signs with"}
		}
		signer = pk.PayloadKey()
	}
	if err := verifyWith(h.Scheme, h.Secret, d, r.now(), r.tolerance(), signer); err != nil {
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
	if reason := refReason(claim.Ref); reason != "" {
		return Push{}, &VerificationError{Scheme: h.Scheme, Reason: reason}
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

// Sign produces the headers a forge would send, for fakes and the conformance
// suite. Verify and Sign are each asserted against externally computed vectors,
// so they cannot be wrong together. id and at are read only by a scheme that
// signs them.
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
	case SchemeGogsSHA256:
		k, err := s.key(secret)
		if err != nil {
			return nil, err
		}
		mac := hmac.New(sha256.New, k)
		mac.Write(body)
		hdr.Set(s.header(), hex.EncodeToString(mac.Sum(nil)))
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
	case SchemePayloadEd25519:
		return nil, fmt.Errorf("forge: %s is signed with the forge's own key and not with a secret; a fake forge signs with SignPayload", s)
	default:
		return nil, fmt.Errorf("forge: scheme %q is not implemented", s)
	}
	return hdr, nil
}

// SignPayload produces the headers a SchemePayloadEd25519 forge sends for a
// body, with that forge's private key. It exists for fakes and fixtures, and it
// is asserted against externally computed vectors like Sign.
func SignPayload(key ed25519.PrivateKey, body []byte, nonce string) http.Header {
	hdr := make(http.Header)
	hdr.Set("X-Payload-Nonce", nonce)
	hdr.Set("X-Payload-Signature", base64.StdEncoding.EncodeToString(ed25519.Sign(key, append(append([]byte{}, body...), nonce...))))
	return hdr
}

func verify(s Scheme, secret Secret, d Delivery, now time.Time, tolerance time.Duration) error {
	return verifyWith(s, secret, d, now, tolerance, nil)
}

// verifyWith is verify given the key the forge signs with, which only
// SchemePayloadEd25519 reads.
func verifyWith(s Scheme, secret Secret, d Delivery, now time.Time, tolerance time.Duration, signer ed25519.PublicKey) error {
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

	case SchemeGogsSHA256:
		// A bare digest and nothing else: a "sha256=" prefix is not how Gogs
		// spells it, so it fails as not-hex rather than being cut off.
		presented := d.Header.Get(s.header())
		if presented == "" {
			return fail("no signature header")
		}
		k, err := s.key(secret)
		if err != nil {
			return fail("secret cannot key this scheme")
		}
		got, err := hex.DecodeString(presented)
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

	case SchemePayloadEd25519:
		if len(signer) != ed25519.PublicKeySize {
			return fail("the driver names no usable key the forge signs with")
		}
		nonce := d.Header.Get("X-Payload-Nonce")
		presented := d.Header.Get(s.header())
		if nonce == "" || presented == "" {
			return fail("delivery is missing X-Payload-Nonce or X-Payload-Signature")
		}
		if len(nonce) > maxPayloadNonce {
			return fail("X-Payload-Nonce is longer than a nonce may be")
		}
		sig, err := base64.StdEncoding.DecodeString(presented)
		if err != nil || len(sig) != ed25519.SignatureSize {
			return fail("signature is not a base64 Ed25519 signature")
		}
		msg := make([]byte, 0, len(d.Body)+len(nonce))
		msg = append(append(msg, d.Body...), nonce...)
		if !ed25519.Verify(signer, msg, sig) {
			return fail("signature does not match")
		}
		// Only now is the body known to be the forge's, and only now does
		// it say which subscription it answers for.
		wire, err := s.Wire(secret)
		if err != nil {
			return fail("secret cannot key this scheme")
		}
		var top struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(d.Body, &top); err != nil {
			return fail("signed body is not a GraphQL response")
		}
		echo, ok := top.Data[wire]
		if !ok || string(echo) != `"Query"` {
			return fail("signed body does not carry this hook's binding; the forge signed a delivery for another subscription")
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

// MaxRefLength bounds a whole refname. Git sets no total length; this bounds what
// a stranger's payload may choose, well above the longest names anyone writes.
const MaxRefLength = 1024

// maxRefComponent is the one length git DOES effectively enforce: a loose ref
// lives at .git/<refname>, so every slash-separated component has to be a legal
// filename, and 255 bytes is the limit on every filesystem git runs on.
const maxRefComponent = 255

// refReason returns why a ref is not one a verified push may carry, or "". It is
// git-check-ref-format's grammar for a full refname, plus: fully qualified
// ("refs/...", since a bare name is ambiguous where it is spent), bounded, and
// valid UTF-8. Any valid UTF-8 above ASCII is allowed. The reason names the rule,
// never the ref.
func refReason(ref string) string {
	switch {
	case ref == "":
		return "the delivery names no ref"
	case len(ref) > MaxRefLength:
		return "the ref is longer than a refname may be"
	case !utf8.ValidString(ref):
		return "the ref is not valid utf-8"
	case !strings.HasPrefix(ref, "refs/"):
		return "the ref is not fully qualified"
	case strings.Contains(ref, ".."):
		return "the ref contains .."
	case strings.Contains(ref, "@{"):
		return "the ref contains @{"
	case strings.HasSuffix(ref, "."):
		return "the ref ends with ."
	}
	for _, r := range ref {
		switch {
		case r < 0x20 || r == 0x7f:
			return "the ref contains a control character"
		case strings.ContainsRune(" ~^:?*[\\", r):
			return "the ref contains a character git forbids in a refname"
		}
	}
	for _, component := range strings.Split(ref, "/") {
		switch {
		case component == "":
			return "the ref has an empty path component"
		case len(component) > maxRefComponent:
			return "the ref has a path component longer than a filename may be"
		case strings.HasPrefix(component, "."):
			return "the ref has a path component beginning with ."
		case strings.HasSuffix(component, ".lock"):
			return "the ref has a path component ending in .lock"
		}
	}
	return ""
}
