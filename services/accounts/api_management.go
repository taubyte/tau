package accounts

import (
	"context"
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/tau/utils/maps"
)

// Stream verbs — one per entity, dispatched by the `action` body field.
const (
	StreamVerbAccount = "account"
	StreamVerbMember  = "member"
	StreamVerbUser    = "user"
	StreamVerbPlan    = "plan"
	StreamVerbLogin   = "login"
)

// Wire shape: scalar input fields ride directly on the body (patrick-style);
// structured fields (slices, nested structs) ride as a single named entry and
// are landed via decodeField.

func requireAccountID(body command.Body) (string, error) {
	id, err := maps.String(body, "account_id")
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", errors.New("account_id required")
	}
	return id, nil
}

func optString(body command.Body, key string) (string, bool) {
	v, ok := body[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func decodeField(v, into any) error {
	raw, err := cbor.Marshal(v)
	if err != nil {
		return fmt.Errorf("re-encode field: %w", err)
	}
	return cbor.Unmarshal(raw, into)
}

// --- Account verb -------------------------------------------------

func (srv *AccountsService) apiAccountHandler(ctx context.Context, _ streams.Connection, body command.Body) (cr.Response, error) {
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, fmt.Errorf("account: %w", err)
	}
	cli := srv.Client().Accounts()
	switch action {
	case "create":
		in := accountsIface.CreateAccountInput{
			Slug:         maps.TryString(body, "slug"),
			Name:         maps.TryString(body, "name"),
			Kind:         accountsIface.AccountKind(maps.TryString(body, "kind")),
			AuthMode:     accountsIface.AuthMode(maps.TryString(body, "auth_mode")),
			PlanTemplate: maps.TryString(body, "plan_template"),
		}
		if v, ok := body["auth_config"]; ok {
			if err := decodeField(v, &in.AuthConfig); err != nil {
				return nil, err
			}
		}
		if v, ok := body["metadata"]; ok {
			if err := decodeField(v, &in.Metadata); err != nil {
				return nil, err
			}
		}
		acc, err := cli.Create(ctx, in)
		if err != nil {
			return nil, err
		}
		return cr.Response{"account": acc}, nil
	case "get":
		id, err := maps.String(body, "id")
		if err != nil {
			return nil, err
		}
		acc, err := cli.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		return cr.Response{"account": acc}, nil
	case "get-by-slug":
		slug, err := maps.String(body, "slug")
		if err != nil {
			return nil, err
		}
		acc, err := cli.GetBySlug(ctx, slug)
		if err != nil {
			return nil, err
		}
		return cr.Response{"account": acc}, nil
	case "list":
		ids, err := cli.List(ctx)
		if err != nil {
			return nil, err
		}
		return cr.Response{"ids": ids}, nil
	case "update":
		id, err := maps.String(body, "id")
		if err != nil {
			return nil, err
		}
		var in accountsIface.UpdateAccountInput
		if s, ok := optString(body, "name"); ok {
			in.Name = &s
		}
		if s, ok := optString(body, "auth_mode"); ok {
			am := accountsIface.AuthMode(s)
			in.AuthMode = &am
		}
		if s, ok := optString(body, "plan_template"); ok {
			in.PlanTemplate = &s
		}
		if s, ok := optString(body, "status"); ok {
			st := accountsIface.AccountStatus(s)
			in.Status = &st
		}
		if v, ok := body["auth_config"]; ok {
			if err := decodeField(v, &in.AuthConfig); err != nil {
				return nil, err
			}
		}
		if v, ok := body["metadata"]; ok {
			if err := decodeField(v, &in.Metadata); err != nil {
				return nil, err
			}
		}
		acc, err := cli.Update(ctx, id, in)
		if err != nil {
			return nil, err
		}
		return cr.Response{"account": acc}, nil
	case "delete":
		id, err := maps.String(body, "id")
		if err != nil {
			return nil, err
		}
		if err := cli.Delete(ctx, id); err != nil {
			return nil, err
		}
		return cr.Response{"ok": true}, nil
	default:
		return nil, fmt.Errorf("account: unknown action %q", action)
	}
}

// --- Member verb --------------------------------------------------

func (srv *AccountsService) apiMemberHandler(ctx context.Context, _ streams.Connection, body command.Body) (cr.Response, error) {
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, fmt.Errorf("member: %w", err)
	}
	accountID, err := requireAccountID(body)
	if err != nil {
		return nil, err
	}
	cli := srv.Client().Members(accountID)
	switch action {
	case "invite":
		in := accountsIface.InviteMemberInput{
			PrimaryEmail: maps.TryString(body, "primary_email"),
			Role:         accountsIface.Role(maps.TryString(body, "role")),
		}
		m, err := cli.Invite(ctx, in)
		if err != nil {
			return nil, err
		}
		return cr.Response{"member": m}, nil
	case "get":
		id, err := maps.String(body, "id")
		if err != nil {
			return nil, err
		}
		m, err := cli.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		return cr.Response{"member": m}, nil
	case "list":
		ids, err := cli.List(ctx)
		if err != nil {
			return nil, err
		}
		return cr.Response{"ids": ids}, nil
	case "update":
		id, err := maps.String(body, "id")
		if err != nil {
			return nil, err
		}
		var in accountsIface.UpdateMemberInput
		if s, ok := optString(body, "role"); ok {
			r := accountsIface.Role(s)
			in.Role = &r
		}
		m, err := cli.Update(ctx, id, in)
		if err != nil {
			return nil, err
		}
		return cr.Response{"member": m}, nil
	case "remove":
		id, err := maps.String(body, "id")
		if err != nil {
			return nil, err
		}
		if err := cli.Remove(ctx, id); err != nil {
			return nil, err
		}
		return cr.Response{"ok": true}, nil
	default:
		return nil, fmt.Errorf("member: unknown action %q", action)
	}
}

