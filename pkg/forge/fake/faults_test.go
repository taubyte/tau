package fake_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
)

// Every fault is observed twice: on the shape as written, giving the right
// answer, and with the fault set, giving the named wrong one.

type env struct {
	t   *testing.T
	f   *fake.Forge
	d   forge.Driver
	c   forge.Client
	k   fake.Keys
	ctx context.Context
}

// shapeOf is a copy of s, so the table never points into the package's own.
func shapeOf(s fake.Shape) *fake.Shape { return &s }

func (e env) client(cred forge.Credential) forge.Client {
	c, err := e.d.Bind(cred)
	require.NoError(e.t, err)
	return c
}

func kind(err error) string {
	var nf *forge.NotFoundError
	var ce *forge.CredentialError
	switch {
	case err == nil:
		return "nil"
	case errors.As(err, &nf):
		return "notfound"
	case errors.As(err, &ce):
		return "credential"
	case errors.Is(err, forge.ErrNotAPush):
		return "notapush"
	case strings.Contains(err.Error(), "unreachable"):
		return "unreachable"
	}
	return "other"
}

func o(v any, err error) string { return fmt.Sprintf("%v %s", v, kind(err)) }

// verified is the push a correctly signed delivery for k reads as, through the
// registry and so through the driver's Push.
func (e env) verified(ref, commit string) (forge.Push, error) {
	reg, err := e.f.Registry(forge.Policy{})
	require.NoError(e.t, err)
	h := e.hook()
	d, err := e.f.Delivery(h, ref, commit)
	require.NoError(e.t, err)
	return reg.VerifyPush(h, d)
}

func (e env) hook() forge.Hook {
	secret, err := forge.NewSecret()
	require.NoError(e.t, err)
	return forge.Hook{Key: e.k.Admin, Handle: "unheld", Scheme: e.d.(forge.Signatures).Scheme(), Secret: secret}
}

// watched is a loopback server counting what reaches it, which is the only way
// to see where a followed hop went.
func watched(t *testing.T) (*httptest.Server, *atomic.Int32) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

const commit = "1f0c9d2b3a4e5f60718293a4b5c6d7e8f9012345"

