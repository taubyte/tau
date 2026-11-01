package gerrit_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/forgetest"
	"github.com/taubyte/tau/pkg/forge/gerrit"
)

func TestConformance(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []gerrit.Option
		caps []forge.Capability
	}{
		{
			name: "with the deployment's membership credential",
			opts: []gerrit.Option{gerrit.Membership(memberToken)},
			caps: []forge.Capability{forge.CapMembership},
		},
		{name: "without one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			opts := append([]gerrit.Option{gerrit.Transport(f.Transport())}, tc.opts...)
			drv, err := gerrit.New(f.Host(), opts...)
			require.NoError(t, err)
			reg, err := forge.New(forge.Policy{}, forge.Binding{Host: f.Host(), Driver: drv})
			require.NoError(t, err)
			forgetest.Run(t, f.subject(t, reg, tc.caps...))
		})
	}
}

// Expect is the exact set: Gerrit has no answer for deploy keys, push hooks,
// signatures or the push probe.
func (f *fixture) subject(t *testing.T, reg *forge.Registry, extra ...forge.Capability) forgetest.Subject {
	t.Helper()
	expect := forge.Set{
		forge.CapIdentity:   {},
		forge.CapAccess:     {},
		forge.CapRepository: {},
		forge.CapList:       {},
		forge.CapCreate:     {},
		forge.CapDelete:     {},
		forge.CapCloneAddr:  {},
	}
	for _, c := range extra {
		expect[c] = struct{}{}
	}
	return forgetest.Subject{
		Name:     "gerrit",
		Registry: reg,
		Host:     f.Host(),
		Expect:   expect,

		Credential:    forge.Token(goodToken),
		BadCredential: forge.Token(badToken),
		Token:         goodToken,
		BadToken:      badToken,

		// Writable is OWNED: Gerrit reports ownership and not Push, so owning
		// is the only grant this driver reads as write.
		Writable:    f.key(t, "acme/pay"),
		ReadOnly:    f.key(t, "acme/public"),
		NoAccess:    f.key(t, "stranger/secret"),
		Missing:     f.key(t, "acme/nope"),
		Admin:       f.key(t, "acme/site"),
		Deletable:   f.key(t, "acme/scratch"),
		Creatable:   f.key(t, "acme/fresh"),
		Uncreatable: f.key(t, "stranger/fresh"),
		// A Gerrit project name is a path, so a nested key is a project.
		Deep:      f.key(t, "acme/team/widget"),
		DeepNests: true,
		Moved:     f.key(t, "acme/billing"),
		MovedTo:   f.key(t, "acme/pay"),

		Requests:     f.Requests,
		Break:        f.Break,
		Unbreak:      f.Unbreak,
		RedirectOnce: f.RedirectOnce,

		Namespace: namespace,
		Member:    member,
		NonMember: nonMember,
	}
}