// --- User verb (linked git accounts) ------------------------------

func (srv *AccountsService) apiUserHandler(ctx context.Context, _ streams.Connection, body command.Body) (cr.Response, error) {
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, fmt.Errorf("user: %w", err)
	}
	accountID, err := requireAccountID(body)
	if err != nil {
		return nil, err
	}
	cli := srv.Client().Users(accountID)
	switch action {
	case "add":
		in := accountsIface.AddUserInput{
			Provider:    maps.TryString(body, "provider"),
			ExternalID:  maps.TryString(body, "external_id"),
			DisplayName: maps.TryString(body, "display_name"),
		}
		u, err := cli.Add(ctx, in)
		if err != nil {
			return nil, err
		}
		return cr.Response{"user": u}, nil
	case "get":
		id, err := maps.String(body, "id")
		if err != nil {
			return nil, err
		}
		u, err := cli.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		return cr.Response{"user": u}, nil
	case "get-by-external":
		provider, err := maps.String(body, "provider")
		if err != nil {
			return nil, err
		}
		externalID, err := maps.String(body, "external_id")
		if err != nil {
			return nil, err
		}
		u, err := cli.GetByExternal(ctx, provider, externalID)
		if err != nil {
			return nil, err
		}
		return cr.Response{"user": u}, nil
	case "list":
		ids, err := cli.List(ctx)
		if err != nil {
			return nil, err
		}
		return cr.Response{"ids": ids}, nil
	case "remove":
		id, err := maps.String(body, "id")
		if err != nil {
			return nil, err
		}
		if err := cli.Remove(ctx, id); err != nil {
			return nil, err
		}
		return cr.Response{"ok": true}, nil
	case "grant":
		id, err := maps.String(body, "id")
		if err != nil {
			return nil, err
		}
		planID, err := maps.String(body, "plan_id")
		if err != nil {
			return nil, err
		}
		isDefault, _ := maps.Bool(body, "is_default")
		if err := cli.Grant(ctx, id, accountsIface.GrantPlanInput{PlanID: planID, IsDefault: isDefault}); err != nil {
			return nil, err
		}
		return cr.Response{"ok": true}, nil
	case "revoke":
		id, err := maps.String(body, "id")
		if err != nil {
			return nil, err
		}
		planID, err := maps.String(body, "plan_id")
		if err != nil {
			return nil, err
		}
		if err := cli.Revoke(ctx, id, planID); err != nil {
			return nil, err
		}
		return cr.Response{"ok": true}, nil
	default:
		return nil, fmt.Errorf("user: unknown action %q", action)
	}
}

// --- Plan verb --------------------------------------------------

