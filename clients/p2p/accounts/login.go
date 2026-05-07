package accounts

import (
	"context"
	"fmt"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/streams/command"
)

type loginImpl struct{ c *Client }

func (i *loginImpl) StartManaged(ctx context.Context, in accountsIface.StartManagedLoginInput) (*accountsIface.ManagedLoginChallenge, error) {
	resp, err := i.c.client.Send(verbLogin, command.Body{
		"action":       "start-managed",
		"email":        in.Email,
		"account_slug": in.AccountSlug,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("login.StartManaged: %w", err)
	}
	var out accountsIface.ManagedLoginChallenge
	if err := readField(resp, "challenge", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (i *loginImpl) FinishManagedPasskey(ctx context.Context, in accountsIface.FinishPasskeyInput) (*accountsIface.Session, error) {
	resp, err := i.c.client.Send(verbLogin, command.Body{
		"action":     "finish-passkey",
		"session_id": in.SessionID,
		"assertion":  in.Assertion,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("login.FinishManagedPasskey: %w", err)
	}
	var sess accountsIface.Session
	if err := readField(resp, "session", &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (i *loginImpl) FinishManagedMagicLink(ctx context.Context, in accountsIface.FinishMagicLinkInput) (*accountsIface.Session, error) {
	resp, err := i.c.client.Send(verbLogin, command.Body{
		"action":    "finish-magic",
		"code":      in.Code,
		"client_ip": in.ClientIP,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("login.FinishManagedMagicLink: %w", err)
	}
	var sess accountsIface.Session
	if err := readField(resp, "session", &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (i *loginImpl) StartExternal(ctx context.Context, accountSlug string) (*accountsIface.ExternalLoginRedirect, error) {
	resp, err := i.c.client.Send(verbLogin, command.Body{
		"action": "start-external", "account_slug": accountSlug,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("login.StartExternal: %w", err)
	}
	var out accountsIface.ExternalLoginRedirect
	if err := readField(resp, "redirect", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (i *loginImpl) FinishExternal(ctx context.Context, in accountsIface.FinishExternalLoginInput) (*accountsIface.Session, error) {
	resp, err := i.c.client.Send(verbLogin, command.Body{
		"action": "finish-external",
		"state":  in.State,
		"code":   in.Code,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("login.FinishExternal: %w", err)
	}
	var sess accountsIface.Session
	if err := readField(resp, "session", &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (i *loginImpl) VerifySession(ctx context.Context, token string) (*accountsIface.Session, error) {
	resp, err := i.c.client.Send(verbLogin, command.Body{
		"action": "verify-session", "token": token,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("login.VerifySession: %w", err)
	}
	var sess accountsIface.Session
	if err := readField(resp, "session", &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (i *loginImpl) Logout(ctx context.Context, token string) error {
	resp, err := i.c.client.Send(verbLogin, command.Body{
		"action": "logout", "token": token,
	}, i.c.peers...)
	if err != nil {
		return fmt.Errorf("login.Logout: %w", err)
	}
	return expectOK(resp, "login.Logout")
}
