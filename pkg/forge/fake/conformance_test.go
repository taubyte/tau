package fake_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/forge/forgetest"
)

// The fake runs the conformance suite as its claim to behave like a forge for
// every test standing on it.

var optional = []forge.Capability{
	forge.CapList, forge.CapCreate, forge.CapDelete, forge.CapDeployKeys,
	forge.CapPushHooks, forge.CapCloneAddr, forge.CapPushProbe,
	forge.CapSignatures, forge.CapMembership,
}

func TestEveryShapeConforms(t *testing.T) {
	for name, sh := range map[string]fake.Shape{
		"github-like":    fake.GitHubLike,
		"gitlab-like":    fake.GitLabLike,
		"gitea-like":     fake.GiteaLike,
		"bitbucket-like": fake.BitbucketLike,
		"minimal":        fake.Minimal,
	} {
		forgetest.Run(t, subject(t, name, sh))
	}
}

func TestEachAbsenceConforms(t *testing.T) {
	for _, c := range optional {
		sh := fake.GitHubLike
		sh.Omit = append(append([]forge.Capability(nil), sh.Omit...), c)
		forgetest.Run(t, subject(t, "without-"+string(c), sh))
	}
}

func subject(t *testing.T, name string, sh fake.Shape) forgetest.Subject {
	t.Helper()
	f := fake.New(sh)
	reg, err := f.Registry(forge.Policy{})
	require.NoError(t, err)

	// Written from the shape, not read off the driver, so the suite can
	// disagree with it.
	expect := forge.Set{forge.CapIdentity: {}, forge.CapAccess: {}, forge.CapRepository: {}}
	for _, c := range optional {
		omitted := false
		for _, o := range sh.Omit {
			omitted = omitted || o == c
		}
		if !omitted {
			expect[c] = struct{}{}
		}
	}

	k := f.Keys()
	return forgetest.Subject{
		Name:     name,
		Registry: reg,
		Host:     f.Host(),
		Expect:   expect,

		Credential:    f.Token(),
		BadCredential: f.BadToken(),
		Token:         f.TokenString(),
		BadToken:      f.BadTokenString(),

		Writable:    k.Writable,
		ReadOnly:    k.ReadOnly,
		NoAccess:    k.NoAccess,
		Missing:     k.Missing,
		Admin:       k.Admin,
		Deletable:   k.Deletable,
		Creatable:   k.Creatable,
		Uncreatable: k.Uncreatable,
		Deep:        k.Deep,
		DeepNests:   sh.Nested,
		Moved:       k.Moved,
		MovedTo:     k.MovedTo,

		Requests: func() []forgetest.Request {
			raw := f.Requests()
			out := make([]forgetest.Request, 0, len(raw))
			for _, r := range raw {
				out = append(out, forgetest.Request{Method: r.Method, URL: r.URL, Header: r.Header})
			}
			return out
		},
		Break:        f.Break,
		Unbreak:      f.Unbreak,
		RedirectOnce: f.RedirectOnce,

		LastDeployKeyReadOnly: f.LastDeployKeyReadOnly,
		DeployKeyInstalled:    f.DeployKeyInstalled,
		LastHookWire:          f.LastHookWire,
		HookInstalled:         f.HookInstalled,

		Delivery:        f.Delivery,
		DeliveryFor:     f.DeliveryFor,
		NonPushDelivery: f.NonPushDelivery,
		DeliveryAt:      f.DeliveryAt,

		Namespace: fake.Namespace,
		Member:    fake.Member,
		NonMember: fake.NonMember,

		PushKey:   f.PushKey(),
		NoPushKey: f.NoPushKey(),
	}
}