func (srv *AccountsService) apiPlanHandler(ctx context.Context, _ streams.Connection, body command.Body) (cr.Response, error) {
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	accountID, err := requireAccountID(body)
	if err != nil {
		return nil, err
	}
	cli := srv.Client().Plans(accountID)
	switch action {
	case "create":
		in := accountsIface.CreatePlanInput{
			Slug:   maps.TryString(body, "slug"),
			Name:   maps.TryString(body, "name"),
			Mode:   accountsIface.PlanMode(maps.TryString(body, "mode")),
			Period: maps.TryString(body, "period"),
		}
		if v, ok := body["dimensions"]; ok {
			if err := decodeField(v, &in.Dimensions); err != nil {
				return nil, err
			}
		}
		p, err := cli.Create(ctx, in)
		if err != nil {
			return nil, err
		}
		return cr.Response{"plan": p}, nil
	case "get":
		id, err := maps.String(body, "id")
		if err != nil {
			return nil, err
		}
		p, err := cli.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		return cr.Response{"plan": p}, nil
	case "get-by-slug":
		slug, err := maps.String(body, "slug")
		if err != nil {
			return nil, err
		}
		p, err := cli.GetBySlug(ctx, slug)
		if err != nil {
			return nil, err
		}
		return cr.Response{"plan": p}, nil
	case "list":
		ids, err := cli.List(ctx)
		if err != nil {
			return nil, err
		}
		return cr.Response{"ids": ids}, nil
	case "update":
		id, err := maps.String(body, "id")
		if err != nil {
			return nil, err
		}
		var in accountsIface.UpdatePlanInput
		if s, ok := optString(body, "name"); ok {
			in.Name = &s
		}
		if s, ok := optString(body, "mode"); ok {
			m := accountsIface.PlanMode(s)
			in.Mode = &m
		}
		if s, ok := optString(body, "period"); ok {
			in.Period = &s
		}
		if s, ok := optString(body, "status"); ok {
			st := accountsIface.PlanStatus(s)
			in.Status = &st
		}
		if v, ok := body["dimensions"]; ok {
			if err := decodeField(v, &in.Dimensions); err != nil {
				return nil, err
			}
		}
		p, err := cli.Update(ctx, id, in)
		if err != nil {
			return nil, err
		}
		return cr.Response{"plan": p}, nil
	case "delete":
		id, err := maps.String(body, "id")
		if err != nil {
			return nil, err
		}
		if err := cli.Delete(ctx, id); err != nil {
			return nil, err
		}
		return cr.Response{"ok": true}, nil
	default:
		return nil, fmt.Errorf("plan: unknown action %q", action)
	}
}

// --- Login verb ---------------------------------------------------

func (srv *AccountsService) apiLoginHandler(ctx context.Context, _ streams.Connection, body command.Body) (cr.Response, error) {
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	cli := srv.Client().Login()
	switch action {
	case "start-managed":
		in := accountsIface.StartManagedLoginInput{
			Email:       maps.TryString(body, "email"),
			AccountSlug: maps.TryString(body, "account_slug"),
		}
		out, err := cli.StartManaged(ctx, in)
		if err != nil {
			return nil, err
		}
		return cr.Response{"challenge": out}, nil
	case "finish-passkey":
		in := accountsIface.FinishPasskeyInput{
			SessionID: maps.TryString(body, "session_id"),
		}
		if v, ok := body["assertion"]; ok {
			if b, ok := v.([]byte); ok {
				in.Assertion = b
			} else if err := decodeField(v, &in.Assertion); err != nil {
				return nil, err
			}
		}
		sess, err := cli.FinishManagedPasskey(ctx, in)
		if err != nil {
			return nil, err
		}
		return cr.Response{"session": sess}, nil
	case "finish-magic":
		in := accountsIface.FinishMagicLinkInput{
			Code:     maps.TryString(body, "code"),
			ClientIP: maps.TryString(body, "client_ip"),
		}
		sess, err := cli.FinishManagedMagicLink(ctx, in)
		if err != nil {
			return nil, err
		}
		return cr.Response{"session": sess}, nil
	case "start-external":
		slug, err := maps.String(body, "account_slug")
		if err != nil {
			return nil, err
		}
		out, err := cli.StartExternal(ctx, slug)
		if err != nil {
			return nil, err
		}
		return cr.Response{"redirect": out}, nil
	case "finish-external":
		in := accountsIface.FinishExternalLoginInput{
			State: maps.TryString(body, "state"),
			Code:  maps.TryString(body, "code"),
		}
		sess, err := cli.FinishExternal(ctx, in)
		if err != nil {
			return nil, err
		}
		return cr.Response{"session": sess}, nil
	case "verify-session":
		token, err := maps.String(body, "token")
		if err != nil {
			return nil, err
		}
		sess, err := cli.VerifySession(ctx, token)
		if err != nil {
			return nil, err
		}
		return cr.Response{"session": sess}, nil
	case "logout":
		token, err := maps.String(body, "token")
		if err != nil {
			return nil, err
		}
		if err := cli.Logout(ctx, token); err != nil {
			return nil, err
		}
		return cr.Response{"ok": true}, nil
	default:
		return nil, fmt.Errorf("login: unknown action %q", action)
	}
}
