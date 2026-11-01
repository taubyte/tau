package forgetest

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
)

// subjectFor is the adapter a driver's own tests write once, wiring a forge's
// handles into a Subject.
func subjectFor(t *testing.T, name string, sh fake.Shape) Subject {
	t.Helper()
	f := fake.New(sh)

	reg, err := f.Registry(forge.Policy{})
	require.NoError(t, err)

	k := f.Keys()
	return Subject{
		Name:     name,
		Registry: reg,
		Host:     f.Host(),
		Expect:   expected(sh),

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

		Requests: func() []Request {
			raw := f.Requests()
			out := make([]Request, 0, len(raw))
			for _, r := range raw {
				out = append(out, Request{Method: r.Method, URL: r.URL, Header: r.Header})
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

// expected is the capability set a shape must compute to. It is written here
// from the SHAPE and not read off the driver, so that Expect is a claim the
// suite can disagree with rather than a copy of the answer.
func expected(sh fake.Shape) forge.Set {
	set := forge.Set{
		forge.CapIdentity:   {},
		forge.CapAccess:     {},
		forge.CapRepository: {},
	}
	for _, c := range []forge.Capability{
		forge.CapList, forge.CapCreate, forge.CapDelete, forge.CapDeployKeys,
		forge.CapPushHooks, forge.CapCloneAddr, forge.CapPushProbe,
		forge.CapSignatures, forge.CapMembership,
	} {
		omitted := false
		for _, o := range sh.Omit {
			if o == c {
				omitted = true
			}
		}
		if !omitted {
			set[c] = struct{}{}
		}
	}
	return set
}

// without copies a shape with one capability removed, which is how each
// optional capability gets a run of BOTH its contracts: present on the full
// shapes, absent here.
func without(sh fake.Shape, c forge.Capability) fake.Shape {
	sh.Omit = append(append([]forge.Capability(nil), sh.Omit...), c)
	return sh
}

func faulty(sh fake.Shape, f fake.Fault) fake.Shape {
	sh.Faults = append(append([]fake.Fault(nil), sh.Faults...), f)
	return sh
}
