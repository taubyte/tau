package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/fxamacker/cbor/v2"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
)

// mgmtSetup runs a real loginTestService, seeds an Account + Member, and
// holds a session bearer ready for direct handler-level calls.
type mgmtSetup struct {
	srv       *AccountsService
	bearer    string
	accountID string
	memberID  string
}

func setupMgmt(t *testing.T) *mgmtSetup {
	t.Helper()
	srv, _ := loginTestService(t)
	ctx := context.Background()
	acc, err := srv.Client().Accounts().Create(ctx, accountsIface.CreateAccountInput{
		Slug: "acme", Name: "Acme",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	mem, err := srv.Client().Members(acc.ID).Invite(ctx, accountsIface.InviteMemberInput{
		PrimaryEmail: "alice@example.com", Role: accountsIface.RoleOwner,
	})
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	_, bearer, err := srv.sessions.Issue(ctx, acc.ID, mem.ID)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	return &mgmtSetup{srv: srv, bearer: bearer, accountID: acc.ID, memberID: mem.ID}
}

// callMgmt invokes the route's handler with a patrick-style body: payload-object
// fields are spread alongside `action`, `account_id`, and extras. On the response
// side it picks the first non-`ok` field and decodes it into `out`.
func (s *mgmtSetup) callMgmt(t *testing.T, route, action, accountID string, extras map[string]string, payloadObj any, out any) error {
	t.Helper()
	body := command.Body{
		"action":     action,
		"account_id": accountID,
	}
	for k, v := range extras {
		if v != "" {
			body[k] = v
		}
	}
	if payloadObj != nil {
		raw, err := cbor.Marshal(payloadObj)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		var fields map[string]any
		if err := cbor.Unmarshal(raw, &fields); err != nil {
			t.Fatalf("spread payload: %v", err)
		}
		for k, v := range fields {
			body[k] = v
		}
	}

	ctx := newMockCtx()
	ctx.headers.Set("Authorization", "Bearer "+s.bearer)
	rawBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	ctx.body = rawBody

	var h func(httpCtx) (any, error)
	switch route {
	case "/members":
		wrapped := s.srv.httpManagementHandler(s.srv.apiMemberHandler)
		h = func(c httpCtx) (any, error) { return wrapped(c.(*mockHTTPCtx)) }
	case "/users":
		wrapped := s.srv.httpManagementHandler(s.srv.apiUserHandler)
		h = func(c httpCtx) (any, error) { return wrapped(c.(*mockHTTPCtx)) }
	default:
		t.Fatalf("unknown management route: %s", route)
	}

	resp, err := h(ctx)
	if err != nil || out == nil {
		return err
	}
	m, ok := resp.(cr.Response)
	if !ok {
		mm, ok2 := resp.(map[string]any)
		if !ok2 {
			t.Fatalf("response wrong type: %T", resp)
		}
		m = mm
	}
	var v any
	for k, val := range m {
		if k == "ok" {
			continue
		}
		v = val
		break
	}
	if v == nil {
		return errors.New("response has no payload field")
	}
	raw, err := cbor.Marshal(v)
	if err != nil {
		t.Fatalf("re-encode response payload: %v", err)
	}
	if err := cbor.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode response payload: %v", err)
	}
	return nil
}

type httpCtx interface{}

// --- Auth gating --------------------------------------------------

func TestHTTPMgmt_RequiresBearer(t *testing.T) {
	s := setupMgmt(t)
	ctx := newMockCtx()
	ctx.body = []byte(`{"action":"list","account_id":"x"}`)
	h := s.srv.httpManagementHandler(s.srv.apiMemberHandler)
	if _, err := h(ctx); err == nil {
		t.Fatalf("expected error without Authorization header")
	}
}

func TestHTTPMgmt_RejectsBadBearer(t *testing.T) {
	s := setupMgmt(t)
	ctx := newMockCtx()
	ctx.headers.Set("Authorization", "Bearer tau-session.bogus.bogus")
	ctx.body = []byte(`{"action":"list","account_id":"x"}`)
	h := s.srv.httpManagementHandler(s.srv.apiMemberHandler)
	if _, err := h(ctx); err == nil {
		t.Fatalf("expected error for bogus bearer")
	}
}

func TestHTTPMgmt_RejectsBadBody(t *testing.T) {
	s := setupMgmt(t)
	ctx := newMockCtx()
	ctx.headers.Set("Authorization", "Bearer "+s.bearer)
	ctx.body = []byte("not json")
	h := s.srv.httpManagementHandler(s.srv.apiMemberHandler)
	if _, err := h(ctx); err == nil {
		t.Fatalf("expected parse error")
	}
}

// --- Members route ------------------------------------------------

func TestHTTPMgmt_Members_InviteAndList(t *testing.T) {
	s := setupMgmt(t)

	var invited accountsIface.Member
	if err := s.callMgmt(t, "/members", "invite", s.accountID, nil, accountsIface.InviteMemberInput{
		PrimaryEmail: "bob@example.com",
		Role:         accountsIface.RoleAdmin,
	}, &invited); err != nil {
		t.Fatalf("invite: %v", err)
	}
	if invited.ID == "" {
		t.Fatalf("invite returned empty id: %+v", invited)
	}

	var ids []string
	if err := s.callMgmt(t, "/members", "list", s.accountID, nil, nil, &ids); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ids) < 2 {
		t.Fatalf("expected ≥2 member ids, got %d", len(ids))
	}
}

// --- Users route --------------------------------------------------

// Plan-create is operator-only and not HTTP-exposed, so the test uses the
// in-process Client to seed a plan before exercising the HTTP user route.
func TestHTTPMgmt_Users_AddAndList(t *testing.T) {
	s := setupMgmt(t)
	ctx := context.Background()
	plan, err := s.srv.Client().Plans(s.accountID).Create(ctx, accountsIface.CreatePlanInput{
		Slug: "prod", Name: "Production", Mode: accountsIface.PlanModeQuota,
	})
	if err != nil {
		t.Fatalf("inline create plan: %v", err)
	}

	var added accountsIface.User
	if err := s.callMgmt(t, "/users", "add", s.accountID, nil, accountsIface.AddUserInput{
		Provider:    "github",
		ExternalID:  "777",
		DisplayName: "wesker",
	}, &added); err != nil {
		t.Fatalf("add user: %v", err)
	}
	if added.ID == "" {
		t.Fatalf("add response missing id: %+v", added)
	}

	if err := s.callMgmt(t, "/users", "grant", s.accountID,
		map[string]string{"id": added.ID},
		accountsIface.GrantPlanInput{PlanID: plan.ID},
		nil,
	); err != nil {
		t.Fatalf("grant: %v", err)
	}

	var ids []string
	if err := s.callMgmt(t, "/users", "list", s.accountID, nil, nil, &ids); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ids) < 1 {
		t.Fatalf("expected ≥1 user, got %d", len(ids))
	}

	if err := s.callMgmt(t, "/users", "remove", s.accountID,
		map[string]string{"id": added.ID},
		nil,
		nil,
	); err != nil {
		t.Fatalf("remove: %v", err)
	}
}
