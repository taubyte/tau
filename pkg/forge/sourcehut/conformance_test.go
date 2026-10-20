package sourcehut_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/forgetest"
	"github.com/taubyte/tau/pkg/forge/sourcehut"
	"github.com/taubyte/tau/pkg/git/repokey"
)

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// ─────────────────────────────── deliveries ───────────────────────────────
//
// A delivery is the answer to the query the hook was REGISTERED with, signed
// with this fixture's own Ed25519 key over body||nonce — which is what
// git.sr.ht's webhook worker does. The binding alias in the body is the one
// the stored query carries, not one recomputed here, so a driver that wrote
// the wrong alias into its query produces deliveries that do not verify.

func (f *fixture) Delivery(h forge.Hook, ref, commit string) (forge.Delivery, error) {
	return f.deliver(h, h.Key, ref, commit, "GIT_POST_RECEIVE")
}

func (f *fixture) DeliveryFor(h forge.Hook, other repokey.Key, ref, commit string) (forge.Delivery, error) {
	return f.deliver(h, other, ref, commit, "GIT_POST_RECEIVE")
}

// NonPushDelivery is a PRE-receive event: signed, real, and about a push that
// has not been accepted.
func (f *fixture) NonPushDelivery(h forge.Hook) (forge.Delivery, error) {
	return f.deliver(h, h.Key, "refs/heads/main", strings.Repeat("a", 40), "GIT_PRE_RECEIVE")
}

func (f *fixture) deliver(h forge.Hook, names repokey.Key, ref, commit, event string) (forge.Delivery, error) {
	id, err := strconv.ParseInt(string(h.Handle), 10, 64)
	if err != nil {
		return forge.Delivery{}, err
	}
	f.mu.Lock()
	rec, ok := f.hooks[id]
	f.mu.Unlock()
	if !ok {
		return forge.Delivery{}, fmt.Errorf("fixture: no hook %q to deliver to", h.Handle)
	}
	wire, err := h.Scheme.Wire(h.Secret)
	if err != nil {
		return forge.Delivery{}, err
	}
	if wire != rec.wire {
		return forge.Delivery{}, fmt.Errorf("fixture: hook %q was registered with a query echoing another binding", h.Handle)
	}
	update := map[string]any{"ref": map[string]any{"name": ref}, "old": map[string]any{"id": strings.Repeat("e", 40)}, "new": map[string]any{"id": commit}}
	if strings.Trim(commit, "0") == "" {
		update["new"] = nil
	}
	body, err := json.Marshal(map[string]any{"data": map[string]any{
		"webhook": map[string]any{
			"uuid":  "6a1f8f9e-0b5c-4d1e-9c3a-2f4b5d6e7f80",
			"event": event,
			"date":  "2026-10-19T00:00:00Z",
			"repository": map[string]any{
				"name":  names.Name,
				"owner": map[string]any{"canonicalName": names.Owner},
			},
			"updates": []any{update},
		},
		rec.wire: "Query",
	}})
	if err != nil {
		return forge.Delivery{}, err
	}
	return forge.Delivery{Header: forge.SignPayload(f.priv, body, "nonce-"+string(h.Handle)), Body: body}, nil
}

// ─────────────────────────────── the suite ───────────────────────────────

func TestConformance(t *testing.T) {
	f := newFixture(t)
	drv, err := sourcehut.New(f.Host(), sourcehut.Transport(f.Transport()), sourcehut.PayloadKey(f.PublicKey()))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: f.Host(), Driver: drv})
	require.NoError(t, err)
	forgetest.Run(t, f.subject(t, reg, forge.CapSignatures))
}

// TestConformanceWithoutAKey: an instance whose signing key is not configured
// cannot verify a delivery, so it has no Signatures face and registering a hook
// is *UnsupportedError — never a hook nothing can check.
func TestConformanceWithoutAKey(t *testing.T) {
	f := newFixture(t)
	drv, err := sourcehut.New(f.Host(), sourcehut.Transport(f.Transport()))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: f.Host(), Driver: drv})
	require.NoError(t, err)
	forgetest.Run(t, f.subject(t, reg))
}

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
	}
	for _, c := range extra {
		expect[c] = struct{}{}
	}
	return forgetest.Subject{
		Name:     "sourcehut",
		Registry: reg,
		Host:     f.Host(),
		Expect:   expect,

		Credential:    forge.Token(goodToken),
		BadCredential: forge.Token(badToken),
		Token:         goodToken,
		BadToken:      badToken,

		// Admin and Deletable are the credential's own, which is the only
		// admin sourcehut has. Writable is too; see the fixture for why.
		Writable:    f.key(t, "~tau/pay"),
		ReadOnly:    f.key(t, "~acme/public"),
		NoAccess:    f.key(t, "~stranger/secret"),
		Missing:     f.key(t, "~acme/nope"),
		Admin:       f.key(t, "~tau/site"),
		Deletable:   f.key(t, "~tau/scratch"),
		Creatable:   f.key(t, "~tau/fresh"),
		Uncreatable: f.key(t, "~stranger/fresh"),
		Deep:        f.key(t, "~acme/team/widget"),
		DeepNests:   false,
		Moved:       f.key(t, "~tau/billing"),
		MovedTo:     f.key(t, "~tau/pay"),

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
	}
}
