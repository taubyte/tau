package gitlab

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/forgetest"
)

// baseCapabilities is what this driver can answer for any credential on any
// instance. It is declared here and compared for EQUALITY by the suite, because
// a face method whose signature is one type wrong compiles, fails its type
// assertion in silence and disappears — and only equality names which one went.
//
// CapPushProbe is absent because "may this credential push" is git's question
// and not a forge's; CapMembership is absent unless the driver was given the
// deployment's credential, and that absence is a different Go type rather than a
// method that would have answered false.
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

// TestConformanceSharedToken is a GitLab as almost every instance is configured:
// its webhooks replay a shared token that binds nothing, which is
// forge.SchemeSharedToken, and a deployment must SAY SO by loosening the policy
// before this driver can even be registered. See TestZeroPolicyRefusesAnUnboundScheme.
func TestConformanceSharedToken(t *testing.T) {
	in := newInstance(t)
	forgetest.Run(t, subject(t, in, "gitlab/shared-token",
		in.driver(t),
		forge.Policy{AllowUnboundSchemes: true},
		baseCapabilities()))
}

// TestConformanceSigning is the same driver against the same instance under the
// other of GitLab's two webhook procedures, and it runs under the ZERO POLICY —
// the strict one — because Standard Webhooks signing binds the body and a
// timestamp. One forge, two schemes, one core implementation of each: the driver
// names one and verifies neither.
//
// It is also the driver that holds the deployment's credential, so it is a
// different Go type carrying forge.Memberships, and the suite's exact capability
// comparison is what proves the two types differ in the one way they should.
func TestConformanceSigning(t *testing.T) {
	in := newInstance(t)
	expect := baseCapabilities()
	expect[forge.CapMembership] = struct{}{}

	forgetest.Run(t, subject(t, in, "gitlab/signing+deployment",
		in.driver(t, Signing(), Deployment(forge.Token(deployToken))),
		forge.Policy{},
		expect))
}
