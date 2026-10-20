package bitbucketserver_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/bitbucketserver"
	"github.com/taubyte/tau/pkg/forge/forgetest"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// ─────────────────────────────── deliveries ───────────────────────────────
//
// REAL deliveries, real HMAC over real bytes, in Bitbucket Server's own
// repo:refs_changed shape, keyed by the wire string the driver handed it.

func (f *fixture) Delivery(h forge.Hook, ref, commit string) (forge.Delivery, error) {
	return f.deliver(h, h.Key, ref, commit, "repo:refs_changed")
}

func (f *fixture) DeliveryFor(h forge.Hook, other repokey.Key, ref, commit string) (forge.Delivery, error) {
	return f.deliver(h, other, ref, commit, "repo:refs_changed")
}

func (f *fixture) NonPushDelivery(h forge.Hook) (forge.Delivery, error) {
	return f.deliver(h, h.Key, "refs/heads/main", strings.Repeat("a", 40), "pr:opened")
}

func pushBody(names repokey.Key, ref, commit string) ([]byte, error) {
	kind, display := "", ""
	switch {
	case strings.HasPrefix(ref, "refs/heads/"):
		kind, display = "BRANCH", strings.TrimPrefix(ref, "refs/heads/")
	case strings.HasPrefix(ref, "refs/tags/"):
		kind, display = "TAG", strings.TrimPrefix(ref, "refs/tags/")
	default:
		return nil, fmt.Errorf("fixture: Bitbucket reports branches and tags, and %q is neither", ref)
	}
	change := map[string]any{
		"ref":   map[string]any{"id": ref, "displayId": display, "type": kind},
		"refId": ref, "fromHash": strings.Repeat("b", 40), "toHash": commit, "type": "UPDATE",
	}
	if commit == "" || strings.Trim(commit, "0") == "" {
		change["toHash"], change["type"] = strings.Repeat("0", 40), "DELETE"
	}
	upper := strings.ToUpper(names.Owner)
	return json.Marshal(map[string]any{
		"eventKey": "repo:refs_changed",
		"date":     "2026-10-20T10:00:00+0000",
		"actor":    map[string]any{"name": login, "slug": login, "id": userID},
		"repository": map[string]any{
			"slug": names.Name, "name": names.Name, "scmId": "git",
			"project": map[string]any{"key": upper, "name": upper},
		},
		"changes": []any{change},
	})
}

func (f *fixture) deliver(h forge.Hook, names repokey.Key, ref, commit, event string) (forge.Delivery, error) {
	f.mu.Lock()
	rec, ok := f.hooks[string(h.Handle)]
	f.mu.Unlock()
	if !ok {
		return forge.Delivery{}, fmt.Errorf("fixture: no hook %q to deliver to", h.Handle)
	}
	wire, err := h.Scheme.Wire(h.Secret)
	if err != nil {
		return forge.Delivery{}, err
	}
	if wire != rec.wire {
		return forge.Delivery{}, fmt.Errorf("fixture: hook %q holds a secret the scheme does not derive", h.Handle)
	}
	body, err := pushBody(names, ref, commit)
	if err != nil {
		return forge.Delivery{}, err
	}
	hdr, err := forge.Sign(h.Scheme, h.Secret, body, "delivery-"+string(h.Handle), time.Now())
	if err != nil {
		return forge.Delivery{}, err
	}
	hdr.Set("X-Event-Key", event)
	hdr.Set("X-Request-Id", "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0")
	hdr.Set("User-Agent", "Atlassian HttpClient")
	hdr.Set("Content-Type", "application/json; charset=utf-8")
	return forge.Delivery{Header: hdr, Body: body}, nil
}

// ─────────────────────────────── the suite ───────────────────────────────

func newDriver(t *testing.T, f *fixture, opts ...bitbucketserver.Option) forge.Driver {
	t.Helper()
	drv, err := bitbucketserver.New(f.Host(), append([]bitbucketserver.Option{bitbucketserver.Transport(f.Transport())}, opts...)...)
	require.NoError(t, err)
	return drv
}

func registry(t *testing.T, f *fixture) *forge.Registry {
	t.Helper()
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: f.Host(), Driver: newDriver(t, f)})
	require.NoError(t, err)
	return reg
}

// TestConformance runs the whole suite. Membership and the push probe are
// absent, for the reasons the package comment gives, and the suite asserts
// each answers *forge.UnsupportedError.
func TestConformance(t *testing.T) {
	f := newFixture(t)
	forgetest.Run(t, f.subject(t, registry(t, f)))
}

func (f *fixture) subject(t *testing.T, reg *forge.Registry) forgetest.Subject {
	t.Helper()
	return forgetest.Subject{
		Name:     "bitbucket-server",
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

		Writable:    f.key(t, "acme/pay"),
		ReadOnly:    f.key(t, "acme/public"),
		NoAccess:    f.key(t, "stranger/secret"),
		Missing:     f.key(t, "acme/nope"),
		Admin:       f.key(t, "acme/site"),
		Deletable:   f.key(t, "acme/scratch"),
		Creatable:   f.key(t, "acme/fresh"),
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
