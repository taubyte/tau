package gitea_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/forgetest"
	"github.com/taubyte/tau/pkg/forge/gitea"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// The fixture signs real deliveries with real HMAC, in Gitea's push-payload
// shape, keyed by the wire string the driver handed it.

func (f *fixture) Delivery(h forge.Hook, ref, commit string) (forge.Delivery, error) {
	return f.deliver(h, h.Key, ref, commit, "push", time.Now())
}

// DeliveryFor names a DIFFERENT repository under this hook's own secret and
// scheme — a delivery that is perfectly signed and speaks for somebody else's
// repository.
func (f *fixture) DeliveryFor(h forge.Hook, other repokey.Key, ref, commit string) (forge.Delivery, error) {
	return f.deliver(h, other, ref, commit, "push", time.Now())
}

func (f *fixture) NonPushDelivery(h forge.Hook) (forge.Delivery, error) {
	return f.deliver(h, h.Key, "", "", "ping", time.Now())
}

func (f *fixture) deliver(h forge.Hook, names repokey.Key, ref, commit, event string, at time.Time) (forge.Delivery, error) {
	f.mu.Lock()
	rec, ok := f.hooks[string(h.Handle)]
	f.mu.Unlock()
	if !ok {
		return forge.Delivery{}, fmt.Errorf("fixture: no hook %q to deliver to", h.Handle)
	}

	// The forge holds the WIRE string and keys its MAC with it. If the driver
	// handed over something else, no delivery this forge could ever send would
	// verify — so say that here rather than quietly signing with the secret and
	// letting a mangling driver look correct.
	wire, err := h.Scheme.Wire(h.Secret)
	if err != nil {
		return forge.Delivery{}, err
	}
	if wire != rec.wire {
		return forge.Delivery{}, fmt.Errorf("fixture: hook %q holds a secret the scheme does not derive; nothing signed with it could verify", h.Handle)
	}

	full := names.FullName()
	payload := map[string]any{
		"ref":    ref,
		"before": strings.Repeat("0", 40),
		"after":  commit,
		"repository": map[string]any{
			"full_name": full,
			"clone_url": "https://" + f.host + "/" + full + ".git",
			"ssh_url":   "ssh://git@" + f.host + "/" + full + ".git",
		},
	}
	if commit == "" || strings.Trim(commit, "0") == "" {
		// A deletion: Gitea sends the all-zero object id and no head commit.
		payload["head_commit"] = nil
		payload["deleted"] = true
	} else {
		payload["head_commit"] = map[string]any{"id": commit}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return forge.Delivery{}, err
	}

	hdr, err := forge.Sign(h.Scheme, h.Secret, body, "delivery-"+string(h.Handle), at)
	if err != nil {
		return forge.Delivery{}, err
	}
	hdr.Set("X-Gitea-Event", event)
	hdr.Set("X-Gitea-Delivery", "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0")
	hdr.Set("X-Gitea-Hook-ID", string(h.Handle))
	hdr.Set("Content-Type", "application/json")
	return forge.Delivery{Header: hdr, Body: body}, nil
}

func TestConformance(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []gitea.Option
		caps []forge.Capability
	}{
		{
			name: "with the deployment's membership token",
			opts: []gitea.Option{gitea.Membership(memberToken)},
			caps: []forge.Capability{forge.CapMembership},
		},
		{
			name: "without one",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			opts := append([]gitea.Option{gitea.Transport(f.Transport())}, tc.opts...)
			drv, err := gitea.New(f.Host(), opts...)
			require.NoError(t, err)

			reg, err := forge.New(forge.Policy{}, forge.Binding{Host: f.Host(), Driver: drv})
			require.NoError(t, err)

			forgetest.Run(t, f.subject(t, reg, tc.caps...))
		})
	}
}

// subject describes this driver to the suite. Expect is the EXACT set, so a
// face whose method signature is one type wrong — which compiles, and which
// silently drops the capability — fails here naming the one that went missing.
func (f *fixture) subject(t *testing.T, reg *forge.Registry, extra ...forge.Capability) forgetest.Subject {
	t.Helper()
	expect := forge.Set{
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
	for _, c := range extra {
		expect[c] = struct{}{}
	}

	return forgetest.Subject{
		Name:     "gitea",
		Registry: reg,
		Host:     f.Host(),
		Expect:   expect,

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
		// stranger is an organization this credential may not create in.
		Uncreatable: f.key(t, "stranger/fresh"),
		// Gitea addresses exactly one owner and one name, so a nested key is
		// refused before the wire rather than flattened into a name no
		// repository has.
		Deep:      f.key(t, "acme/team/widget"),
		DeepNests: false,
		// acme/billing was renamed to acme/pay and Gitea still answers for it.
		Moved:   f.key(t, "acme/billing"),
		MovedTo: f.key(t, "acme/pay"),

		Requests:     f.Requests,
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

		Namespace: namespace,
		Member:    member,
		NonMember: nonMember,
	}
}