func TestEachFaultIsTheMistakeItNames(t *testing.T) {
	cb, err := url.Parse("https://cloud.invalid/hook")
	require.NoError(t, err)

	type testCase struct {
		fault   fake.Fault
		shape   *fake.Shape // GitHubLike when nil
		observe func(e env) string
		right   string
		wrong   string
	}
	for _, tc := range []testCase{
		{fault: fake.FaultWhoamiHasNoLogin,
			observe: func(e env) string { id, err := e.c.Whoami(e.ctx); return o(id.Login, err) },
			right:   "fake-user nil", wrong: " nil"},
		{fault: fake.FaultBindsAZeroCredential,
			observe: func(e env) string { _, err := e.d.Bind(forge.Credential{}); return kind(err) },
			right:   "other", wrong: "nil"},
		{fault: fake.FaultRefusesAnonymousAtTheDoor,
			observe: func(e env) string { _, err := e.d.Bind(forge.Anonymous()); return kind(err) },
			right:   "nil", wrong: "other"},
		{fault: fake.FaultAsksAnotherHostWhoThisIs,
			observe: func(e env) string {
				_, err := e.c.Whoami(e.ctx)
				require.NoError(e.t, err)
				u, err := url.Parse(e.f.Requests()[0].URL)
				require.NoError(e.t, err)
				return u.Host
			},
			right: "fake.github", wrong: "elsewhere.invalid"},
		{fault: fake.FaultLeaksTokenInURL,
			observe: func(e env) string {
				_, err := e.c.Whoami(e.ctx)
				require.NoError(e.t, err)
				return fmt.Sprint(strings.Contains(e.f.Requests()[0].URL, e.f.TokenString()))
			},
			right: "false", wrong: "true"},
		{fault: fake.FaultLeaksTokenInError,
			observe: func(e env) string {
				_, err := e.client(e.f.BadToken()).Access(e.ctx, e.k.Writable)
				return fmt.Sprint(strings.Contains(err.Error(), e.f.BadTokenString()))
			},
			right: "false", wrong: "true"},
		{fault: fake.FaultLeaksTokenInError,
			observe: func(e env) string {
				e.f.Break()
				_, err := e.c.Access(e.ctx, e.k.Writable)
				return o(strings.Contains(err.Error(), e.f.TokenString()), err)
			},
			right: "false unreachable", wrong: "true unreachable"},

		{fault: fake.FaultOutageIsRefusal,
			observe: func(e env) string { e.f.Break(); return o(e.c.Access(e.ctx, e.k.Writable)) },
			right:   o(forge.AccessNone, errors.New("unreachable")), wrong: o(forge.AccessNone, nil)},
		{fault: fake.FaultAnOutageIsNotFound,
			observe: func(e env) string { e.f.Break(); _, err := e.c.Access(e.ctx, e.k.Writable); return kind(err) },
			right:   "unreachable", wrong: "notfound"},
		{fault: fake.FaultAnswersBesideItsError,
			observe: func(e env) string { e.f.Break(); return o(e.c.Access(e.ctx, e.k.Writable)) },
			right:   o(forge.AccessNone, errors.New("unreachable")), wrong: o(forge.AccessWrite, errors.New("unreachable"))},
		{fault: fake.FaultARefusedCredentialIsNoAccess,
			observe: func(e env) string { return o(e.client(e.f.BadToken()).Access(e.ctx, e.k.Writable)) },
			right:   o(forge.AccessNone, &forge.CredentialError{}), wrong: o(forge.AccessNone, nil)},
		{fault: fake.FaultARefusedCredentialIsAccepted,
			observe: func(e env) string { _, err := e.client(e.f.BadToken()).Access(e.ctx, e.k.Writable); return kind(err) },
			right:   "credential", wrong: "nil"},
		{fault: fake.FaultMissingIsNone,
			observe: func(e env) string { return o(e.c.Access(e.ctx, e.k.Missing)) },
			right:   o(forge.AccessNone, &forge.NotFoundError{}), wrong: o(forge.AccessNone, nil)},
		{fault: fake.FaultAHiddenRepositoryIsARawError,
			observe: func(e env) string { return o(e.c.Access(e.ctx, e.k.NoAccess)) },
			right:   o(forge.AccessNone, nil), wrong: o(forge.AccessNone, errors.New("403"))},
		{fault: fake.FaultAHiddenRepositoryIsReadable,
			observe: func(e env) string { return o(e.c.Access(e.ctx, e.k.NoAccess)) },
			right:   o(forge.AccessNone, nil), wrong: o(forge.AccessRead, nil)},
		{fault: fake.FaultVisibilityIsPermission,
			observe: func(e env) string { return o(e.c.Access(e.ctx, e.k.ReadOnly)) },
			right:   o(forge.AccessRead, nil), wrong: o(forge.AccessWrite, nil)},
		{fault: fake.FaultWritableReadsAsReadOnly,
			observe: func(e env) string { return o(e.c.Access(e.ctx, e.k.Writable)) },
			right:   o(forge.AccessWrite, nil), wrong: o(forge.AccessRead, nil)},

		{fault: fake.FaultFlattensDeepKey,
			observe: func(e env) string { return kind(e.d.Addressable(e.k.Deep)) },
			right:   "other", wrong: "nil"},
		{fault: fake.FaultFlattensDeepKey,
			observe: func(e env) string { return o(e.c.Access(e.ctx, e.k.Deep)) },
			right:   o(forge.AccessAdmin, nil), wrong: o(forge.AccessNone, &forge.NotFoundError{})},
		{fault: fake.FaultANestingForgeRefusesADepth, shape: shapeOf(fake.GitLabLike),
			observe: func(e env) string { return kind(e.d.Addressable(e.k.Deep)) },
			right:   "nil", wrong: "other"},
		{fault: fake.FaultAsksBeforeRefusingADepth,
			observe: func(e env) string {
				require.Error(e.t, e.d.Addressable(e.k.Deep))
				return fmt.Sprint(len(e.f.Requests()))
			},
			right: "0", wrong: "1"},

		{fault: fake.FaultAnswersFromCacheWhileUnreachable,
			observe: func(e env) string { e.f.Break(); id, err := e.c.Whoami(e.ctx); return o(id.Login, err) },
			right:   " unreachable", wrong: "fake-user nil"},
		{fault: fake.FaultAnswersFromCacheWhileUnreachable,
			observe: func(e env) string {
				e.f.Break()
				r, err := e.c.Repository(e.ctx, e.k.Writable)
				return o(r.Key.Name, err)
			},
			right: " unreachable", wrong: "pay nil"},
		{fault: fake.FaultAnswersFromCacheWhileUnreachable,
			observe: func(e env) string {
				e.f.Break()
				n := 0
				err := e.c.(forge.Lister).ListRepositories(e.ctx, func(forge.Repository) bool { n++; return true })
				return o(n, err)
			},
			right: "0 unreachable", wrong: "3 nil"},
		{fault: fake.FaultAnswersFromCacheWhileUnreachable,
			observe: func(e env) string {
				e.f.Break()
				return o(e.d.(forge.Memberships).IsActiveMember(e.ctx, fake.Namespace, fake.Member))
			},
			right: "false unreachable", wrong: "true nil"},
		{fault: fake.FaultAnswersBesideItsError,
			observe: func(e env) string {
				e.f.Break()
				r, err := e.c.Repository(e.ctx, e.k.Writable)
				return o(r.Key.Name, err)
			},
			right: " unreachable", wrong: "pay unreachable"},
		{fault: fake.FaultAMissingRepositoryIsNoError,
			observe: func(e env) string { r, err := e.c.Repository(e.ctx, e.k.Missing); return o(r.Key.Name, err) },
			right:   " notfound", wrong: " nil"},
		{fault: fake.FaultTheRecordHasNoDefaultBranch,
			observe: func(e env) string { r, err := e.c.Repository(e.ctx, e.k.Writable); return o(r.DefaultBranch, err) },
			right:   "main nil", wrong: " nil"},
		{fault: fake.FaultTheRecordsAddressCarriesTheCredential,
			observe: func(e env) string {
				r, err := e.c.Repository(e.ctx, e.k.Writable)
				return o(strings.Contains(r.HTTPS+r.SSH, e.f.TokenString()), err)
			},
			right: "false nil", wrong: "true nil"},
		{fault: fake.FaultRepositoryAddressNamesAnother,
			observe: func(e env) string { r, err := e.c.Repository(e.ctx, e.k.Writable); return o(r.HTTPS, err) },
			right:   "https://fake.github/acme/pay.git nil", wrong: "https://fake.github/acme/elsewhere.git nil"},
		{fault: fake.FaultTheRecordNamesAnotherRepository,
			observe: func(e env) string { r, err := e.c.Repository(e.ctx, e.k.Writable); return o(r.Key.FullName(), err) },
			right:   "acme/pay nil", wrong: "acme/elsewhere nil"},
		{fault: fake.FaultEchoesKey,
			observe: func(e env) string { r, err := e.c.Repository(e.ctx, e.k.Moved); return o(r.Key.FullName(), err) },
			right:   "acme/pay nil", wrong: "acme/billing nil"},

		{fault: fake.FaultListsNothing,
			observe: func(e env) string {
				n := 0
				err := e.c.(forge.Lister).ListRepositories(e.ctx, func(forge.Repository) bool { n++; return true })
				return o(n, err)
			},
			right: "3 nil", wrong: "0 nil"},
		{fault: fake.FaultListOmitsWhatCanBeWritten,
			observe: func(e env) string {
				var names []string
				err := e.c.(forge.Lister).ListRepositories(e.ctx, func(r forge.Repository) bool { names = append(names, r.Key.Name); return true })
				return o(names, err)
			},
			right: "[pay public site] nil", wrong: "[public site] nil"},
		{fault: fake.FaultListIgnoresAStoppedYield,
			observe: func(e env) string {
				n := 0
				err := e.c.(forge.Lister).ListRepositories(e.ctx, func(forge.Repository) bool { n++; return false })
				return o(n, err)
			},
			right: "1 nil", wrong: "3 nil"},

		{fault: fake.FaultCreatesTheSameRepositoryTwice,
			observe: func(e env) string {
				_, err := e.c.(forge.Creator).Create(e.ctx, e.k.Writable, forge.CreateOptions{})
				return kind(err)
			},
			right: "other", wrong: "nil"},
		{fault: fake.FaultCreatesInAnyNamespace,
			observe: func(e env) string {
				_, err := e.c.(forge.Creator).Create(e.ctx, e.k.Uncreatable, forge.CreateOptions{})
				return kind(err)
			},
			right: "other", wrong: "nil"},
		{fault: fake.FaultDeletesWithoutAdmin,
			observe: func(e env) string { return kind(e.c.(forge.Deleter).Delete(e.ctx, e.k.Writable)) },
			right:   "other", wrong: "nil"},
		{fault: fake.FaultDeleteForgetsTheRepository,
			observe: func(e env) string {
				require.NoError(e.t, e.c.(forge.Deleter).Delete(e.ctx, e.k.Deletable))
				_, err := e.c.Access(e.ctx, e.k.Deletable)
				return kind(err)
			},
			right: "notfound", wrong: "nil"},

		{fault: fake.FaultWritableDeployKey,
			observe: func(e env) string {
				_, err := e.c.(forge.DeployKeys).InstallDeployKey(e.ctx, e.k.Admin, "t", "ssh-ed25519 AAAA")
				return o(e.f.LastDeployKeyReadOnly(), err)
			},
			right: "true nil", wrong: "false nil"},
		{fault: fake.FaultDeployKeyHandleIsEmpty,
			observe: func(e env) string {
				h, err := e.c.(forge.DeployKeys).InstallDeployKey(e.ctx, e.k.Admin, "t", "ssh-ed25519 AAAA")
				return o(h == "", err)
			},
			right: "false nil", wrong: "true nil"},
		{fault: fake.FaultDeployKeyHandleNamesNothing,
			observe: func(e env) string {
				h, err := e.c.(forge.DeployKeys).InstallDeployKey(e.ctx, e.k.Admin, "t", "ssh-ed25519 AAAA")
				return o(e.f.DeployKeyInstalled(h), err)
			},
			right: "true nil", wrong: "false nil"},
		{fault: fake.FaultRemovingAnUnknownDeployKeySucceeds,
			observe: func(e env) string { return kind(e.c.(forge.DeployKeys).RemoveDeployKey(e.ctx, e.k.Admin, "9999")) },
			right:   "other", wrong: "nil"},
		{fault: fake.FaultForgetsRemoval,
			observe: func(e env) string {
				dk := e.c.(forge.DeployKeys)
				h, err := dk.InstallDeployKey(e.ctx, e.k.Admin, "t", "ssh-ed25519 AAAA")
				require.NoError(e.t, err)
				err = dk.RemoveDeployKey(e.ctx, e.k.Admin, h)
				return o(e.f.DeployKeyInstalled(h), err)
			},
			right: "false nil", wrong: "true nil"},
		{fault: fake.FaultRemovesWithoutAHandle,
			observe: func(e env) string {
				e.c.(forge.DeployKeys).RemoveDeployKey(e.ctx, e.k.Admin, "")
				return fmt.Sprint(len(e.f.Requests()))
			},
			right: "0", wrong: "1"},

		{fault: fake.FaultManglesWire,
			observe: func(e env) string {
				_, err := e.c.(forge.PushHooks).RegisterPushHook(e.ctx, e.k.Admin, cb, "wire-abc")
				return o(e.f.LastHookWire(), err)
			},
			right: "wire-abc nil", wrong: "WIRE-ABC nil"},
		{fault: fake.FaultManglesWire,
			observe: func(e env) string {
				// A hook the forge holds signs with the wire it was given, so a
				// mangled registration is a delivery nothing verifies.
				reg, err := e.f.Registry(forge.Policy{})
				require.NoError(e.t, err)
				h := e.hook()
				wire, err := h.Scheme.Wire(h.Secret)
				require.NoError(e.t, err)
				h.Handle, err = e.c.(forge.PushHooks).RegisterPushHook(e.ctx, e.k.Admin, cb, wire)
				require.NoError(e.t, err)
				d, err := e.f.Delivery(h, "refs/heads/main", commit)
				require.NoError(e.t, err)
				_, err = reg.VerifyPush(h, d)
				return fmt.Sprint(err == nil)
			},
			right: "true", wrong: "false"},
		{fault: fake.FaultHookHandleNamesNothing,
			observe: func(e env) string {
				h, err := e.c.(forge.PushHooks).RegisterPushHook(e.ctx, e.k.Admin, cb, "w")
				return o(e.f.HookInstalled(h), err)
			},
			right: "true nil", wrong: "false nil"},
		{fault: fake.FaultRemovingAnUnknownHookSucceeds,
			observe: func(e env) string { return kind(e.c.(forge.PushHooks).RemovePushHook(e.ctx, e.k.Admin, "9999")) },
			right:   "other", wrong: "nil"},
		{fault: fake.FaultForgetsRemoval,
			observe: func(e env) string {
				ph := e.c.(forge.PushHooks)
				h, err := ph.RegisterPushHook(e.ctx, e.k.Admin, cb, "w")
				require.NoError(e.t, err)
				err = ph.RemovePushHook(e.ctx, e.k.Admin, h)
				return o(e.f.HookInstalled(h), err)
			},
			right: "false nil", wrong: "true nil"},
		{fault: fake.FaultRemovesWithoutAHandle,
			observe: func(e env) string {
				e.c.(forge.PushHooks).RemovePushHook(e.ctx, e.k.Admin, "")
				return fmt.Sprint(len(e.f.Requests()))
			},
			right: "0", wrong: "1"},

		{fault: fake.FaultCloneAddressNamesAnother,
			observe: func(e env) string {
				a, err := e.c.(forge.CloneAddresses).CloneAddress(e.k.Writable, forge.SSH)
				return o(a.URL, err)
			},
			right: "ssh://git@fake.github/acme/pay.git nil", wrong: "ssh://git@fake.github/acme/elsewhere.git nil"},
		{fault: fake.FaultSSHAddressHasNoAccount,
			observe: func(e env) string {
				a, err := e.c.(forge.CloneAddresses).CloneAddress(e.k.Writable, forge.SSH)
				return o(a.SSHAccount, err)
			},
			right: "git nil", wrong: " nil"},
		{fault: fake.FaultHTTPSAddressHasNoUsername,
			observe: func(e env) string {
				a, err := e.c.(forge.CloneAddresses).CloneAddress(e.k.Writable, forge.HTTPS)
				return o(a.HTTPUsername, err)
			},
			right: "x-access-token nil", wrong: " nil"},
		{fault: fake.FaultHTTPSAddressHasNoUsername, shape: shapeOf(fake.BitbucketLike),
			observe: func(e env) string {
				a, err := e.c.(forge.CloneAddresses).CloneAddress(e.k.Writable, forge.HTTPS)
				return o(a.HTTPUsername, err)
			},
			right: "x-token-auth nil", wrong: " nil"},
		{fault: fake.FaultCloneAddressCarriesTheCredential,
			observe: func(e env) string {
				a, err := e.c.(forge.CloneAddresses).CloneAddress(e.k.Writable, forge.HTTPS)
				return o(a.URL, err)
			},
			right: "https://fake.github/acme/pay.git nil", wrong: "https://x-access-token:fake-token-6f1c8a2e@fake.github/acme/pay.git nil"},

		{fault: fake.FaultPushProbeSaysNoToAKeyThatMay,
			observe: func(e env) string { return o(e.c.(forge.PushProber).CanPush(e.ctx, e.k.Admin, e.f.PushKey())) },
			right:   "true nil", wrong: "false nil"},
		{fault: fake.FaultPushProbeSaysYesToAKeyThatMayNot,
			observe: func(e env) string { return o(e.c.(forge.PushProber).CanPush(e.ctx, e.k.Admin, e.f.NoPushKey())) },
			right:   "false nil", wrong: "true nil"},
		{fault: fake.FaultPushProbeAnswersForAMissingRepository,
			observe: func(e env) string { return o(e.c.(forge.PushProber).CanPush(e.ctx, e.k.Missing, e.f.NoPushKey())) },
			right:   "false notfound", wrong: "false nil"},

		{fault: fake.FaultAMemberReadsAsANonMember,
			observe: func(e env) string {
				return o(e.d.(forge.Memberships).IsActiveMember(e.ctx, fake.Namespace, fake.Member))
			},
			right: "true nil", wrong: "false nil"},
		{fault: fake.FaultANonMemberReadsAsAMember,
			observe: func(e env) string {
				return o(e.d.(forge.Memberships).IsActiveMember(e.ctx, fake.Namespace, fake.NonMember))
			},
			right: "false nil", wrong: "true nil"},
		{fault: fake.FaultAnswersBesideItsError,
			observe: func(e env) string {
				e.f.Break()
				return o(e.d.(forge.Memberships).IsActiveMember(e.ctx, fake.Namespace, fake.NonMember))
			},
			right: "false unreachable", wrong: "true unreachable"},

		{fault: fake.FaultPushReadsTheBaseRef,
			observe: func(e env) string { p, err := e.verified("refs/heads/main", commit); return o(p.Ref, err) },
			right:   "refs/heads/main nil", wrong: "refs/heads/somewhere-else nil"},
		{fault: fake.FaultPushReadsTheCommitItMovedFrom,
			observe: func(e env) string { p, err := e.verified("refs/heads/main", commit); return o(p.Commit, err) },
			right:   commit + " nil", wrong: "9e8d7c6b5a4938271605f4e3d2c1b0a998877665 nil"},
		{fault: fake.FaultAnyDeliveryReadsAsAPush,
			observe: func(e env) string {
				reg, err := e.f.Registry(forge.Policy{})
				require.NoError(e.t, err)
				h := e.hook()
				d, err := e.f.NonPushDelivery(h)
				require.NoError(e.t, err)
				_, err = reg.VerifyPush(h, d)
				return fmt.Sprint(errors.Is(err, forge.ErrNotAPush))
			},
			right: "true", wrong: "false"},
		{fault: fake.FaultSignsWithAnotherScheme,
			observe: func(e env) string { _, err := e.verified("refs/heads/main", commit); return fmt.Sprint(err == nil) },
			right:   "true", wrong: "false"},
		{fault: fake.FaultSignsWithAnotherScheme, shape: shapeOf(fake.GitLabLike),
			observe: func(e env) string { _, err := e.verified("refs/heads/main", commit); return fmt.Sprint(err == nil) },
			right:   "true", wrong: "false"},

		{fault: fake.FaultFollowsRedirect,
			observe: func(e env) string {
				srv, hits := watched(e.t)
				e.f.RedirectOnce(srv.URL, http.StatusFound)
				_, err := e.c.Access(e.ctx, e.k.Writable)
				return o(hits.Load(), err)
			},
			right: "0 other", wrong: "1 nil"},
		{fault: fake.FaultFollowsRedirectOnTheDeploymentCredential,
			observe: func(e env) string {
				srv, hits := watched(e.t)
				e.f.RedirectOnce(srv.URL, http.StatusFound)
				_, err := e.d.(forge.Memberships).IsActiveMember(e.ctx, fake.Namespace, fake.Member)
				return o(hits.Load(), err)
			},
			right: "0 other", wrong: "1 nil"},
		{fault: fake.FaultFollowsSameHostRedirect,
			observe: func(e env) string {
				// Asking about the admin fixture issues its address; a hop to it
				// then acts on that repository whatever the caller named.
				_, err := e.c.Access(e.ctx, e.k.Admin)
				require.NoError(e.t, err)
				e.f.RedirectOnce("https://fake.github/api/repos/acme/site", http.StatusTemporaryRedirect)
				return o(e.c.Access(e.ctx, e.k.ReadOnly))
			},
			right: o(forge.AccessNone, errors.New("refused")), wrong: o(forge.AccessAdmin, nil)},
		{fault: fake.FaultFollowsSameHostRedirect,
			observe: func(e env) string {
				// A 302 turns the write into a GET, so it reports success and
				// creates nothing.
				e.f.RedirectOnce("https://fake.github/api/repos/acme/site", http.StatusFound)
				_, err := e.c.(forge.Creator).Create(e.ctx, e.k.Creatable, forge.CreateOptions{})
				_, found := e.c.Access(e.ctx, e.k.Creatable)
				return kind(err) + " " + kind(found)
			},
			right: "other notfound", wrong: "nil notfound"},
	} {
		sh := fake.GitHubLike
		if tc.shape != nil {
			sh = *tc.shape
		}
		t.Run(string(tc.fault), func(t *testing.T) {
			for _, faulty := range []bool{false, true} {
				s := sh
				want := tc.right
				if faulty {
					s.Faults = []fake.Fault{tc.fault}
					want = tc.wrong
				}
				f := fake.New(s)
				e := env{t: t, f: f, d: f.Driver(), k: f.Keys(), ctx: context.Background()}
				e.c = e.client(f.Token())
				assert.Equal(t, want, tc.observe(e), "faulty=%v", faulty)
			}
		})
	}
}

