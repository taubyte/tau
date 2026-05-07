package accounts

import (
	"context"
	"fmt"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/streams/command"
)

type accountsImpl struct {
	c *Client
}

func (i *accountsImpl) Create(ctx context.Context, in accountsIface.CreateAccountInput) (*accountsIface.Account, error) {
	body := command.Body{
		"action":        "create",
		"slug":          in.Slug,
		"name":          in.Name,
		"kind":          string(in.Kind),
		"auth_mode":     string(in.AuthMode),
		"plan_template": in.PlanTemplate,
	}
	if in.AuthConfig != nil {
		body["auth_config"] = in.AuthConfig
	}
	if in.Metadata != nil {
		body["metadata"] = in.Metadata
	}
	resp, err := i.c.client.Send(verbAccount, body, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("accounts.Create: %w", err)
	}
	var acc accountsIface.Account
	if err := readField(resp, "account", &acc); err != nil {
		return nil, err
	}
	return &acc, nil
}

func (i *accountsImpl) Get(ctx context.Context, accountID string) (*accountsIface.Account, error) {
	resp, err := i.c.client.Send(verbAccount, command.Body{"action": "get", "id": accountID}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("accounts.Get: %w", err)
	}
	var acc accountsIface.Account
	if err := readField(resp, "account", &acc); err != nil {
		return nil, err
	}
	return &acc, nil
}

func (i *accountsImpl) GetBySlug(ctx context.Context, slug string) (*accountsIface.Account, error) {
	resp, err := i.c.client.Send(verbAccount, command.Body{"action": "get-by-slug", "slug": slug}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("accounts.GetBySlug: %w", err)
	}
	var acc accountsIface.Account
	if err := readField(resp, "account", &acc); err != nil {
		return nil, err
	}
	return &acc, nil
}

func (i *accountsImpl) List(ctx context.Context) ([]string, error) {
	resp, err := i.c.client.Send(verbAccount, command.Body{"action": "list"}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("accounts.List: %w", err)
	}
	var ids []string
	if err := readField(resp, "ids", &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func (i *accountsImpl) Update(ctx context.Context, accountID string, in accountsIface.UpdateAccountInput) (*accountsIface.Account, error) {
	body := command.Body{"action": "update", "id": accountID}
	if in.Name != nil {
		body["name"] = *in.Name
	}
	if in.AuthMode != nil {
		body["auth_mode"] = string(*in.AuthMode)
	}
	if in.PlanTemplate != nil {
		body["plan_template"] = *in.PlanTemplate
	}
	if in.Status != nil {
		body["status"] = string(*in.Status)
	}
	if in.AuthConfig != nil {
		body["auth_config"] = in.AuthConfig
	}
	if in.Metadata != nil {
		body["metadata"] = in.Metadata
	}
	resp, err := i.c.client.Send(verbAccount, body, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("accounts.Update: %w", err)
	}
	var acc accountsIface.Account
	if err := readField(resp, "account", &acc); err != nil {
		return nil, err
	}
	return &acc, nil
}

func (i *accountsImpl) Delete(ctx context.Context, accountID string) error {
	resp, err := i.c.client.Send(verbAccount, command.Body{"action": "delete", "id": accountID}, i.c.peers...)
	if err != nil {
		return fmt.Errorf("accounts.Delete: %w", err)
	}
	return expectOK(resp, "accounts.Delete")
}
