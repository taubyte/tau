// Package fake is an in-memory git forge, parameterised by Shape so one
// implementation stands in for several real ones. It is the forge consumers'
// tests use, holding repositories in a map and recording what it would have
// sent; and it is what the conformance suite is proven against, wrong in named
// ways (see Fault) that forgetest asserts the suite catches.
package fake

import "github.com/taubyte/tau/pkg/forge"

// Handles is a shape because Bitbucket's webhook id is a UUID where the others'
// are integers.
type Handles uint8

const (
	HandlesInteger Handles = iota
	HandlesUUID
)

type Shape struct {
	Host string

	// Nested is a forge that addresses a nested owner — GitLab's subgroups. A
	// forge that is not nested REFUSES a deep key rather than flattening it.
	Nested bool

	Handles Handles

	// Scheme is what this forge's driver registers new hooks with, and — unless
	// FaultSignsWithAnotherScheme is set — what its deliveries are signed with.
	Scheme forge.Scheme

	// PayloadCarriesCloneURL is whether a push payload names the repository by
	// a clone URL (GitHub, GitLab, Gitea) or by an owner/name pair the driver
	// must combine with its own host (Bitbucket).
	PayloadCarriesCloneURL bool

	// Omit lists capabilities this forge does not answer. A Go type implements an
	// interface or not, so only all, none or all but one of the client faces exist,
	// and New panics on any other combination. CapSignatures and CapMembership hang
	// off the driver.
	Omit []forge.Capability

	Faults []Fault
}

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
	// Minimal answers only the three mandatory questions, proving absence is a
	// tested contract.
	Minimal = Shape{
		Host: "fake.bare",
		Omit: []forge.Capability{
			forge.CapList, forge.CapCreate, forge.CapDelete,
			forge.CapDeployKeys, forge.CapPushHooks, forge.CapCloneAddr,
			forge.CapPushProbe, forge.CapSignatures, forge.CapMembership,
		},
	}
)