func TestAFollowedRedirectThatNamesNoRequestIsAnError(t *testing.T) {
	sh := fake.GitHubLike
	sh.Faults = []fake.Fault{fake.FaultFollowsRedirect}
	f := fake.New(sh)
	c, err := f.Driver().Bind(f.Token())
	require.NoError(t, err)
	f.RedirectOnce("http://[::1", http.StatusFound)
	_, err = c.Access(context.Background(), f.Keys().Writable)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a request")
}

func TestAHostRepokeyCannotSpellIsRefused(t *testing.T) {
	defer func() {
		require.NotNil(t, recover(), "a forge was built on a host no key can name")
	}()
	fake.New(fake.Shape{Host: "Not A Host", Scheme: forge.SchemeHubSHA256})
}

func TestTheAdminFacesRefuseWhatIsMissingAndWhatIsNotAdministered(t *testing.T) {
	f := fake.New(fake.GitHubLike)
	c, err := f.Driver().Bind(f.Token())
	require.NoError(t, err)
	ctx, k := context.Background(), f.Keys()
	cb, err := url.Parse("https://cloud.invalid/hook")
	require.NoError(t, err)
	dk, ph := c.(forge.DeployKeys), c.(forge.PushHooks)

	for name, op := range map[string]func() error{
		"install a deploy key": func() error { _, err := dk.InstallDeployKey(ctx, k.Missing, "t", "p"); return err },
		"list deploy keys":     func() error { _, err := dk.ListDeployKeys(ctx, k.Missing); return err },
		"register a hook":      func() error { _, err := ph.RegisterPushHook(ctx, k.Missing, cb, "w"); return err },
		"list hooks":           func() error { _, err := ph.ListPushHooks(ctx, k.Missing); return err },
		"delete":               func() error { return c.(forge.Deleter).Delete(ctx, k.Missing) },
	} {
		var nf *forge.NotFoundError
		require.ErrorAs(t, op(), &nf, "%s on a missing repository", name)
	}
	for name, op := range map[string]func() error{
		"install a deploy key": func() error { _, err := dk.InstallDeployKey(ctx, k.Writable, "t", "p"); return err },
		"list deploy keys":     func() error { _, err := dk.ListDeployKeys(ctx, k.Writable); return err },
		"register a hook":      func() error { _, err := ph.RegisterPushHook(ctx, k.Writable, cb, "w"); return err },
		"list hooks":           func() error { _, err := ph.ListPushHooks(ctx, k.Writable); return err },
	} {
		err := op()
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "needs admin", name)
	}
}
