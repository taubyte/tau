package azure_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/azure"
	"github.com/taubyte/tau/pkg/forge/forgetest"
)

// ─────────────────────────────── the suite ───────────────────────────────

func newDriver(t *testing.T, f *fixture, opts ...azure.Option) forge.Driver {
	t.Helper()
	all := append([]azure.Option{azure.Transport(f.Transport()), azure.Organizations(org)}, opts...)
	drv, err := azure.New(f.Host(), all...)
	require.NoError(t, err)
	return drv
}

// TestConformance runs the whole suite under the zero policy.
func TestConformance(t *testing.T) {
	f := newFixture(t)
	drv := newDriver(t, f)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: f.Host(), Driver: drv})
	require.NoError(t, err)
	forgetest.Run(t, f.subject(t, reg))
}

func (f *fixture) subject(t *testing.T, reg *forge.Registry) forgetest.Subject {
	t.Helper()
	return forgetest.Subject{
		Name:     "azure",
		Registry: reg,
		Host:     f.Host(),
		// Exact: deploy keys, membership, the push probe and hooks are absent, each
		// for the reason the package comment gives.
		Expect: forge.Set{
			forge.CapIdentity:   {},
			forge.CapAccess:     {},
			forge.CapRepository: {},
			forge.CapList:       {},
			forge.CapCreate:     {},
			forge.CapDelete:     {},
			forge.CapCloneAddr:  {},
		},

		Credential:    forge.Token(goodToken),
		BadCredential: forge.Token(badToken),
		Token:         goodToken,
		BadToken:      badToken,

		Writable:    f.key(t, "acme/pay/_git/pay"),
		ReadOnly:    f.key(t, "acme/public/_git/public"),
		NoAccess:    f.key(t, "acme/secret/_git/secret"),
		Missing:     f.key(t, "acme/pay/_git/nope"),
		Admin:       f.key(t, "acme/site/_git/site"),
		Deletable:   f.key(t, "acme/scratch/_git/scratch"),
		Creatable:   f.key(t, "acme/pay/_git/fresh"),
		Uncreatable: f.key(t, "acme/locked/_git/fresh"),
		// Not the {org}/{project}/_git/{repo} shape: refused before the wire.
		Deep:      f.key(t, "acme/team/widget"),
		DeepNests: false,
		Moved:     f.key(t, "acme/pay/_git/billing"),
		MovedTo:   f.key(t, "acme/pay/_git/pay"),

		Requests:     f.Requests,
		Break:        f.Break,
		Unbreak:      f.Unbreak,
		RedirectOnce: f.RedirectOnce,
	}
}
