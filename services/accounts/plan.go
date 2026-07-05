package accounts

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/core/kvdb"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	protocolCommon "github.com/taubyte/tau/services/common"
)

// planStore implements accountsIface.Plans over the global /plans/ namespace.
// Plans are not scoped to an Account — a PRef is the only link. Records are
// immutable and undeletable; Create is the only state-mutating op.
type planStore struct {
	db kvdb.KVDB
}

func newPlanStore(db kvdb.KVDB) *planStore {
	return &planStore{db: db}
}

var _ accountsIface.Plans = (*planStore)(nil)

func (s *planStore) Create(ctx context.Context, in accountsIface.CreatePlanInput) (*accountsIface.Plan, error) {
	if in.Name == "" {
		return nil, errors.New("accounts: plan name required")
	}
	displayName := in.DisplayName
	if displayName == "" {
		displayName = in.Name
	}
	p := &accountsIface.Plan{
		ID:          protocolCommon.GetNewPlanID(in.Name),
		Name:        in.Name,
		DisplayName: displayName,
		Data:        in.Data,
	}
	if err := putKV(ctx, s.db, PlanProfilePath(p.ID), p); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *planStore) Get(ctx context.Context, planID string) (*accountsIface.Plan, error) {
	if planID == "" {
		return nil, errors.New("accounts: plan_id required")
	}
	var p accountsIface.Plan
	if err := getKV(ctx, s.db, PlanProfilePath(planID), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *planStore) List(ctx context.Context) ([]string, error) {
	keys, err := s.db.List(ctx, PlansPrefix())
	if err != nil {
		return nil, fmt.Errorf("accounts: list plans: %w", err)
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		id := k[len(PlansPrefix()):]
		if id == "" {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}
