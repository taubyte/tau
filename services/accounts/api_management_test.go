package accounts

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/streams/command"
)

func dispatchTestService(t *testing.T) *AccountsService {
	srv, _ := loginTestService(t)
	return srv
}

// decodeResponsePayload pulls the first non-`ok` field out of a wire response
// and round-trips it through CBOR into T.
func decodeResponsePayload[T any](t *testing.T, resp map[string]any) T {
	t.Helper()
	var v any
	for k, val := range resp {
		if k == "ok" {
			continue
		}
		v = val
		break
	}
	if v == nil {
		t.Fatalf("response has no payload field: %+v", resp)
	}
	raw, err := cbor.Marshal(v)
	if err != nil {
		t.Fatalf("re-encode response: %v", err)
	}
	var out T
	if err := cbor.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

// encodeBodyPayload spreads `in`'s fields into the body alongside `action`
// and `extras` (CBOR round-trip via the type's struct tags).
func encodeBodyPayload(t *testing.T, action string, in any, extras command.Body) command.Body {
	t.Helper()
	body := command.Body{}
	for k, v := range extras {
		body[k] = v
	}
	body["action"] = action
	if in != nil {
		raw, err := cbor.Marshal(in)
		if err != nil {
			t.Fatalf("encode input: %v", err)
		}
		var fields map[string]any
		if err := cbor.Unmarshal(raw, &fields); err != nil {
			t.Fatalf("decode input to map: %v", err)
		}
		for k, v := range fields {
			body[k] = v
		}
	}
	return body
}

// --- account verb -------------------------------------------------

func TestApiAccountHandler_RoundTrip(t *testing.T) {
	srv := dispatchTestService(t)
	ctx := context.Background()

	// create
	body := encodeBodyPayload(t, "create",
		accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"}, nil)
	resp, err := srv.apiAccountHandler(ctx, nil, body)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	acc := decodeResponsePayload[accountsIface.Account](t, resp)
	if acc.Slug != "acme" {
		t.Fatalf("created slug = %q", acc.Slug)
	}

	// get
	resp, err = srv.apiAccountHandler(ctx, nil, command.Body{"action": "get", "id": acc.ID})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got := decodeResponsePayload[accountsIface.Account](t, resp)
	if got.ID != acc.ID {
		t.Fatalf("get id mismatch: %s vs %s", got.ID, acc.ID)
	}

	// get-by-slug
	resp, err = srv.apiAccountHandler(ctx, nil, command.Body{"action": "get-by-slug", "slug": "acme"})
	if err != nil {
		t.Fatalf("get-by-slug: %v", err)
	}
	if got := decodeResponsePayload[accountsIface.Account](t, resp); got.ID != acc.ID {
		t.Fatalf("get-by-slug id mismatch")
	}

	// list
	resp, err = srv.apiAccountHandler(ctx, nil, command.Body{"action": "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	ids := decodeResponsePayload[[]string](t, resp)
	if len(ids) != 1 || ids[0] != acc.ID {
		t.Fatalf("list: %+v", ids)
	}

	// update
	newName := "Acme Inc."
	body = encodeBodyPayload(t, "update",
		accountsIface.UpdateAccountInput{Name: &newName}, command.Body{"id": acc.ID})
	resp, err = srv.apiAccountHandler(ctx, nil, body)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := decodeResponsePayload[accountsIface.Account](t, resp); got.Name != newName {
		t.Fatalf("update name: %q", got.Name)
	}

	// delete
	resp, err = srv.apiAccountHandler(ctx, nil, command.Body{"action": "delete", "id": acc.ID})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("delete: server did not confirm")
	}
}

func TestApiAccountHandler_BadInput(t *testing.T) {
	srv := dispatchTestService(t)
	ctx := context.Background()

	if _, err := srv.apiAccountHandler(ctx, nil, command.Body{}); err == nil {
		t.Fatalf("expected error for missing action")
	}
	if _, err := srv.apiAccountHandler(ctx, nil, command.Body{"action": "unknown"}); err == nil {
		t.Fatalf("expected error for unknown action")
	}
	if _, err := srv.apiAccountHandler(ctx, nil, command.Body{"action": "get"}); err == nil {
		t.Fatalf("expected error for missing id")
	}
}

// --- plan / member / user / token (smoke tests) -----------------

func TestApiPlanHandler_RoundTrip(t *testing.T) {
	srv := dispatchTestService(t)
	ctx := context.Background()
	cli := newInProcessClient(srv)

	acc, err := cli.Accounts().Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	if err != nil {
		t.Fatalf("Create acc: %v", err)
	}

	body := encodeBodyPayload(t, "create",
		accountsIface.CreatePlanInput{Slug: "prod", Name: "Prod"},
		command.Body{"account_id": acc.ID})
	resp, err := srv.apiPlanHandler(ctx, nil, body)
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	bk := decodeResponsePayload[accountsIface.Plan](t, resp)
	if bk.Slug != "prod" {
		t.Fatalf("create slug")
	}

	resp, err = srv.apiPlanHandler(ctx, nil, command.Body{
		"action": "list", "account_id": acc.ID,
	})
	if err != nil {
		t.Fatalf("list plan: %v", err)
	}
	if ids := decodeResponsePayload[[]string](t, resp); len(ids) != 1 {
		t.Fatalf("expected 1 plan")
	}

	resp, err = srv.apiPlanHandler(ctx, nil, command.Body{
		"action": "get-by-slug", "account_id": acc.ID, "slug": "prod",
	})
	if err != nil {
		t.Fatalf("get-by-slug: %v", err)
	}
	if got := decodeResponsePayload[accountsIface.Plan](t, resp); got.ID != bk.ID {
		t.Fatalf("get-by-slug id")
	}

	if _, err := srv.apiPlanHandler(ctx, nil, command.Body{
		"action": "delete", "account_id": acc.ID, "id": bk.ID,
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestApiMemberHandler_RoundTrip(t *testing.T) {
	srv := dispatchTestService(t)
	ctx := context.Background()
	cli := newInProcessClient(srv)
	acc, _ := cli.Accounts().Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})

	body := encodeBodyPayload(t, "invite",
		accountsIface.InviteMemberInput{PrimaryEmail: "alice@example.com", Role: accountsIface.RoleOwner},
		command.Body{"account_id": acc.ID})
	resp, err := srv.apiMemberHandler(ctx, nil, body)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	m := decodeResponsePayload[accountsIface.Member](t, resp)

	if _, err := srv.apiMemberHandler(ctx, nil, command.Body{
		"action": "list", "account_id": acc.ID,
	}); err != nil {
		t.Fatalf("list: %v", err)
	}

	newRole := accountsIface.RoleViewer
	body = encodeBodyPayload(t, "update",
		accountsIface.UpdateMemberInput{Role: &newRole},
		command.Body{"account_id": acc.ID, "id": m.ID})
	if resp, err := srv.apiMemberHandler(ctx, nil, body); err != nil {
		t.Fatalf("update: %v", err)
	} else if got := decodeResponsePayload[accountsIface.Member](t, resp); got.Role != newRole {
		t.Fatalf("role not updated: %s", got.Role)
	}

	if _, err := srv.apiMemberHandler(ctx, nil, command.Body{
		"action": "remove", "account_id": acc.ID, "id": m.ID,
	}); err != nil {
		t.Fatalf("remove: %v", err)
	}
}

func TestApiUserHandler_RoundTrip(t *testing.T) {
	srv := dispatchTestService(t)
	ctx := context.Background()
	cli := newInProcessClient(srv)
	acc, _ := cli.Accounts().Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	bk, _ := cli.Plans(acc.ID).Create(ctx, accountsIface.CreatePlanInput{Slug: "prod", Name: "Prod"})

	body := encodeBodyPayload(t, "add",
		accountsIface.AddUserInput{Provider: "github", ExternalID: "1"},
		command.Body{"account_id": acc.ID})
	resp, err := srv.apiUserHandler(ctx, nil, body)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	u := decodeResponsePayload[accountsIface.User](t, resp)

	body = encodeBodyPayload(t, "grant",
		accountsIface.GrantPlanInput{PlanID: bk.ID},
		command.Body{"account_id": acc.ID, "id": u.ID})
	if _, err := srv.apiUserHandler(ctx, nil, body); err != nil {
		t.Fatalf("grant: %v", err)
	}

	if _, err := srv.apiUserHandler(ctx, nil, command.Body{
		"action": "get-by-external", "account_id": acc.ID,
		"provider": "github", "external_id": "1",
	}); err != nil {
		t.Fatalf("get-by-external: %v", err)
	}

	if _, err := srv.apiUserHandler(ctx, nil, command.Body{
		"action": "revoke", "account_id": acc.ID, "id": u.ID, "plan_id": bk.ID,
	}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
}

// --- login verb ---------------------------------------------------

func TestApiLoginHandler_StartManagedAndMagicFinish(t *testing.T) {
	srv := dispatchTestService(t)
	ctx := context.Background()
	cli := newInProcessClient(srv)
	acc, _ := cli.Accounts().Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	_, _ = cli.Members(acc.ID).Invite(ctx, accountsIface.InviteMemberInput{
		PrimaryEmail: "alice@example.com", Role: accountsIface.RoleOwner,
	})

	body := encodeBodyPayload(t, "start-managed",
		accountsIface.StartManagedLoginInput{Email: "alice@example.com"}, nil)
	resp, err := srv.apiLoginHandler(ctx, nil, body)
	if err != nil {
		t.Fatalf("start-managed: %v", err)
	}
	chal := decodeResponsePayload[accountsIface.ManagedLoginChallenge](t, resp)
	if !chal.MagicLinkSent {
		t.Fatalf("expected magic-link path")
	}

	// Verify session round-trip via direct issue (we don't have the magic
	// code here in test; just confirm verify-session and logout work with a
	// freshly issued bearer).
	sess, bearer, err := srv.sessions.Issue(ctx, acc.ID, "mem-1")
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}

	resp, err = srv.apiLoginHandler(ctx, nil, command.Body{
		"action": "verify-session", "token": bearer,
	})
	if err != nil {
		t.Fatalf("verify-session: %v", err)
	}
	got := decodeResponsePayload[accountsIface.Session](t, resp)
	if got.AccountID != sess.AccountID {
		t.Fatalf("verify-session account mismatch")
	}

	if _, err := srv.apiLoginHandler(ctx, nil, command.Body{
		"action": "logout", "token": bearer,
	}); err != nil {
		t.Fatalf("logout: %v", err)
	}

	// After logout, verify-session should fail.
	if _, err := srv.apiLoginHandler(ctx, nil, command.Body{
		"action": "verify-session", "token": bearer,
	}); err == nil {
		t.Fatalf("expected verify-session to fail after logout")
	}
}

func TestApiLoginHandler_BadInputs(t *testing.T) {
	srv := dispatchTestService(t)
	ctx := context.Background()

	if _, err := srv.apiLoginHandler(ctx, nil, command.Body{}); err == nil {
		t.Fatalf("expected error for missing action")
	}
	if _, err := srv.apiLoginHandler(ctx, nil, command.Body{"action": "unknown"}); err == nil {
		t.Fatalf("expected error for unknown action")
	}
}

// --- requireAccountID + helpers ----------------------------------

func TestRequireAccountID(t *testing.T) {
	if _, err := requireAccountID(command.Body{}); err == nil {
		t.Fatalf("expected error for missing account_id")
	}
	if _, err := requireAccountID(command.Body{"account_id": ""}); err == nil {
		t.Fatalf("expected error for empty account_id")
	}
	if id, err := requireAccountID(command.Body{"account_id": "x"}); err != nil || id != "x" {
		t.Fatalf("requireAccountID returned %q %v", id, err)
	}
}

func TestWireHelpers(t *testing.T) {
	type sample struct {
		A string `cbor:"a"`
	}
	var got sample
	if err := decodeField(map[string]any{"a": "ok"}, &got); err != nil {
		t.Fatalf("decodeField: %v", err)
	}
	if got.A != "ok" {
		t.Fatalf("decodeField roundtrip lost A: %+v", got)
	}

	if s, ok := optString(command.Body{"k": "v"}, "k"); !ok || s != "v" {
		t.Fatalf("optString happy: %q %v", s, ok)
	}
	if _, ok := optString(command.Body{}, "missing"); ok {
		t.Fatalf("optString missing should return false")
	}
}

func TestApiLoginHandler_FinishMagic_NotFoundPath(t *testing.T) {
	srv := dispatchTestService(t)
	ctx := context.Background()
	body := encodeBodyPayload(t, "finish-magic",
		accountsIface.FinishMagicLinkInput{Code: "ghost"}, nil)
	if _, err := srv.apiLoginHandler(ctx, nil, body); err == nil {
		t.Fatalf("expected not-found error for ghost code")
	}
}

// Confirm bearer-token format contains the prefix (sanity check on the
// session encoding wired through the api_login handler).
func TestSessionTokenViaWire(t *testing.T) {
	srv := dispatchTestService(t)
	ctx := context.Background()

	_, bearer, err := srv.sessions.Issue(ctx, "acct-1", "mem-1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !strings.HasPrefix(bearer, "tau-session.") {
		t.Fatalf("bad bearer prefix: %s", bearer)
	}

	// Round-trip via the verify-session handler.
	resp, err := srv.apiLoginHandler(ctx, nil, command.Body{
		"action": "verify-session", "token": bearer,
	})
	if err != nil {
		t.Fatalf("verify-session: %v", err)
	}
	sess := decodeResponsePayload[accountsIface.Session](t, resp)
	if sess.AccountID != "acct-1" {
		t.Fatalf("verify-session account: %q", sess.AccountID)
	}
}

// Quick sanity check that ManagedLoginChallenge marshalling preserves
// SessionID + WebAuthnChallenge bytes when a passkey path would be hit.
func TestManagedLoginChallenge_JSONShape(t *testing.T) {
	in := accountsIface.ManagedLoginChallenge{
		SessionID:         "abc",
		WebAuthnChallenge: []byte(`{"challenge":"xyz"}`),
		MagicLinkSent:     false,
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out accountsIface.ManagedLoginChallenge
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.SessionID != "abc" {
		t.Fatalf("session_id lost: %q", out.SessionID)
	}
	if string(out.WebAuthnChallenge) != `{"challenge":"xyz"}` {
		t.Fatalf("webauthn_challenge lost")
	}
}

// Defensive: verify the top-level stream verbs are non-empty (catch a typo
// in the constants without needing wire integration).
func TestStreamVerbConstants(t *testing.T) {
	for name, v := range map[string]string{
		"account": StreamVerbAccount, "member": StreamVerbMember,
		"user": StreamVerbUser, "plan": StreamVerbPlan,
		"login":  StreamVerbLogin,
		"verify": StreamVerbVerify, "resolve": StreamVerbResolve,
	} {
		if v == "" {
			t.Errorf("StreamVerb%s is empty", name)
		}
	}
}

// Hit the remaining action branches across each verb to drive coverage. The
// happy paths above exercised create/list/get; these fill in the rest
// (get-by-slug, update, get-by-external, etc.).
func TestApiHandlers_AdditionalBranches(t *testing.T) {
	srv := dispatchTestService(t)
	ctx := context.Background()
	cli := newInProcessClient(srv)
	acc, _ := cli.Accounts().Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	bk, _ := cli.Plans(acc.ID).Create(ctx, accountsIface.CreatePlanInput{Slug: "prod", Name: "Prod"})
	u, _ := cli.Users(acc.ID).Add(ctx, accountsIface.AddUserInput{Provider: "github", ExternalID: "1"})
	m, _ := cli.Members(acc.ID).Invite(ctx, accountsIface.InviteMemberInput{PrimaryEmail: "alice@x"})

	// account: missing-slug + get-not-found
	if _, err := srv.apiAccountHandler(ctx, nil, command.Body{"action": "get-by-slug"}); err == nil {
		t.Fatalf("expected error for missing slug")
	}
	if _, err := srv.apiAccountHandler(ctx, nil, command.Body{"action": "get-by-slug", "slug": "ghost"}); err == nil {
		t.Fatalf("expected not-found")
	}
	if _, err := srv.apiAccountHandler(ctx, nil, command.Body{"action": "delete"}); err == nil {
		t.Fatalf("expected error for missing id on delete")
	}
	if _, err := srv.apiAccountHandler(ctx, nil, command.Body{"action": "update"}); err == nil {
		t.Fatalf("expected error for missing id on update")
	}

	// plan: get + update + delete missing-id
	if _, err := srv.apiPlanHandler(ctx, nil, command.Body{
		"action": "get", "account_id": acc.ID, "id": bk.ID,
	}); err != nil {
		t.Fatalf("plan get: %v", err)
	}
	newName := "Production"
	body := encodeBodyPayload(t, "update",
		accountsIface.UpdatePlanInput{Name: &newName},
		command.Body{"account_id": acc.ID, "id": bk.ID})
	if _, err := srv.apiPlanHandler(ctx, nil, body); err != nil {
		t.Fatalf("plan update: %v", err)
	}
	for _, action := range []string{"get", "get-by-slug", "update", "delete"} {
		if _, err := srv.apiPlanHandler(ctx, nil, command.Body{"action": action, "account_id": acc.ID}); err == nil {
			t.Fatalf("plan %s should require id/slug", action)
		}
	}
	if _, err := srv.apiPlanHandler(ctx, nil, command.Body{"action": "get"}); err == nil {
		t.Fatalf("plan missing account_id")
	}

	// member: get
	if _, err := srv.apiMemberHandler(ctx, nil, command.Body{
		"action": "get", "account_id": acc.ID, "id": m.ID,
	}); err != nil {
		t.Fatalf("member get: %v", err)
	}
	for _, action := range []string{"get", "update", "remove"} {
		if _, err := srv.apiMemberHandler(ctx, nil, command.Body{"action": action, "account_id": acc.ID}); err == nil {
			t.Fatalf("member %s should require id", action)
		}
	}

	// user: get + remove + missing-id branches
	if _, err := srv.apiUserHandler(ctx, nil, command.Body{
		"action": "get", "account_id": acc.ID, "id": u.ID,
	}); err != nil {
		t.Fatalf("user get: %v", err)
	}
	for _, action := range []string{"get", "remove", "grant", "revoke", "get-by-external"} {
		if _, err := srv.apiUserHandler(ctx, nil, command.Body{"action": action, "account_id": acc.ID}); err == nil {
			t.Fatalf("user %s should reject missing fields", action)
		}
	}

	// login: start-external (errors with EE-required), finish-external (errors).
	if _, err := srv.apiLoginHandler(ctx, nil, command.Body{
		"action": "start-external", "account_slug": "acme",
	}); err == nil {
		t.Fatalf("start-external should error in v1")
	}
	if _, err := srv.apiLoginHandler(ctx, nil, encodeBodyPayload(t, "finish-external",
		accountsIface.FinishExternalLoginInput{Code: "x", State: "y"}, nil)); err == nil {
		t.Fatalf("finish-external should error in v1")
	}
	// finish-passkey: bad assertion bytes → ParseCredentialRequestResponseBody fails.
	body = encodeBodyPayload(t, "finish-passkey",
		accountsIface.FinishPasskeyInput{SessionID: "x", Assertion: []byte("{not json")}, nil)
	if _, err := srv.apiLoginHandler(ctx, nil, body); err == nil {
		t.Fatalf("finish-passkey should reject invalid assertion bytes")
	}
	// missing fields
	for _, action := range []string{"start-managed", "finish-magic", "finish-passkey", "finish-external", "verify-session", "logout"} {
		if _, err := srv.apiLoginHandler(ctx, nil, command.Body{"action": action}); err == nil {
			t.Fatalf("login %s should error on empty body", action)
		}
	}

	// account_id missing on each Account-scoped verb.
	for _, verb := range []func(context.Context, any, command.Body) (map[string]interface{}, error){
		func(c context.Context, _ any, b command.Body) (map[string]interface{}, error) {
			return srv.apiMemberHandler(c, nil, b)
		},
		func(c context.Context, _ any, b command.Body) (map[string]interface{}, error) {
			return srv.apiUserHandler(c, nil, b)
		},
		func(c context.Context, _ any, b command.Body) (map[string]interface{}, error) {
			return srv.apiPlanHandler(c, nil, b)
		},
	} {
		if _, err := verb(ctx, nil, command.Body{"action": "list"}); err == nil {
			t.Fatalf("verb without account_id should error")
		}
	}
}

func TestInProcessClient_Close_NoOp(t *testing.T) {
	srv, _ := loginTestService(t)
	cli := newInProcessClient(srv)
	cli.Close() // exercised; no panic, no resources held
}

// TestVerbErrors_IncludeContext confirms each verb errors on an unknown action.
func TestVerbErrors_IncludeContext(t *testing.T) {
	srv := dispatchTestService(t)
	ctx := context.Background()
	for _, verb := range []func(context.Context, any, command.Body) (map[string]interface{}, error){
		func(c context.Context, _ any, b command.Body) (map[string]interface{}, error) {
			return srv.apiAccountHandler(c, nil, b)
		},
		func(c context.Context, _ any, b command.Body) (map[string]interface{}, error) {
			return srv.apiMemberHandler(c, nil, b)
		},
		func(c context.Context, _ any, b command.Body) (map[string]interface{}, error) {
			return srv.apiUserHandler(c, nil, b)
		},
		func(c context.Context, _ any, b command.Body) (map[string]interface{}, error) {
			return srv.apiPlanHandler(c, nil, b)
		},
	} {
		if _, err := verb(ctx, nil, command.Body{"action": "unknown"}); err == nil {
			t.Fatalf("verb missing unknown-action error")
		}
	}
}
