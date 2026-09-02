// Package fake is an in-memory git forge that is SHAPE-PARAMETERISED, so one
// implementation stands in for four real ones.
//
// It exists for two jobs and is honest about both.
//
// The first is to be the forge every consumer's tests use. services/v2/auth
// reaches its coverage with a libp2p mocknet and an in-memory store and no
// network at all; a forge dependency that needed an HTTP server would break
// that house style, so this one holds its repositories in a map and records
// what it WOULD have sent rather than sending it. Requests() is therefore a
// faithful description and not a capture — for the real drivers it is an
// httptest recorder, and that is where the wire is actually exercised.
//
// The second is to be the thing the conformance suite is proven against. A
// suite that only ever sees a correct driver proves nothing, so this forge can
// be told to be WRONG in specific, named ways (see Fault), and forgetest's own
// tests assert that the suite catches each one. Every fault here is a mistake a
// real driver author can make and a compiler cannot see.
//
// The shapes are not decoration. Nested is GitLab's variable depth,
// HandlesUUID is Bitbucket's hook id, PayloadCarriesCloneURL false is
// Bitbucket's push payload — which names a repository by full_name and leaves
// the driver to supply the host — and the four Schemes are the four real
// signature conventions. Running the suite in every shape is how the interface
// is proven against divergences we are not paying to write drivers for.
package fake

import "github.com/taubyte/tau/pkg/v2/forge"

// Handles is how this forge numbers the resources it owns. It is a shape
// because Bitbucket's webhook id is a UUID where GitHub's, GitLab's and
// Gitea's are integers, and a Handle that a consumer parsed as a number would
// work on three forges and lose data on the fourth.
type Handles uint8

const (
	HandlesInteger Handles = iota
	HandlesUUID
)

// Shape is what makes one in-memory forge stand in for several real ones.
type Shape struct {
	// Host is the repokey host spelling this forge answers for.
	Host string

	// Nested is a forge that addresses a nested owner — GitLab's subgroups. A
	// forge that is not nested REFUSES a deep key rather than flattening it.
	Nested bool

	// Handles is the id form this forge assigns.
	Handles Handles

	// Scheme is what this forge's driver registers new hooks with, and — unless
	// FaultSignsWithAnotherScheme is set — what its deliveries are signed with.
	Scheme forge.Scheme

	// PayloadCarriesCloneURL is whether a push payload names the repository by
	// a clone URL (GitHub, GitLab, Gitea) or by an owner/name pair the driver
	// must combine with its own host (Bitbucket).
	PayloadCarriesCloneURL bool

	// Omit lists capabilities this forge does not answer, so a partial forge
	// and a partial fake are the same thing to a consumer.
	//
	// Only three patterns exist as concrete types, because a Go type either
	// implements an interface or does not and there is no way to decide that at
	// runtime: all of them, none of them, or all but one. New panics on any
	// other combination rather than silently giving you a different forge than
	// you asked for. Omitting CapSignatures or CapMembership is independent of
	// the client-side count, since those two hang off the driver.
	Omit []forge.Capability

	// Faults make this forge WRONG in named ways. See Fault.
	Faults []Fault
}

