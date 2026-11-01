package gitlab

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/forgetest"
)

// baseCapabilities is compared for equality by the suite. CapPushProbe is git's
// question; CapMembership needs the deployment credential, a different Go type.
func baseCapabilities() forge.Set {
	return forge.Set{
		forge.CapIdentity:   {},
		forge.CapAccess:     {},
		forge.CapRepository: {},
		forge.CapList:       {},
		forge.CapCreate:     {},
		forge.CapDelete:     {},
		forge.CapDeployKeys: {},
		forge.CapPushHooks:  {},
		forge.CapCloneAddr:  {},
		forge.CapSignatures: {},
		forge.CapSignIn:     {},
	}
}

func subject(t *testing.T, in *instance, name string, driver forge.Driver, policy forge.Policy, expect forge.Set) forgetest.Subject {
	t.Helper()
	registry, err := forge.New(policy, forge.Binding{Host: in.host, Driver: driver})
	require.NoError(t, err)

	fixtures := in.keys(t)
	return forgetest.Subject{
		Name:     name,
		Registry: registry,
		Host:     in.host,
		Expect:   expect,

		Credential:    forge.Token(goodToken),
		BadCredential: forge.Token(badToken),
		Token:         goodToken,
		BadToken:      badToken,

		Writable:    fixtures.Writable,
		ReadOnly:    fixtures.ReadOnly,
		NoAccess:    fixtures.NoAccess,
		Missing:     fixtures.Missing,
		Admin:       fixtures.Admin,
		Deletable:   fixtures.Deletable,
		Creatable:   fixtures.Creatable,
		Uncreatable: fixtures.Uncreatable,
		Deep:        fixtures.Deep,
		// THE ANSWER THIS DRIVER EXISTS TO GIVE. GitLab nests subgroups, so a
		// nested owner is addressed rather than refused — and the suite then runs
		// the whole round trip against it.
		DeepNests: true,
		// acme/billing is the project this instance answers for and does not
		// answer as: a driver that echoes the key it was handed calls this
		// repository acme/billing, which is not what the instance just said it
		// is.
		Moved:   fixtures.Moved,
		MovedTo: fixtures.MovedTo,

		Requests:     in.rec.drain,
		Break:        func() { in.rec.setBroken(true) },
		Unbreak:      func() { in.rec.setBroken(false) },
		RedirectOnce: in.redirectOnce,

		LastDeployKeyReadOnly: in.lastDeployKeyReadOnly,
		DeployKeyInstalled:    in.deployKeyInstalled,
		LastHookWire:          in.lastHookWire,
		HookInstalled:         in.hookInstalled,

		Delivery:        in.Delivery,
		DeliveryFor:     in.DeliveryFor,
		NonPushDelivery: in.NonPushDelivery,
		DeliveryAt:      in.DeliveryAt,

		Namespace: fixtureGroup,
		Member:    fixtureMember,
		NonMember: fixtureNonMember,
	}
}

// unsigned is what a driver for an instance that does not sign has: no hook
// faces, because the only thing such an instance offers is a replayed token
// that binds nothing.
func unsigned(s forge.Set) forge.Set {
	delete(s, forge.CapPushHooks)
	delete(s, forge.CapSignatures)
	return s
}

func withMembership(s forge.Set) forge.Set {
	s[forge.CapMembership] = struct{}{}
	return s
}

func TestConformance(t *testing.T) {
	deploy := Deployment(forge.Token(deployToken))
	for _, tc := range []struct {
		name   string
		opts   []Option
		expect forge.Set
	}{
		{"gitlab/unsigned", nil, unsigned(baseCapabilities())},
		{"gitlab/unsigned+deployment", []Option{deploy}, withMembership(unsigned(baseCapabilities()))},
		{"gitlab/signing", []Option{Signing()}, baseCapabilities()},
		{"gitlab/signing+deployment", []Option{Signing(), deploy}, withMembership(baseCapabilities())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := newInstance(t)
			forgetest.Run(t, subject(t, in, tc.name, in.driver(t, tc.opts...), forge.Policy{}, tc.expect))
		})
	}
}
