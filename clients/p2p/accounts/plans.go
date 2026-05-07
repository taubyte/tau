package accounts

import (
	"context"
	"fmt"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/streams/command"
)

type plansImpl struct {
	c         *Client
	accountID string
}

func (i *plansImpl) Create(ctx context.Context, in accountsIface.CreatePlanInput) (*accountsIface.Plan, error) {
	body := command.Body{
		"action":     "create",
		"account_id": i.accountID,
		"slug":       in.Slug,
		"name":       in.Name,
		"mode":       string(in.Mode),
		"period":     in.Period,
	}
	if in.Dimensions != nil {
		body["dimensions"] = in.Dimensions
	}
	resp, err := i.c.client.Send(verbPlan, body, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("plans.Create: %w", err)
	}
	var p accountsIface.Plan
	if err := readField(resp, "plan", &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (i *plansImpl) Get(ctx context.Context, planID string) (*accountsIface.Plan, error) {
	resp, err := i.c.client.Send(verbPlan, command.Body{
		"action": "get", "account_id": i.accountID, "id": planID,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("plans.Get: %w", err)
	}
	var p accountsIface.Plan
	if err := readField(resp, "plan", &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (i *plansImpl) GetBySlug(ctx context.Context, slug string) (*accountsIface.Plan, error) {
	resp, err := i.c.client.Send(verbPlan, command.Body{
		"action": "get-by-slug", "account_id": i.accountID, "slug": slug,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("plans.GetBySlug: %w", err)
	}
	var p accountsIface.Plan
	if err := readField(resp, "plan", &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (i *plansImpl) List(ctx context.Context) ([]string, error) {
	resp, err := i.c.client.Send(verbPlan, command.Body{
		"action": "list", "account_id": i.accountID,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("plans.List: %w", err)
	}
	var ids []string
	if err := readField(resp, "ids", &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func (i *plansImpl) Update(ctx context.Context, planID string, in accountsIface.UpdatePlanInput) (*accountsIface.Plan, error) {
	body := command.Body{
		"action":     "update",
		"account_id": i.accountID,
		"id":         planID,
	}
	if in.Name != nil {
		body["name"] = *in.Name
	}
	if in.Mode != nil {
		body["mode"] = string(*in.Mode)
	}
	if in.Period != nil {
		body["period"] = *in.Period
	}
	if in.Status != nil {
		body["status"] = string(*in.Status)
	}
	if in.Dimensions != nil {
		body["dimensions"] = in.Dimensions
	}
	resp, err := i.c.client.Send(verbPlan, body, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("plans.Update: %w", err)
	}
	var p accountsIface.Plan
	if err := readField(resp, "plan", &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (i *plansImpl) Delete(ctx context.Context, planID string) error {
	resp, err := i.c.client.Send(verbPlan, command.Body{
		"action": "delete", "account_id": i.accountID, "id": planID,
	}, i.c.peers...)
	if err != nil {
		return fmt.Errorf("plans.Delete: %w", err)
	}
	return expectOK(resp, "plans.Delete")
}
