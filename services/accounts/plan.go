package accounts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taubyte/tau/core/kvdb"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	protocolCommon "github.com/taubyte/tau/services/common"
)

// planStore implements accountsIface.Plans for one Account.
type planStore struct {
	db        kvdb.KVDB
	accountID string
}

func newPlanStore(db kvdb.KVDB, accountID string) *planStore {
	return &planStore{db: db, accountID: accountID}
}

var _ accountsIface.Plans = (*planStore)(nil)

// Create persists a new Plan under the parent Account. Slug must be unique
// within the Account (cheap O(N) check via List + GetBySlug — plans per
// account are small).
func (s *planStore) Create(ctx context.Context, in accountsIface.CreatePlanInput) (*accountsIface.Plan, error) {
	if err := validatePlanSlug(in.Slug); err != nil {
		return nil, err
	}
	if in.Name == "" {
		return nil, errors.New("accounts: plan name required")
	}
	if in.Mode == "" {
		in.Mode = accountsIface.PlanModeHybrid
	}
	if existing, _ := s.GetBySlug(ctx, in.Slug); existing != nil {
		return nil, fmt.Errorf("accounts: plan slug %q already in use", in.Slug)
	}

	now := time.Now().UTC()
	b := &accountsIface.Plan{
		ID:         protocolCommon.GetNewPlanID(s.accountID, in.Slug, now.UnixNano()),
		AccountID:  s.accountID,
		Slug:       in.Slug,
		Name:       in.Name,
		Mode:       in.Mode,
		Dimensions: in.Dimensions,
		Period:     in.Period,
		Status:     accountsIface.PlanStatusActive,
		CreatedAt:  now,
	}
	if err := putKV(ctx, s.db, PlanProfilePath(s.accountID, b.ID), b); err != nil {
		return nil, err
	}
	return b, nil
}

// Get loads a Plan by ID.
func (s *planStore) Get(ctx context.Context, planID string) (*accountsIface.Plan, error) {
	var b accountsIface.Plan
	if err := getKV(ctx, s.db, PlanProfilePath(s.accountID, planID), &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// GetBySlug iterates the Account's Plans and returns the one with matching slug.
//
// O(N) scan; we don't index slug-per-Account because Plans-per-Account is
// expected to be small. If that assumption breaks we add a per-Account slug
// index (parallel to the global account-slug index).
func (s *planStore) GetBySlug(ctx context.Context, slug string) (*accountsIface.Plan, error) {
	ids, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		b, err := s.Get(ctx, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		if b.Slug == slug {
			return b, nil
		}
	}
	return nil, ErrNotFound
}

// List returns the IDs of all Plans in this Account.
func (s *planStore) List(ctx context.Context) ([]string, error) {
	return listChildIDs(ctx, s.db, AccountPlansPrefix(s.accountID))
}

// Update applies a partial update.
func (s *planStore) Update(ctx context.Context, planID string, in accountsIface.UpdatePlanInput) (*accountsIface.Plan, error) {
	b, err := s.Get(ctx, planID)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		b.Name = *in.Name
	}
	if in.Mode != nil {
		b.Mode = *in.Mode
	}
	if in.Dimensions != nil {
		b.Dimensions = in.Dimensions
	}
	if in.Period != nil {
		b.Period = *in.Period
	}
	if in.Status != nil {
		b.Status = *in.Status
	}
	if err := putKV(ctx, s.db, PlanProfilePath(s.accountID, planID), b); err != nil {
		return nil, err
	}
	return b, nil
}

// Delete removes the Plan. Does not cascade-revoke User grants pointing at
// it — admin should revoke first; orphan grants will surface at compile time
// (resolve will return "plan not found"). v2 may add cascade.
func (s *planStore) Delete(ctx context.Context, planID string) error {
	if err := s.db.Delete(ctx, PlanProfilePath(s.accountID, planID)); err != nil {
		return fmt.Errorf("accounts: delete plan: %w", err)
	}
	return nil
}

// validatePlanSlug uses the same alphabet as accounts (URL-safe).
func validatePlanSlug(slug string) error {
	// Reuse the account validator — same rules apply.
	if err := validateAccountSlug(slug); err != nil {
		// Re-wrap so error message reflects plan context.
		return fmt.Errorf("plan: %w", err)
	}
	return nil
}
