package onedev_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/forgetest"
	"github.com/taubyte/tau/pkg/forge/onedev"
)

// expect is the EXACT capability set, compared for equality by the suite.
//
// CapDeployKeys is absent because OneDev has no repository-scoped key: an SSH
// key belongs to a user and reaches every project that user reaches.
// CapMembership is absent because a parent project is not an organisation
// with members, and there is no deployment credential that would answer it.
// CapPushProbe is absent for the gitlab driver's reason: "may this machine key
// push" is git's question, not a forge's.
func expect() forge.Set {
	return forge.Set{
		forge.CapIdentity:   {},
		forge.CapAccess:     {},
		forge.CapRepository: {},
		forge.CapList:       {},
		forge.CapCreate:     {},
		forge.CapDelete:     {},
		forge.CapCloneAddr:  {},
	}
}

// CapPushHooks and CapSignatures are absent because OneDev cannot sign a
// delivery; see TestPushHooksAreUnsupported.
//
// TestConformance runs the suite under the zero policy, the only one.
func TestConformance(t *testing.T) {
	f := newFixture(t)
	reg := f.registry(t, forge.Policy{})
	forgetest.Run(t, f.subject(t, reg))
}

func (f *fixture) registry(t *testing.T, policy forge.Policy) *forge.Registry {
	t.Helper()
	drv, err := onedev.New(f.Host(), onedev.Transport(f.Transport()))
	require.NoError(t, err)
	reg, err := forge.New(policy, forge.Binding{Host: f.Host(), Driver: drv})
	require.NoError(t, err)
	return reg
}

func (f *fixture) subject(t *testing.T, reg *forge.Registry) forgetest.Subject {
	t.Helper()
	return forgetest.Subject{
		Name:     "onedev",
		Registry: reg,
		Host:     f.Host(),
		Expect:   expect(),

		Credential:    forge.Token(goodToken),
		BadCredential: forge.Token(badToken),
		Token:         goodToken,
		BadToken:      badToken,

		Writable:  f.key(t, "acme/pay"),
		ReadOnly:  f.key(t, "acme/public"),
		NoAccess:  f.key(t, "stranger/secret"),
		Missing:   f.key(t, "acme/nope"),
		Admin:     f.key(t, "acme/site"),
		Deletable: f.key(t, "acme/scratch"),
		Creatable: f.key(t, "acme/fresh"),
		// stranger is a project this credential may read and not create under.
		Uncreatable: f.key(t, "stranger/fresh"),
		// OneDev nests projects, so the deep key is addressed and the suite
		// runs the round trip on it.
		Deep:      f.key(t, "acme/team/widget"),
		DeepNests: true,
		// acme/billing resolves to the project now at acme/pay.
		Moved:   f.key(t, "acme/billing"),
		MovedTo: f.key(t, "acme/pay"),

		Requests:     f.Requests,
		Break:        f.Break,
		Unbreak:      f.Unbreak,
		RedirectOnce: f.RedirectOnce,
	}
}