// Fault is a way this forge is deliberately wrong: a mistake a driver author can
// make that compiles. forgetest asserts the suite catches each.
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
	// derived from the clone URL the forge returned. It is one defect: a fault
	// carrying two proves the suite catches the easier one.
	FaultEchoesKey Fault = "echoes-key"

	// FaultRepositoryAddressNamesAnother serves a record whose key is right and
	// whose clone addresses name another repository on this host, as a driver
	// reading the fork parent's field would.
	FaultRepositoryAddressNamesAnother Fault = "repository-address-names-another"

	// FaultTheRecordNamesAnotherRepository serves a record whose key names a third
	// repository: wrong on every record the suite reads, where an echo is wrong
	// only on Moved.
	FaultTheRecordNamesAnotherRepository Fault = "the-record-names-another-repository"

	// FaultAsksAnotherHostWhoThisIs sends the identity request to a host this
	// credential is not for, with the credential on it. It is the hardcoded
	// api.github.com left in a driver written by copying one — no redirect, no
	// attacker, just a request that was always addressed wrongly, and a
	// credential spent at whatever answers there.
	FaultAsksAnotherHostWhoThisIs Fault = "asks-another-host-who-this-is"

	// FaultCloneAddressNamesAnother is the same mistake on the CloneAddress face,
	// separate code in every driver.
	FaultCloneAddressNamesAnother Fault = "clone-address-names-another"

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

	// FaultFollowsRedirect sends a forge's redirect to another host with the
	// credential still on it, as a driver attaching its credential on an unpinned
	// transport does. A same-host hop has its own fault.
	FaultFollowsRedirect Fault = "follows-redirect"

	// FaultFollowsRedirectOnTheDeploymentCredential follows an off-host hop on the
	// deployment's own client, which answers membership: a redirect policy set per
	// call site rather than per client.
	FaultFollowsRedirectOnTheDeploymentCredential Fault = "follows-redirect-on-the-deployment-credential"

	// FaultFollowsSameHostRedirect acts on the repository a same-host redirect
	// names, while every value returned describes the one asked about. A credential
	// pin permits the hop by design, so only the driver's redirect policy can
	// refuse it.
	FaultFollowsSameHostRedirect Fault = "follows-same-host-redirect"

	// FaultBindsAZeroCredential builds a client for an unfilled Credential: the half
	// of Bind's contract no Session reaches, since the registry refuses one first.
	FaultBindsAZeroCredential Fault = "binds-a-zero-credential"

	// FaultRefusesAnonymousAtTheDoor refuses Anonymous() at Bind rather than at
	// the question. Reading a public repository with no credential is a real
	// thing to want, and the refusal that matters is the authorization one,
	// which forge.Session already owns.
	FaultRefusesAnonymousAtTheDoor Fault = "refuses-anonymous-at-the-door"

	// FaultWhoamiHasNoLogin answers an identity with no login. Whoami is the
	// only honest token check a forge offers, and an answer with nothing in it
	// is a token check that passes for a token nobody honoured.
	FaultWhoamiHasNoLogin Fault = "whoami-has-no-login"

	// FaultARefusedCredentialIsAccepted honours a credential this forge revoked,
	// which is the shape of a driver that reads a 401 body for a message instead
	// of reading the status.
	FaultARefusedCredentialIsAccepted Fault = "a-refused-credential-is-accepted"

	// FaultARefusedCredentialIsNoAccess turns a refused credential into
	// (AccessNone, nil): a statement about the CREDENTIAL delivered as a verdict
	// on the repository, which a caller will act on by revoking somebody.
	FaultARefusedCredentialIsNoAccess Fault = "a-refused-credential-is-no-access"

	// FaultAnswersBesideItsError returns a filled value along with its error, so
	// a caller that reads the value first — which is every caller that forgets a
	// check — acts on an answer no forge gave.
	FaultAnswersBesideItsError Fault = "answers-beside-its-error"

	// FaultWritableReadsAsReadOnly is visibility-is-permission's mirror: it stops a
	// registration rather than permitting one.
	FaultWritableReadsAsReadOnly Fault = "writable-reads-as-read-only"

	// FaultAHiddenRepositoryIsARawError answers a repository this credential
	// holds nothing on with a plain error. The contract allows exactly two
	// answers there — AccessNone or *NotFoundError — because a caller has to be
	// able to tell "not yours" from "could not ask".
	FaultAHiddenRepositoryIsARawError Fault = "a-hidden-repository-is-a-raw-error"

	// FaultAHiddenRepositoryIsReadable reads a repository the credential holds
	// nothing on as AccessRead: the API answered, so the driver assumed the
	// caller may see it.
	FaultAHiddenRepositoryIsReadable Fault = "a-hidden-repository-is-readable"

	// FaultAMissingRepositoryIsNoError answers a repository that does not exist
	// with a zero record and no error, so a caller files a repository with no
	// name, no branch and no clone address.
	FaultAMissingRepositoryIsNoError Fault = "a-missing-repository-is-no-error"

	// FaultTheRecordHasNoDefaultBranch serves a record with no default branch. A
	// builder handed one has nothing to check out and no way to say so.
	FaultTheRecordHasNoDefaultBranch Fault = "the-record-has-no-default-branch"

	// FaultTheRecordsAddressCarriesTheCredential splices the credential into the
	// clone addresses a repository RECORD carries. That address is written into
	// .git/config, into process arguments and into every error that quotes a
	// remote — which is v1's standing way of spending a token.
	FaultTheRecordsAddressCarriesTheCredential Fault = "the-records-address-carries-the-credential"

	// FaultCloneAddressCarriesTheCredential is the same mistake on the surface a
	// builder is actually handed, which is separate code in every driver.
	FaultCloneAddressCarriesTheCredential Fault = "clone-address-carries-the-credential"

	// FaultAnOutageIsNotFound reports an unreachable forge as "no such
	// repository", which is a definitive answer manufactured out of a network
	// failure.
	FaultAnOutageIsNotFound Fault = "an-outage-is-not-found"

	// FaultAnswersFromCacheWhileUnreachable answers the questions that are not
	// the authorization one — who this credential is, what a repository is, what
	// it can reach, whether somebody is a member — while the forge cannot be
	// reached. An outage becomes a stale answer nobody can date. (Access has its
	// own fault, because there an outage becomes a REFUSAL, which is worse.)
	FaultAnswersFromCacheWhileUnreachable Fault = "answers-from-cache-while-unreachable"

	// FaultAsksBeforeRefusingADepth makes a request before refusing a key it
	// cannot address — a driver checking with the forge whether "acme/team" and
	// "widget" happen to name something, instead of refusing a shape it cannot
	// express.
	FaultAsksBeforeRefusingADepth Fault = "asks-before-refusing-a-depth"

	// FaultANestingForgeRefusesADepth refuses a nested owner on a forge that
	// nests, which is GitLab's whole subgroup story reported as an unaddressable
	// key.
	FaultANestingForgeRefusesADepth Fault = "a-nesting-forge-refuses-a-depth"

	// FaultListsNothing yields nothing from a listing this credential can
	// certainly reach, so a consumer walking a namespace finds it empty.
	FaultListsNothing Fault = "lists-nothing"

	// FaultListOmitsWhatCanBeWritten yields everything but the repository this
	// credential may write. An absence proves nothing in general — that is the
	// contract — but a listing that omits precisely what a caller can act on is
	// a listing nobody can use.
	FaultListOmitsWhatCanBeWritten Fault = "list-omits-what-can-be-written"

	// FaultListIgnoresAStoppedYield keeps going after yield returned false. The
	// caller that stopped has already decided; a forge that pages on is a walk
	// nobody can bound.
	FaultListIgnoresAStoppedYield Fault = "list-ignores-a-stopped-yield"

	// FaultCreatesTheSameRepositoryTwice answers a creation that collides by
	// overwriting, which turns "make me a repository" into "empty somebody's".
	FaultCreatesTheSameRepositoryTwice Fault = "creates-the-same-repository-twice"

	// FaultCreatesInAnyNamespace creates in a namespace this credential may not
	// write, rather than refusing the key it was handed.
	FaultCreatesInAnyNamespace Fault = "creates-in-any-namespace"

	// FaultDeletesWithoutAdmin deletes for a credential that holds less than
	// admin. AccessAdmin is a real rung and deletion is what consumes it.
	FaultDeletesWithoutAdmin Fault = "deletes-without-admin"

	// FaultDeleteForgetsTheRepository answers a deletion with nil and deletes
	// nothing, which is FaultForgetsRemoval's shape on the surface where the
	// caller has no handle to re-check.
	FaultDeleteForgetsTheRepository Fault = "delete-forgets-the-repository"

	// FaultDeployKeyHandleIsEmpty answers an installation with an empty handle,
	// which is a machine credential nothing can ever remove. forge.Session
	// refuses an empty HOOK handle for exactly this reason and does not refuse
	// this one, so the suite is what stands under it.
	FaultDeployKeyHandleIsEmpty Fault = "deploy-key-handle-is-empty"

	// FaultDeployKeyHandleNamesNothing answers with a handle the forge does not
	// know, so the key is installed and unremovable under a name nobody holds.
	FaultDeployKeyHandleNamesNothing Fault = "deploy-key-handle-names-nothing"

	// FaultRemovingAnUnknownDeployKeySucceeds answers "removed" for a key that
	// was never there, so a registrar cleaning up cannot tell a removal from a
	// typo.
	FaultRemovingAnUnknownDeployKeySucceeds Fault = "removing-an-unknown-deploy-key-succeeds"

	// FaultHookHandleNamesNothing is the same mistake for a webhook: a handle
	// the forge does not hold, so nothing can ever find or remove that hook.
	FaultHookHandleNamesNothing Fault = "hook-handle-names-nothing"

	// FaultRemovingAnUnknownHookSucceeds answers "removed" for a hook that was
	// never there.
	FaultRemovingAnUnknownHookSucceeds Fault = "removing-an-unknown-hook-succeeds"

	// FaultRemovesWithoutAHandle sends a removal naming no handle instead of
	// refusing it: an empty handle addresses the collection, .../keys/ being every
	// key. An install that names no id hands a caller one.
	FaultRemovesWithoutAHandle Fault = "removes-without-a-handle"

	// FaultHTTPSAddressHasNoUsername hands back an https clone address with no
	// account to present a token as, which is a clone that will prompt.
	FaultHTTPSAddressHasNoUsername Fault = "https-address-has-no-username"

	// FaultSSHAddressHasNoAccount hands back an ssh clone address with no
	// account before the '@'. It is a host fact and not a caller's choice.
	FaultSSHAddressHasNoAccount Fault = "ssh-address-has-no-account"

	// FaultPushProbeSaysNoToAKeyThatMay reports a git credential that may push
	// as one that may not, which stops a registration that should have run.
	FaultPushProbeSaysNoToAKeyThatMay Fault = "push-probe-says-no-to-a-key-that-may"

	// FaultPushProbeSaysYesToAKeyThatMayNot is the dangerous direction of the
	// same mistake.
	FaultPushProbeSaysYesToAKeyThatMayNot Fault = "push-probe-says-yes-to-a-key-that-may-not"

	// FaultPushProbeAnswersForAMissingRepository answers false for a repository
	// that does not exist, so "there is nothing there" arrives as "you may not
	// push".
	FaultPushProbeAnswersForAMissingRepository Fault = "push-probe-answers-for-a-missing-repository"

	// FaultAMemberReadsAsANonMember answers false for somebody who is a member.
	// (false, nil) is the one definitive negative membership has, and a caller
	// acts on it.
	FaultAMemberReadsAsANonMember Fault = "a-member-reads-as-a-non-member"

	// FaultANonMemberReadsAsAMember is the same mistake in the direction that
	// grants.
	FaultANonMemberReadsAsAMember Fault = "a-non-member-reads-as-a-member"

	// FaultPushReadsTheBaseRef reads the branch a change was aimed at rather than
	// the ref that moved: the signature is genuine and the build is not.
	FaultPushReadsTheBaseRef Fault = "push-reads-the-base-ref"

	// FaultPushReadsTheCommitItMovedFrom reads the commit the ref moved from. A
	// deletion's after is all zeroes, so reading before reports an ordinary push of
	// a branch that no longer exists.
	FaultPushReadsTheCommitItMovedFrom Fault = "push-reads-the-commit-it-moved-from"

	// FaultAnyDeliveryReadsAsAPush ignores what KIND of event arrived and reads
	// every delivery as a push. A forge sends pings, tag creations and
	// membership changes down the same hook; a driver that does not look turns
	// each of them into a build of whatever the payload happens to leave empty.
	FaultAnyDeliveryReadsAsAPush Fault = "any-delivery-reads-as-a-push"

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
