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
	//
	// It is ONE defect. It used to point the clone addresses at another
	// repository as well, and the suite caught that second defect and reported
	// it as this one — so the echo the contract actually forbids went unchecked
	// on every driver while a test said it did not. A fault carrying two
	// mistakes proves the suite catches the easier one.
	//
	// SPLITTING IT LEFT THE OTHER HALF WITH NO FAULT AT ALL, which is a hole of
	// the same kind: the clone-address assertion could be deleted from the suite
	// and every test still passed. The half it lost is
	// FaultRepositoryAddressNamesAnother below.
	FaultEchoesKey Fault = "echoes-key"

	// FaultRepositoryAddressNamesAnother serves a repository RECORD whose key is
	// right and whose clone addresses name a different repository on this host.
	// It is FaultEchoesKey's other half, which used to ride along inside it —
	// and the suite reported the pair as an echo, so the half nobody was
	// checking was the address. They are two defects, and two defects need two
	// faults: a fault carrying both proves the suite catches one of them.
	//
	// A forge's answer carries more than one clone address: the project's, its
	// fork parent's, its mirror's. A driver that reads the wrong field files the
	// repository under one name and hands a builder the address of another, and
	// every value it returns looks entirely well formed. The only thing between
	// that driver and a pass is the assertion that a record's key and its
	// addresses name the same repository.
	FaultRepositoryAddressNamesAnother Fault = "repository-address-names-another"

	// FaultTheRecordNamesAnotherRepository serves a record whose KEY names a
	// repository that is not the one it describes — the mistake a driver makes
	// by reading the wrong object out of an answer that carries several: the
	// project and its fork parent, the repository and the one it was
	// transferred from.
	//
	// It is not FaultEchoesKey, and the difference is what each proves. An echo
	// answers with the key it was ASKED about, which is the right answer
	// everywhere except a repository that moved — so it proves only the
	// derivation contract, on the one fixture where argument and answer differ.
	// This names a THIRD repository, so it is wrong on every record the suite
	// reads: the listing's, the created one's, the writable fixture's, the
	// nested one's. Each of those is a separate contract and each needs a fault
	// that reaches it.
	FaultTheRecordNamesAnotherRepository Fault = "the-record-names-another-repository"

	// FaultAsksAnotherHostWhoThisIs sends the identity request to a host this
	// credential is not for, with the credential on it. It is the hardcoded
	// api.github.com left in a driver written by copying one — no redirect, no
	// attacker, just a request that was always addressed wrongly, and a
	// credential spent at whatever answers there.
	FaultAsksAnotherHostWhoThisIs Fault = "asks-another-host-who-this-is"

	// FaultCloneAddressNamesAnother is the same mistake on the OTHER surface
	// that produces a clone address: the CloneAddress face, which is what a
	// builder is actually handed. The two surfaces are separate code in every
	// driver — one reads a field out of an answer, the other builds a URL from a
	// key — so one fault covering both would leave whichever assertion runs
	// second unproven, which is exactly how the record's half came to be
	// unchecked.
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

	// FaultFollowsRedirect takes a forge's redirect to another host and sends
	// the request there with the credential still on it. It is the fault
	// forge.PinnedTransport exists to make impossible, and it is what a driver
	// that attaches its credential on an unpinned transport does: the header is
	// set by a RoundTripper, and a RoundTripper runs on every hop.
	//
	// It follows a hop to ANOTHER HOST and nothing else. A hop to another
	// repository on this host is a different defect against a different
	// defence, and it has its own fault below.
	FaultFollowsRedirect Fault = "follows-redirect"

	// FaultFollowsRedirectOnTheDeploymentCredential follows a hop off this host
	// on the one client NO SESSION CAN REACH: the credential the deployment
	// itself holds, which answers the membership question and nothing else.
	//
	// It is a redirect policy written per call site instead of per client, or
	// written on the client a driver's author was looking at. Every session call
	// refuses the hop; the membership walk does not, and it is walking with a
	// credential that speaks for the whole deployment rather than for one user.
	// A probe that arms one method finds nothing here, which is what it was
	// added for.
	FaultFollowsRedirectOnTheDeploymentCredential Fault = "follows-redirect-on-the-deployment-credential"

	// FaultFollowsSameHostRedirect takes a forge's redirect to ANOTHER
	// REPOSITORY ON THIS HOST and acts on the one the redirect names, while
	// every value it hands back still describes the one the caller asked about.
	//
	// IT IS THE HOP A CREDENTIAL PIN PERMITS BY DESIGN. A pin decides one thing
	// per hop — is this the host the credential is for — and the answer here is
	// yes, so nothing below a driver refuses this and nothing above it can see
	// it. The forge is free to answer 301 to a renamed path, 302 to anything,
	// and 307 to a write with the body replayed; what comes back is a
	// repository the caller never named, with its permissions, its hooks, its
	// deletion. The only place the refusal can live is the driver's own
	// redirect policy.
	//
	// The credential goes on the hop, because the hop is to the host the
	// credential is for. That is not the defect. Acting on somebody else's
	// repository is.
	FaultFollowsSameHostRedirect Fault = "follows-same-host-redirect"

	// ─── the mandatory contract ───

	// FaultBindsAZeroCredential builds a client for a Credential nobody filled
	// in. The registry refuses one before Bind is reached, so this is the half
	// of Driver.Bind's contract no Session can reach — and a driver that binds
	// one is a driver that will act as nobody the moment a configuration value
	// goes missing.
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

	// FaultWritableReadsAsReadOnly is visibility-is-permission's mirror: a
	// repository this credential may push to reads as one it may only see. It is
	// the failure that stops a registration rather than the one that permits it,
	// and a suite that only catches the dangerous direction catches half a
	// driver.
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

	// ─── the optional faces ───

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

	// FaultRemovesWithoutAHandle sends a removal that names no handle instead
	// of refusing it where it stands.
	//
	// AN EMPTY HANDLE ADDRESSES THE COLLECTION. Every forge with a REST shape
	// spells one key as .../keys/<id>, so .../keys/ with nothing after it is
	// every key at once, and a removal of one thing is answered by an answer
	// about all of them. A correct driver refuses it BEFORE anything leaves the
	// process, because a write cannot be undone by checking afterwards — so
	// this fake refuses it too, and this is the fault that makes it stop.
	//
	// It is reachable without a hostile forge: an install that names no id
	// hands a caller "", and a caller that stored what it was handed arrives
	// here with it.
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

	// FaultPushReadsTheBaseRef reads the wrong field out of a verified payload:
	// the branch a change was aimed AT rather than the ref that moved. The
	// delivery is genuine, the signature is genuine, and the build runs on a
	// branch nobody pushed.
	FaultPushReadsTheBaseRef Fault = "push-reads-the-base-ref"

	// FaultPushReadsTheCommitItMovedFrom reads the wrong commit out of a
	// verified payload: the object the ref moved FROM rather than the one it
	// moved to. It is the same class of mistake as reading the base ref, and it
	// has a second victim — a ref DELETION is spelled as an after of all
	// zeroes, so a driver reading before instead reports an ordinary push of
	// the last commit that was there, and tau builds a branch that no longer
	// exists.
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
