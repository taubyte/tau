package bitbucket_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/bitbucket"
	"github.com/taubyte/tau/pkg/forge/forgetest"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// ─────────────────────────────── deliveries ───────────────────────────────
//
// The fixture signs REAL deliveries with real HMAC over real bytes, in
// Bitbucket Cloud's own repo:push payload shape, keyed by the wire string the
// driver actually handed it.

func (f *fixture) Delivery(h forge.Hook, ref, commit string) (forge.Delivery, error) {
	return f.deliver(h, h.Key, ref, commit, "repo:push", time.Now())
}

func (f *fixture) DeliveryFor(h forge.Hook, other repokey.Key, ref, commit string) (forge.Delivery, error) {
	return f.deliver(h, other, ref, commit, "repo:push", time.Now())
}

func (f *fixture) NonPushDelivery(h forge.Hook) (forge.Delivery, error) {
	return f.deliver(h, h.Key, "refs/heads/main", strings.Repeat("a", 40), "repo:commit_status_created", time.Now())
}

// refSide is one side of a Bitbucket ref change, from a full ref name.
func refSide(ref, commit string) (map[string]any, error) {
	kind, name := "", ""
	switch {
	case strings.HasPrefix(ref, "refs/heads/"):
		kind, name = "branch", strings.TrimPrefix(ref, "refs/heads/")
	case strings.HasPrefix(ref, "refs/tags/"):
		kind, name = "tag", strings.TrimPrefix(ref, "refs/tags/")
	default:
		return nil, fmt.Errorf("fixture: Bitbucket reports branches and tags, and %q is neither", ref)
	}
	return map[string]any{
		"type":   kind,
		"name":   name,
		"target": map[string]any{"type": "commit", "hash": commit},
	}, nil
}

func (f *fixture) pushBody(names repokey.Key, ref, commit string) ([]byte, error) {
	full := names.FullName()
	change := map[string]any{"forced": false, "closed": false, "created": false}
	if commit == "" || strings.Trim(commit, "0") == "" {
		// A deletion: Bitbucket sends "new": null and the ref that went as "old".
		old, err := refSide(ref, strings.Repeat("b", 40))
		if err != nil {
			return nil, err
		}
		change["new"], change["old"], change["closed"] = nil, old, true
	} else {
		side, err := refSide(ref, commit)
		if err != nil {
			return nil, err
		}
		change["new"], change["old"] = side, nil
		change["created"] = true
	}
	return json.Marshal(map[string]any{
		"actor": map[string]any{"type": "user", "username": login, "uuid": userUUID},
		"repository": map[string]any{
			"type":      "repository",
			"full_name": full,
			"name":      names.Name,
			"uuid":      "{0a1b2c3d-" + names.Name + "}",
			"links":     map[string]any{"html": map[string]string{"href": "https://" + f.host + "/" + full}},
		},
		"push": map[string]any{"changes": []any{change}},
	})
}

func (f *fixture) deliver(h forge.Hook, names repokey.Key, ref, commit, event string, at time.Time) (forge.Delivery, error) {
	f.mu.Lock()
	rec, ok := f.hooks[string(h.Handle)]
	f.mu.Unlock()
	if !ok {
		return forge.Delivery{}, fmt.Errorf("fixture: no hook %q to deliver to", h.Handle)
	}
	// The forge holds the WIRE string and keys its MAC with it.
	wire, err := h.Scheme.Wire(h.Secret)
	if err != nil {
		return forge.Delivery{}, err
	}
	if wire != rec.wire {
		return forge.Delivery{}, fmt.Errorf("fixture: hook %q holds a secret the scheme does not derive; nothing signed with it could verify", h.Handle)
	}

	body, err := f.pushBody(names, ref, commit)
	if err != nil {
		return forge.Delivery{}, err
	}
	hdr, err := forge.Sign(h.Scheme, h.Secret, body, "delivery-"+string(h.Handle), at)
	if err != nil {
		return forge.Delivery{}, err
	}
	hdr.Set("X-Event-Key", event)
	hdr.Set("X-Request-UUID", "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0")
	hdr.Set("X-Hook-UUID", strings.Trim(string(h.Handle), "{}"))
	hdr.Set("X-Attempt-Number", "1")
	hdr.Set("User-Agent", "Bitbucket-Webhooks/2.0")
	hdr.Set("Content-Type", "application/json")
	return forge.Delivery{Header: hdr, Body: body}, nil
}

// ─────────────────────────────── the suite ───────────────────────────────

// TestConformance runs the whole suite. Membership is absent from Expect on
// purpose: Bitbucket addresses a workspace member by UUID or account id and
// never by username, so the face is not implemented and the suite asserts it
// answers *forge.UnsupportedError and never false.
func TestConformance(t *testing.T) {
	f := newFixture(t)
	drv, err := bitbucket.New(f.Host(), bitbucket.Transport(f.Transport()))
	require.NoError(t, err)

	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: f.Host(), Driver: drv})
	require.NoError(t, err)

	forgetest.Run(t, f.subject(t, reg))
}

// subject describes this driver to the suite. Expect is the EXACT set.
func (f *fixture) subject(t *testing.T, reg *forge.Registry) forgetest.Subject {
	t.Helper()
	return forgetest.Subject{
		Name:     "bitbucket",
		Registry: reg,
		Host:     f.Host(),
		Expect: forge.Set{
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
		},

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
		// stranger is a workspace this credential may not create in.
		Uncreatable: f.key(t, "stranger/fresh"),
		Deep:        f.key(t, "acme/team/widget"),
		DeepNests:   false,
		Moved:       f.key(t, "acme/billing"),
		MovedTo:     f.key(t, "acme/pay"),

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
