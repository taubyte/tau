package forgetest

import (
	"testing"
	"time"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// A proof is one subject wrong in a named way and the exact complaints the suite
// makes about it, each naming one assertion by a fragment of its message. stops
// lists the steps the subject prevents, kept apart so a claim cannot pass as a
// step.
type proof struct {
	name string

	// subject builds with a given set of faults, so the ledger can rebuild it
	// without one and ask what stopped being said.
	subject func(t *testing.T, faults []fake.Fault) Subject

	says  []string
	stops []string

	// faults is written down so every fault must have a proof in front of it, the
	// inverse of every assertion having a fault behind it.
	faults []fake.Fault
}

func allProofs() []proof {
	var out []proof
	out = append(out, faultProofs()...)
	out = append(out, subjectProofs()...)
	return out
}

func faultProofs() []proof {
	var out []proof
	add := func(name string, sh fake.Shape, faults []fake.Fault, says ...string) {
		out = append(out, proof{
			name: name,
			// Faults come from the argument, so a subject without one is the same subject
			// built the same way.
			subject: func(t *testing.T, fs []fake.Fault) Subject {
				shape := sh
				for _, f := range fs {
					shape = faulty(shape, f)
				}
				return subjectFor(t, "broken", shape)
			},
			says:   says,
			faults: faults,
		})
	}

	add("binds-a-zero-credential", fake.GitHubLike, []fake.Fault{fake.FaultBindsAZeroCredential},
		"Driver.Bind built a client")
	add("refuses-anonymous-at-the-door", fake.GitHubLike, []fake.Fault{fake.FaultRefusesAnonymousAtTheDoor},
		"Anonymous() must bind")
	add("flattens-deep-key", fake.GitHubLike, []fake.Fault{fake.FaultFlattensDeepKey},
		"a forge that does not nest must refuse")
	add("asks-before-refusing-a-depth", fake.GitHubLike, []fake.Fault{fake.FaultAsksBeforeRefusingADepth},
		"the depth was refused only after a request went out")
	add("a-nesting-forge-refuses-a-depth", fake.GitLabLike, []fake.Fault{fake.FaultANestingForgeRefusesADepth},
		"a nesting forge must address")
	add("whoami-has-no-login", fake.GitHubLike, []fake.Fault{fake.FaultWhoamiHasNoLogin},
		"Whoami answered with no login")
	add("a-refused-credential-is-accepted", fake.GitHubLike, []fake.Fault{fake.FaultARefusedCredentialIsAccepted},
		"a refused credential must be an error and never a zero Identity",
		"a refused credential must not read as AccessNone")
	add("answers-beside-its-error", fake.GitHubLike, []fake.Fault{fake.FaultAnswersBesideItsError},
		"a failed call returned a value beside its error",
		"a failed Access returned a level a caller could act on",
		"a refused credential answered a level a caller could act on",
		"a failed Repository returned a value beside its error",
		"a followed hop answered a level a caller could act on",
		"an unreachable forge answered a level a caller could act on",
		"an unreachable forge answered a membership a caller could act on")
	add("visibility-is-permission", fake.GitHubLike, []fake.Fault{fake.FaultVisibilityIsPermission},
		"a readable repository with no grant read as",
		"CanWrite() said yes about a grant that is exactly read")
	add("writable-reads-as-read-only", fake.GitHubLike, []fake.Fault{fake.FaultWritableReadsAsReadOnly},
		"Writable read as")
	add("a-hidden-repository-is-a-raw-error", fake.GitHubLike, []fake.Fault{fake.FaultAHiddenRepositoryIsARawError},
		"must be AccessNone or *NotFoundError")
	add("a-hidden-repository-is-readable", fake.GitHubLike, []fake.Fault{fake.FaultAHiddenRepositoryIsReadable},
		"a repository the credential holds nothing on read as")
	add("a-refused-credential-is-no-access", fake.GitHubLike, []fake.Fault{fake.FaultARefusedCredentialIsNoAccess},
		"a refused credential must not read as AccessNone")
	add("missing-is-none", fake.GitHubLike, []fake.Fault{fake.FaultMissingIsNone},
		"must not answer an authorization question")
	add("a-missing-repository-is-no-error", fake.GitHubLike, []fake.Fault{fake.FaultAMissingRepositoryIsNoError},
		"a repository that does not exist was described anyway",
		// And the delete case reads "is it gone" the same way, so a driver that
		// answers for a repository that is not there says it is still there.
		"the repository is still there after a successful delete")
	add("outage-is-refusal", fake.GitHubLike, []fake.Fault{fake.FaultOutageIsRefusal},
		"an unreachable forge answered the authorization question")
	add("an-outage-is-not-found", fake.GitHubLike, []fake.Fault{fake.FaultAnOutageIsNotFound},
		"an outage must not read as 'no such repository'")
	add("answers-from-cache-while-unreachable", fake.GitHubLike, []fake.Fault{fake.FaultAnswersFromCacheWhileUnreachable},
		"an unreachable forge answered Whoami",
		"an unreachable forge answered Repository",
		"an unreachable forge answered a membership question",
		"an unreachable forge answered a membership a caller could act on",
		"an unreachable forge listed repositories")

	add("echoes-key", fake.GitHubLike, []fake.Fault{fake.FaultEchoesKey},
		"must be derived from the clone address the forge returned",
		"the forge and the caller disagree about which repository the record the forge answers for a repository that moved is",
		"clone address in the record the forge answers for a repository that moved for")
	// Every record the suite reads is a separate contract, and this fault is
	// wrong in all of them: the listing's, the created one's, the writable
	// fixture's and the moved one's. Naming one of the four would leave the
	// other three deletable.
	add("repository-address-names-another", fake.GitHubLike, []fake.Fault{fake.FaultRepositoryAddressNamesAnother},
		"clone address in a record the listing yielded for",
		"clone address in the record of the repository just created for",
		"clone address in the record of the writable fixture for",
		"clone address in the record the forge answers for a repository that moved for")
	add("the-records-address-carries-the-credential", fake.GitHubLike, []fake.Fault{fake.FaultTheRecordsAddressCarriesTheCredential},
		"clone address in a record the listing yielded, %q, does not parse",
		"clone address in the record of the repository just created, %q, does not parse",
		"clone address in the record of the writable fixture, %q, does not parse",
		"clone address in the record the forge answers for a repository that moved, %q, does not parse")
	add("the-record-has-no-default-branch", fake.GitHubLike, []fake.Fault{fake.FaultTheRecordHasNoDefaultBranch},
		"a record the listing yielded has no default branch",
		"the record of the repository just created has no default branch",
		"the record of the writable fixture has no default branch",
		"the record the forge answers for a repository that moved has no default branch")
	add("clone-address-names-another", fake.GitHubLike, []fake.Fault{fake.FaultCloneAddressNamesAnother},
		"the HTTPS clone address handed to a builder for",
		"the SSH clone address handed to a builder for")
	add("clone-address-carries-the-credential", fake.GitHubLike, []fake.Fault{fake.FaultCloneAddressCarriesTheCredential},
		"the HTTPS clone address handed to a builder, %q, does not parse",
		"the SSH clone address handed to a builder, %q, does not parse")
	add("https-address-has-no-username", fake.GitHubLike, []fake.Fault{fake.FaultHTTPSAddressHasNoUsername},
		"no HTTP username")
	add("ssh-address-has-no-account", fake.GitHubLike, []fake.Fault{fake.FaultSSHAddressHasNoAccount},
		"no SSH account")

	// A record whose KEY names a third repository, which is wrong on every
	// record the suite reads rather than only on the one that moved. On a
	// nesting forge, so the nested fixture's record is one of them.
	add("the-record-names-another-repository", fake.GitLabLike, []fake.Fault{fake.FaultTheRecordNamesAnotherRepository},
		"the listing omitted %q, which this credential can write",
		"must be derived from the clone address the forge returned",
		"about which repository the record of the writable fixture is",
		"about which repository the record of the repository just created is",
		"creating the same repository twice was not refused as the name being taken",
		"about which repository the record of the nested fixture is",
		"about which repository the record the forge answers for a repository that moved is",
		"clone address in a record the listing yielded for",
		"clone address in the record of the repository just created for",
		"clone address in the record of the writable fixture for",
		"clone address in the record of the nested fixture for",
		"clone address in the record the forge answers for a repository that moved for")

	add("asks-another-host-who-this-is", fake.GitHubLike, []fake.Fault{fake.FaultAsksAnotherHostWhoThisIs},
		"a recorded request went to %q, which is not the host this credential is for, of everything the subject sent",
		"a recorded request went to %q, which is not the host this credential is for, of the requests an operation made")

	// The nested record is a record too: only a nesting forge reaches the nested
	// fixture's round trip.
	add("a-nesting-forges-record-has-no-default-branch", fake.GitLabLike, []fake.Fault{fake.FaultTheRecordHasNoDefaultBranch},
		"a record the listing yielded has no default branch",
		"the record of the repository just created has no default branch",
		"the record of the writable fixture has no default branch",
		"the record of the nested fixture has no default branch",
		"the record the forge answers for a repository that moved has no default branch")
	add("a-nesting-forges-record-carries-the-credential", fake.GitLabLike, []fake.Fault{fake.FaultTheRecordsAddressCarriesTheCredential},
		"clone address in a record the listing yielded, %q, does not parse",
		"clone address in the record of the repository just created, %q, does not parse",
		"clone address in the record of the writable fixture, %q, does not parse",
		"clone address in the record of the nested fixture, %q, does not parse",
		"clone address in the record the forge answers for a repository that moved, %q, does not parse")
	add("a-nesting-forges-record-address-names-another", fake.GitLabLike, []fake.Fault{fake.FaultRepositoryAddressNamesAnother},
		"clone address in a record the listing yielded for",
		"clone address in the record of the repository just created for",
		"clone address in the record of the writable fixture for",
		"clone address in the record of the nested fixture for",
		"clone address in the record the forge answers for a repository that moved for")

	// The record a followed same-host redirect answers with is reachable only by
	// breaking the driver twice.
	add("follows-same-host-redirect+the-record-has-no-default-branch", fake.GitHubLike,
		[]fake.Fault{fake.FaultFollowsSameHostRedirect, fake.FaultTheRecordHasNoDefaultBranch},
		"an Access has nowhere to say it is about another repository",
		"a Handle names no repository",
		"stamps Hook.Key FROM THE ARGUMENT",
		"the method was replayed",
		"is gone: a write was replayed",
		"deleting a repository this credential holds admin on failed",
		"a record the listing yielded has no default branch",
		"the record of the repository just created has no default branch",
		"the record of the writable fixture has no default branch",
		"the record the forge answers for a repository that moved has no default branch",
		"the record a followed same-host redirect answered with has no default branch")
	add("follows-same-host-redirect+the-records-address-carries-the-credential", fake.GitHubLike,
		[]fake.Fault{fake.FaultFollowsSameHostRedirect, fake.FaultTheRecordsAddressCarriesTheCredential},
		"an Access has nowhere to say it is about another repository",
		"a Handle names no repository",
		"stamps Hook.Key FROM THE ARGUMENT",
		"the method was replayed",
		"is gone: a write was replayed",
		"deleting a repository this credential holds admin on failed",
		"clone address in a record the listing yielded, %q, does not parse",
		"clone address in the record of the repository just created, %q, does not parse",
		"clone address in the record of the writable fixture, %q, does not parse",
		"clone address in the record the forge answers for a repository that moved, %q, does not parse",
		"clone address in the record a followed same-host redirect answered with, %q, does not parse")

	add("follows-redirect", fake.GitHubLike, []fake.Fault{fake.FaultFollowsRedirect},
		"succeeded anyway, so an answer from another host was taken for the forge's",
		"a request arrived at",
		"and it carried the credential there",
		"a recorded request went to %q, which is not the host this credential is for, of the requests an operation made")
	// The credential on this hop is the deployment's, which no Subject hands over,
	// so it is caught by the arrival, not by what it carries.
	add("follows-redirect-on-the-deployment-credential", fake.GitHubLike, []fake.Fault{fake.FaultFollowsRedirectOnTheDeploymentCredential},
		"succeeded anyway, so an answer from another host was taken for the forge's",
		"a request arrived at",
		"a recorded request went to %q, which is not the host this credential is for, of the requests an operation made")
	add("follows-same-host-redirect", fake.GitHubLike, []fake.Fault{fake.FaultFollowsSameHostRedirect},
		"an Access has nowhere to say it is about another repository",
		"a Handle names no repository",
		"stamps Hook.Key FROM THE ARGUMENT",
		"the method was replayed",
		"is gone: a write was replayed",
		"deleting a repository this credential holds admin on failed")
	add("follows-same-host-redirect+echoes-key", fake.GitHubLike,
		[]fake.Fault{fake.FaultFollowsSameHostRedirect, fake.FaultEchoesKey},
		"the record describes",
		"must be derived from the clone address the forge returned",
		"the forge and the caller disagree about which repository the record the forge answers for a repository that moved is",
		"the forge and the caller disagree about which repository the record a followed same-host redirect answered with is",
		"clone address in the record the forge answers for a repository that moved for",
		"clone address in the record a followed same-host redirect answered with for",
		"an Access has nowhere to say it is about another repository",
		"a Handle names no repository",
		"stamps Hook.Key FROM THE ARGUMENT",
		"the method was replayed",
		"is gone: a write was replayed",
		"deleting a repository this credential holds admin on failed")
	add("leaks-token-in-error", fake.GitHubLike, []fake.Fault{fake.FaultLeaksTokenInError},
		"the live credential is in an error string", "the refused credential is in an error string")
	add("leaks-token-in-url", fake.GitHubLike, []fake.Fault{fake.FaultLeaksTokenInURL},
		"the credential is in a request URL, of the requests an operation made",
		"the credential is in a request URL, of everything the subject sent",
		"the credential is in a query parameter, of the requests an operation made",
		"the credential is in a query parameter, of everything the subject sent",
		// And an address with a credential in it is quoted back by whatever
		// failed at it, so the URL canary and the error canary catch the same
		// driver. Both, or neither is load-bearing.
		"the live credential is in an error string")

	add("lists-nothing", fake.GitHubLike, []fake.Fault{fake.FaultListsNothing},
		"the listing yielded nothing")
	add("list-omits-what-can-be-written", fake.GitHubLike, []fake.Fault{fake.FaultListOmitsWhatCanBeWritten},
		"the listing omitted")
	add("list-ignores-a-stopped-yield", fake.GitHubLike, []fake.Fault{fake.FaultListIgnoresAStoppedYield},
		"yield returned false and the listing kept going")
	add("creates-the-same-repository-twice", fake.GitHubLike, []fake.Fault{fake.FaultCreatesTheSameRepositoryTwice},
		"creating the same repository twice was not refused as the name being taken")
	add("creates-in-any-namespace", fake.GitHubLike, []fake.Fault{fake.FaultCreatesInAnyNamespace},
		"created a repository in a namespace this credential may not write")
	add("deletes-without-admin", fake.GitHubLike, []fake.Fault{fake.FaultDeletesWithoutAdmin},
		"deleted a repository this credential holds only")
	add("delete-forgets-the-repository", fake.GitHubLike, []fake.Fault{fake.FaultDeleteForgetsTheRepository},
		"the repository is still there after a successful delete")

	add("deploy-key-handle-is-empty", fake.GitHubLike, []fake.Fault{fake.FaultDeployKeyHandleIsEmpty},
		"an empty handle is a key nothing can ever remove")
	add("deploy-key-handle-names-nothing", fake.GitHubLike, []fake.Fault{fake.FaultDeployKeyHandleNamesNothing},
		"the handle names no key on the forge",
		"the listing does not name the key just installed",
		"removing the deploy key this driver just installed failed")
	add("removing-an-unknown-deploy-key-succeeds", fake.GitHubLike, []fake.Fault{fake.FaultRemovingAnUnknownDeployKeySucceeds},
		"removing a key that is not there succeeded")
	add("writable-deploy-key", fake.GitHubLike, []fake.Fault{fake.FaultWritableDeployKey},
		"the installed deploy key can push")
	add("hook-handle-names-nothing", fake.GitHubLike, []fake.Fault{fake.FaultHookHandleNamesNothing},
		"the handle names no hook on the forge",
		"the listing does not name the hook just registered",
		"removing the hook this driver just registered failed")
	add("removing-an-unknown-hook-succeeds", fake.GitHubLike, []fake.Fault{fake.FaultRemovingAnUnknownHookSucceeds},
		"removing a hook that is not there succeeded")

	// Each of the three faults is load-bearing: without removes-without-a-handle
	// nothing fires; without the two "unknown removal succeeds" faults only the
	// wire half fires.
	add("removes-without-a-handle", fake.GitHubLike, []fake.Fault{
		fake.FaultRemovesWithoutAHandle,
		fake.FaultRemovingAnUnknownDeployKeySucceeds,
		fake.FaultRemovingAnUnknownHookSucceeds},
		"removing a key that is not there succeeded",
		"removing a hook that is not there succeeded",
		"removing a deploy key with no handle at all succeeded",
		"removing a push hook with no handle at all succeeded",
		"a deploy-key removal naming no handle went to the wire",
		"a hook removal naming no handle went to the wire")
	add("forgets-removal", fake.GitHubLike, []fake.Fault{fake.FaultForgetsRemoval},
		"still installed after a successful removal",
		"the hook is still registered after a successful removal",
		"the listing still names a key that was removed",
		"the listing still names a hook that was removed")

	// A mangled wire is two complaints and not one: the forge was handed a
	// string the verifier will not key its MAC with, and then every delivery it
	// signs with that string is rejected. The second is what a deployment
	// actually experiences, months later, as a hook that never fires.
	add("mangles-wire", fake.GitHubLike, []fake.Fault{fake.FaultManglesWire},
		"wire secret the verifier will not key its MAC with",
		"a correctly signed delivery was rejected")
	add("mangles-wire-under-standard-webhooks", fake.GitLabLike, []fake.Fault{fake.FaultManglesWire},
		"wire secret the verifier will not key its MAC with",
		"a correctly signed delivery was rejected")
	add("signs-with-another-scheme", fake.GitHubLike, []fake.Fault{fake.FaultSignsWithAnotherScheme},
		"a correctly signed delivery was rejected")
	add("push-reads-the-base-ref", fake.GitHubLike, []fake.Fault{fake.FaultPushReadsTheBaseRef},
		"the driver read a push the payload does not describe")
	// A deletion is spelled as an after of all zeroes, so a driver reading the
	// commit the ref moved FROM reports an ordinary push of a branch that is
	// gone. Both complaints are the same misreading, seen twice: once as a
	// commit that is not the one the payload names, and once as a deletion that
	// stopped looking like one.
	add("push-reads-the-commit-it-moved-from", fake.GitHubLike, []fake.Fault{fake.FaultPushReadsTheCommitItMovedFrom},
		"the driver read a push the payload does not describe",
		"a ref deletion must be distinguishable from a failure")
	add("any-delivery-reads-as-a-push", fake.GitHubLike, []fake.Fault{fake.FaultAnyDeliveryReadsAsAPush},
		"a delivery that is not a push read as one")

	add("push-probe-says-no-to-a-key-that-may", fake.GitHubLike, []fake.Fault{fake.FaultPushProbeSaysNoToAKeyThatMay},
		"a credential that may push read as one that may not")
	add("push-probe-says-yes-to-a-key-that-may-not", fake.GitHubLike, []fake.Fault{fake.FaultPushProbeSaysYesToAKeyThatMayNot},
		"a credential that may not push read as one that may")
	add("push-probe-answers-for-a-missing-repository", fake.GitHubLike, []fake.Fault{fake.FaultPushProbeAnswersForAMissingRepository},
		"probing a repository that does not exist must not answer false")
	add("a-member-reads-as-a-non-member", fake.GitHubLike, []fake.Fault{fake.FaultAMemberReadsAsANonMember},
		"read as a non-member")
	add("a-non-member-reads-as-a-member", fake.GitHubLike, []fake.Fault{fake.FaultANonMemberReadsAsAMember},
		"read as a member")
	return out
}

// subjectProofs is a SUBJECT that is wrong: a capability declared that the
// driver does not have, and every probe the suite cannot run without.
func subjectProofs() []proof {
	var out []proof
	// Subject proofs inject no fault: the subject is what is wrong.
	add := func(name string, build func(t *testing.T) Subject, says ...string) {
		out = append(out, proof{name: name,
			subject: func(t *testing.T, _ []fake.Fault) Subject { return build(t) },
			says:    says})
	}

	// A capability set that does not match the driver, both ways round.
	add("overclaims-a-capability", func(t *testing.T) Subject {
		s := subjectFor(t, "overclaims", without(fake.GitHubLike, forge.CapPushHooks))
		s.Expect[forge.CapPushHooks] = struct{}{}
		return s
	}, "capabilities =", "refused a capability the session has")
	stop(out, "overclaims-a-capability",
		// A capability the driver does not have is a call that answers
		// *UnsupportedError where the suite was on its way to a question, so the
		// overclaim stops the suite as well as failing it.
		"registering the hook every delivery below is signed for",
		"learning the address this operation uses for")

	// One proof per capability the driver has and the Subject does not declare:
	// each takes a different absent branch.
	for _, tc := range []struct {
		cap   forge.Capability
		calls []string
	}{
		{forge.CapList, []string{"so ListRepositories must be"}},
		{forge.CapCreate, []string{"so Create must be"}},
		{forge.CapDelete, []string{"so Delete must be", "so Delete under a same-host redirect must be"}},
		{forge.CapDeployKeys, []string{"so InstallDeployKey must be", "so RemoveDeployKey must be", "so ListDeployKeys must be",
			"so InstallDeployKey under a same-host redirect must be"}},
		{forge.CapCloneAddr, []string{"so CloneAddress must be"}},
		{forge.CapPushProbe, []string{"so CanPush must be"}},
		{forge.CapMembership, []string{"so IsActiveMember must be"}},
		{forge.CapPushHooks, []string{"so RegisterPushHook must be", "so RemovePushHook must be", "so ListPushHooks must be",
			"so RegisterPushHook under a same-host redirect must be"}},
		{forge.CapSignatures, []string{"so RegisterPushHook must be", "so VerifyPush must be",
			"so RegisterPushHook under a same-host redirect must be"}},
	} {
		missing := tc.cap
		says := append([]string{"capabilities =", "must refuse a capability the session lacks"}, tc.calls...)
		add("underclaims-"+string(missing), func(t *testing.T) Subject {
			s := subjectFor(t, "underclaims-"+string(missing), fake.GitHubLike)
			delete(s.Expect, missing)
			return s
		}, says...)
	}

	// A Registry that is not the one binding this suite is run against. Both of
	// these are preflight, and both are the difference between a suite that ran
	// and a suite that quietly ran something else.
	add("a-host-the-registry-has-no-driver-for", func(t *testing.T) Subject {
		s := subjectFor(t, "no-driver", fake.GitHubLike)
		s.Host = "elsewhere.invalid"
		return s
	}, "the Registry has no driver for")

	add("a-registry-holding-more-than-one-binding", func(t *testing.T) Subject {
		s := subjectFor(t, "two-bindings", fake.GitHubLike)
		other := fake.New(fake.GitLabLike)
		reg, err := forge.New(forge.Policy{},
			forge.Binding{Host: s.Host, Driver: mustDriver(t, s)},
			forge.Binding{Host: other.Host(), Driver: other.Driver()})
		if err != nil {
			t.Fatalf("building a registry with two bindings: %v", err)
		}
		s.Registry = reg
		return s
	}, "the Registry must hold exactly one binding")

	broken := func(name string, break_ func(*Subject), says ...string) {
		add(name, func(t *testing.T) Subject {
			s := subjectFor(t, name, fake.GitHubLike)
			break_(&s)
			return s
		}, says...)
	}
	broken("no-requests-probe", func(s *Subject) { s.Requests = nil }, "an outage that is not a refusal")
	broken("no-redirect-probe", func(s *Subject) { s.RedirectOnce = nil }, "where a request WENT is unobservable")
	broken("a-moved-fixture-that-did-not-move", func(s *Subject) { s.Moved = s.MovedTo }, "answer for one key and answer WITH another")
	broken("no-moved-fixture", func(s *Subject) { s.Moved = repokey.Key{} }, "fixture %s is not set")
	broken("no-break-probe", func(s *Subject) { s.Break = nil }, "an outage that is not a refusal")
	broken("no-expect", func(s *Subject) { s.Expect = nil }, "exact capability set")
	broken("no-registry", func(s *Subject) { s.Registry = nil }, "needs a Registry and a Host")
	broken("no-token", func(s *Subject) { s.Token = "" }, "raw credential the canary looks for")
	broken("fixture-on-another-host", func(s *Subject) { s.Writable.Host = "elsewhere.invalid" }, "is on host")
	broken("deep-fixture-is-not-deep", func(s *Subject) { s.Deep.Owner = "acme" }, "must have a nested owner")
	broken("no-hook-probe", func(s *Subject) { s.LastHookWire = nil }, "CapPushHooks needs LastHookWire")
	broken("no-deploy-key-probe", func(s *Subject) { s.DeployKeyInstalled = nil }, "CapDeployKeys needs")
	broken("no-delivery-builder", func(s *Subject) { s.Delivery = nil }, "CapSignatures needs")
	broken("no-membership-fixtures", func(s *Subject) { s.Member = "" }, "CapMembership needs")
	broken("no-push-probe-keys", func(s *Subject) { s.PushKey = nil }, "CapPushProbe needs")

	// The probes are part of the subject, and a probe that lies is a fault: a
	// DeliveryFor naming the hook's own repository, or a DeliveryAt ignoring the
	// date, makes the suite green for no stated reason.

	add("a-deputy-delivery-that-names-this-very-repository", func(t *testing.T) Subject {
		s := subjectFor(t, "deputy-names-this-one", fake.GitHubLike)
		plain := s.Delivery
		s.DeliveryFor = func(h forge.Hook, _ repokey.Key, ref, commit string) (forge.Delivery, error) {
			return plain(h, ref, commit)
		}
		return s
	}, "a perfectly signed delivery naming another repository was accepted",
		"a delivery naming another repository produced a Push beside its error")

	// The other way a deputy can prove nothing: it names another repository and
	// is signed for nothing, so the core refuses it on the signature and never
	// reaches the comparison the case exists for.
	add("a-deputy-delivery-nothing-signed", func(t *testing.T) Subject {
		s := subjectFor(t, "deputy-unsigned", fake.GitHubLike)
		deputy := s.DeliveryFor
		s.DeliveryFor = func(h forge.Hook, other repokey.Key, ref, commit string) (forge.Delivery, error) {
			stranger, err := forge.NewSecret()
			if err != nil {
				return forge.Delivery{}, err
			}
			// A handle this forge does not hold, so the secret below is what
			// signs rather than the wire the forge was actually given.
			h.Handle, h.Secret = "a-handle-no-forge-holds", stranger
			return deputy(h, other, ref, commit)
		}
		return s
	}, "the refusal does not say the delivery claimed another repository")

	add("a-dating-builder-that-ignores-the-date", func(t *testing.T) Subject {
		s := subjectFor(t, "dating-ignores-the-date", fake.GitLabLike)
		plain := s.Delivery
		s.DeliveryAt = func(h forge.Hook, ref, commit string, _ time.Time) (forge.Delivery, error) {
			return plain(h, ref, commit)
		}
		return s
	}, "a delivery signed an hour ago was accepted",
		"a delivery signed an hour from now was accepted",
		"a delivery signed an hour ago produced a Push beside its error",
		"a delivery signed an hour from now produced a Push beside its error")

	add("a-timestamped-scheme-with-no-dating-builder", func(t *testing.T) Subject {
		s := subjectFor(t, "no-delivery-at", fake.GitLabLike)
		s.DeliveryAt = nil
		return s
	}, "signs a timestamp")
	return out
}

// mustDriver is the driver already bound in a Subject's registry, which is the
// only way to build a SECOND registry holding it as well.
func mustDriver(t *testing.T, s Subject) forge.Driver {
	t.Helper()
	d, err := s.Registry.Driver(s.Host)
	if err != nil {
		t.Fatalf("the subject's own registry has no driver for %q: %v", s.Host, err)
	}
	return d
}

// stop records the steps a proof prevents the suite from taking. It is written
// beside the proof rather than inside it so that says stays the list of
// complaints and nothing else.
func stop(out []proof, name string, steps ...string) {
	for i := range out {
		if out[i].name == name {
			out[i].stops = append(out[i].stops, steps...)
			return
		}
	}
	panic("forgetest: no proof named " + name)
}
