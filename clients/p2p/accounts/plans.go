package accounts

import (
	"context"
	"fmt"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/streams/command"
)

// plansImpl is the P2P client for the global Plan catalogue. Plans are not
// scoped to an account; the only operations are Create (operator-only), Get,
// and List.
type plansImpl struct {
	c *Client
}

func (i *plansImpl) Create(ctx context.Context, in accountsIface.CreatePlanInput) (*accountsIface.Plan, error) {
	body := command.Body{
		"action":       "create",
		"name":         in.Name,
		"display_name": in.DisplayName,
	}
	if in.Data != nil {
		body["data"] = in.Data
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
		"action": "get", "id": planID,
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

func (i *plansImpl) List(ctx context.Context) ([]string, error) {
	resp, err := i.c.client.Send(verbPlan, command.Body{
		"action": "list",
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
