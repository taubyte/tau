package accounts

import (
	"context"
	"fmt"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/streams/command"
)

type usersImpl struct {
	c         *Client
	accountID string
}

func (i *usersImpl) Add(ctx context.Context, in accountsIface.AddUserInput) (*accountsIface.User, error) {
	resp, err := i.c.client.Send(verbUser, command.Body{
		"action":       "add",
		"account_id":   i.accountID,
		"provider":     in.Provider,
		"external_id":  in.ExternalID,
		"display_name": in.DisplayName,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("users.Add: %w", err)
	}
	var u accountsIface.User
	if err := readField(resp, "user", &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (i *usersImpl) Get(ctx context.Context, userID string) (*accountsIface.User, error) {
	resp, err := i.c.client.Send(verbUser, command.Body{
		"action": "get", "account_id": i.accountID, "id": userID,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("users.Get: %w", err)
	}
	var u accountsIface.User
	if err := readField(resp, "user", &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (i *usersImpl) GetByExternal(ctx context.Context, provider, externalID string) (*accountsIface.User, error) {
	resp, err := i.c.client.Send(verbUser, command.Body{
		"action": "get-by-external", "account_id": i.accountID,
		"provider": provider, "external_id": externalID,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("users.GetByExternal: %w", err)
	}
	var u accountsIface.User
	if err := readField(resp, "user", &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (i *usersImpl) List(ctx context.Context) ([]string, error) {
	resp, err := i.c.client.Send(verbUser, command.Body{
		"action": "list", "account_id": i.accountID,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("users.List: %w", err)
	}
	var ids []string
	if err := readField(resp, "ids", &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func (i *usersImpl) Remove(ctx context.Context, userID string) error {
	resp, err := i.c.client.Send(verbUser, command.Body{
		"action": "remove", "account_id": i.accountID, "id": userID,
	}, i.c.peers...)
	if err != nil {
		return fmt.Errorf("users.Remove: %w", err)
	}
	return expectOK(resp, "users.Remove")
}

func (i *usersImpl) Grant(ctx context.Context, userID string, in accountsIface.GrantPlanInput) error {
	resp, err := i.c.client.Send(verbUser, command.Body{
		"action":     "grant",
		"account_id": i.accountID,
		"id":         userID,
		"plan_id":    in.PlanID,
		"is_default": in.IsDefault,
	}, i.c.peers...)
	if err != nil {
		return fmt.Errorf("users.Grant: %w", err)
	}
	return expectOK(resp, "users.Grant")
}

func (i *usersImpl) Revoke(ctx context.Context, userID, planID string) error {
	resp, err := i.c.client.Send(verbUser, command.Body{
		"action": "revoke", "account_id": i.accountID,
		"id": userID, "plan_id": planID,
	}, i.c.peers...)
	if err != nil {
		return fmt.Errorf("users.Revoke: %w", err)
	}
	return expectOK(resp, "users.Revoke")
}