// The named shapes the conformance suite runs. A consumer testing degradation
// against Minimal is testing exactly the machinery a real partial forge uses.
var (
	GitHubLike = Shape{
		Host:                   "fake.github",
		Scheme:                 forge.SchemeHubSHA256,
		PayloadCarriesCloneURL: true,
	}
	GitLabLike = Shape{
		Host:                   "fake.gitlab",
		Nested:                 true,
		Scheme:                 forge.SchemeStandardWebhooks,
		PayloadCarriesCloneURL: true,
	}
	// GitLabOldLike is an instance older than 19.0, which offers only a
	// replayed shared token. The zero Policy refuses it; a registry that binds
	// this shape has to say AllowUnboundSchemes out loud.
	GitLabOldLike = Shape{
		Host:                   "fake.gitlab18",
		Nested:                 true,
		Scheme:                 forge.SchemeSharedToken,
		PayloadCarriesCloneURL: true,
	}
	GiteaLike = Shape{
		Host:                   "fake.gitea",
		Scheme:                 forge.SchemeHubSHA256,
		PayloadCarriesCloneURL: true,
	}
	BitbucketLike = Shape{
		Host:    "fake.bitbucket",
		Handles: HandlesUUID,
		Scheme:  forge.SchemeBitbucketSHA256,
	}
	// Minimal answers only the three mandatory questions. It is what a host
	// with no API beyond authentication looks like from here, and it is the
	// shape that proves absence is a tested contract rather than an omission.
	Minimal = Shape{
		Host: "fake.bare",
		Omit: []forge.Capability{
			forge.CapList, forge.CapCreate, forge.CapDelete,
			forge.CapDeployKeys, forge.CapPushHooks, forge.CapCloneAddr,
			forge.CapPushProbe, forge.CapSignatures, forge.CapMembership,
		},
	}
)

// Fault is a way this forge is deliberately WRONG. Each one is a mistake a real
// driver author can make that compiles, and each one has a test in forgetest
// asserting the suite catches it. A fault the suite cannot catch is a hole in
// the suite, and the only way to know is to write it down and try.
type Fault string

const (
	// FaultFlattensDeepKey accepts a nested key on a forge that addresses one
	// owner and one name, then acts on "owner-team/widget". This is the
	// failure Driver.Addressable exists to prevent: a request that lands on
	// something other than what the caller named.
	FaultFlattensDeepKey Fault = "flattens-deep-key"

	// FaultVisibilityIsPermission reads a public repository as writable —
	// "I can see it" accepted as "I may push to it".
	FaultVisibilityIsPermission Fault = "visibility-is-permission"

	// FaultOutageIsRefusal answers (AccessNone, nil) while the forge is
	// unreachable, so a network problem arrives at a consumer as a denial and
	// gets acted on.
	FaultOutageIsRefusal Fault = "outage-is-refusal"

	// FaultEchoesKey returns Repository.Key echoed from the argument instead of
	// derived from the clone URL the forge returned, so a rename, a redirect or
	// a case-folding disagreement is invisible.
	FaultEchoesKey Fault = "echoes-key"

	// FaultLeaksTokenInError quotes the credential in an error string, which is
	// the most-copied thing in a log.
	FaultLeaksTokenInError Fault = "leaks-token-in-error"

	// FaultLeaksTokenInURL puts the credential in a query parameter, where
	// every proxy and access log on the path keeps it.
	FaultLeaksTokenInURL Fault = "leaks-token-in-url"

	// FaultManglesWire re-encodes the wire string before handing it to the
	// forge, so the hook registers cleanly and rejects every delivery it will
	// ever receive.
	FaultManglesWire Fault = "mangles-wire"

	// FaultWritableDeployKey installs a machine credential that can push, which
	// is v1's standing bug: a write key issued per repository and never removed.
	FaultWritableDeployKey Fault = "writable-deploy-key"

	// FaultForgetsRemoval answers a removal with nil and removes nothing, so a
	// registrar believes it cleaned up.
	FaultForgetsRemoval Fault = "forgets-removal"

	// FaultMissingIsNone answers AccessNone for a repository that does not
	// exist, collapsing "there is nothing here" into "you may not".
	FaultMissingIsNone Fault = "missing-is-none"

	// FaultSignsWithAnotherScheme names one scheme and signs with another,
	// which is the one webhook mistake this design leaves a driver room to
	// make: the core owns verification, but the driver owns the claim about
	// which procedure its forge uses.
	FaultSignsWithAnotherScheme Fault = "signs-with-another-scheme"
)

func (s Shape) faulty(f Fault) bool {
	for _, have := range s.Faults {
		if have == f {
			return true
		}
	}
	return false
}

func (s Shape) omits(c forge.Capability) bool {
	for _, have := range s.Omit {
		if have == c {
			return true
		}
	}
	return false
}
