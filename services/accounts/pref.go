package accounts

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/taubyte/tau/core/kvdb"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
)

// prefStore implements accountsIface.PRefs for one Account.
//
// Invariants worth highlighting:
//   - PRef names are varname-shaped, case-sensitive, and immortal (no
//     Rename, no Delete; Disable is the only off-switch).
//   - The envelope is written once at Create and mutated only via
//     SetDisplayName. Status is NOT persisted — derived on Get from the
//     latest enable/disable event. This is what keeps Disable/Enable from
//     racing on a shared envelope blob across nodes.
//   - The "current" plan is the PlanID of the latest `assign` event.
//   - Assign on a disabled PRef is rejected (admin must Enable first).
type prefStore struct {
	db        kvdb.KVDB
	accountID string
	plans     *planStore // global; needed to validate Assign targets
}

func newPRefStore(db kvdb.KVDB, accountID string, plans *planStore) *prefStore {
	return &prefStore{db: db, accountID: accountID, plans: plans}
}

var _ accountsIface.PRefs = (*prefStore)(nil)

func (s *prefStore) Create(ctx context.Context, in accountsIface.CreatePRefInput) (*accountsIface.PRef, error) {
	if err := validatePRefName(in.Name); err != nil {
		return nil, err
	}
	if in.MemberID == "" {
		return nil, errors.New("accounts: member_id required")
	}
	if existing, _ := s.Get(ctx, in.Name); existing != nil {
		return nil, fmt.Errorf("accounts: pref %q already exists", in.Name)
	}
	displayName := in.DisplayName
	if displayName == "" {
		displayName = in.Name
	}
	pref := &accountsIface.PRef{
		Name:        in.Name,
		AccountID:   s.accountID,
		DisplayName: displayName,
		CreatedAt:   time.Now().UTC(),
	}
	if err := putKV(ctx, s.db, PRefProfilePath(s.accountID, pref.Name), pref); err != nil {
		return nil, err
	}
	pref.Status = accountsIface.PRefStatusActive
	return pref, nil
}

func (s *prefStore) Get(ctx context.Context, name string) (*accountsIface.PRef, error) {
	var pref accountsIface.PRef
	if err := getKV(ctx, s.db, PRefProfilePath(s.accountID, name), &pref); err != nil {
		return nil, err
	}
	status, err := s.derivedStatus(ctx, name)
	if err != nil {
		return nil, err
	}
	pref.Status = status
	return &pref, nil
}

// derivedStatus walks the event log newest-first and returns the status from
// the most recent disable/enable. Assigns are skipped — they don't affect
// the active/disabled axis. Default for a PRef with no such events is Active.
func (s *prefStore) derivedStatus(ctx context.Context, name string) (accountsIface.PRefStatus, error) {
	keys, err := s.db.List(ctx, PRefEventsPrefix(s.accountID, name))
	if err != nil {
		return "", fmt.Errorf("accounts: list pref events for status: %w", err)
	}
	var latestKey string
	for _, k := range keys {
		if k > latestKey {
			latestKey = k
		}
	}
	for latestKey != "" {
		var ev accountsIface.PRefEvent
		if err := getKV(ctx, s.db, latestKey, &ev); err != nil {
			if errors.Is(err, ErrNotFound) {
				break
			}
			return "", err
		}
		switch ev.Kind {
		case accountsIface.PRefEventKindDisable:
			return accountsIface.PRefStatusDisabled, nil
		case accountsIface.PRefEventKindEnable:
			return accountsIface.PRefStatusActive, nil
		}
		var nextKey string
		for _, k := range keys {
			if k < latestKey && k > nextKey {
				nextKey = k
			}
		}
		latestKey = nextKey
	}
	return accountsIface.PRefStatusActive, nil
}

func (s *prefStore) List(ctx context.Context) ([]string, error) {
	return listChildIDs(ctx, s.db, AccountPRefsPrefix(s.accountID))
}

// SetDisplayName mutates only the cosmetic field. Works on disabled PRefs.
func (s *prefStore) SetDisplayName(ctx context.Context, name, displayName string) (*accountsIface.PRef, error) {
	pref, err := s.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if displayName == "" {
		displayName = pref.Name
	}
	pref.DisplayName = displayName
	if err := putKV(ctx, s.db, PRefProfilePath(s.accountID, name), pref); err != nil {
		return nil, err
	}
	return pref, nil
}

func (s *prefStore) Assign(ctx context.Context, in accountsIface.AssignPRefInput) (*accountsIface.PRefEvent, error) {
	if in.PlanID == "" {
		return nil, errors.New("accounts: plan_id required")
	}
	pref, err := s.Get(ctx, in.Name)
	if err != nil {
		return nil, err
	}
	if pref.Status != accountsIface.PRefStatusActive {
		return nil, fmt.Errorf("accounts: pref %q is disabled; enable before assigning", in.Name)
	}
	if _, err := s.plans.Get(ctx, in.PlanID); err != nil {
		if errors.Is(err, ErrNotFound) {
			// Plan exists on a peer but hasn't converged here yet — surface
			// the retry hint instead of a bare "not found".
			return nil, fmt.Errorf("accounts: plan %q not found (retry if newly created on another node)", in.PlanID)
		}
		return nil, err
	}
	return s.writeEvent(ctx, in.Name, accountsIface.PRefEvent{
		Kind:     accountsIface.PRefEventKindAssign,
		PlanID:   in.PlanID,
		MemberID: in.MemberID,
		Note:     in.Note,
	})
}

func (s *prefStore) Disable(ctx context.Context, in accountsIface.DisablePRefInput) (*accountsIface.PRefEvent, error) {
	if _, err := s.Get(ctx, in.Name); err != nil {
		return nil, err
	}
	return s.writeEvent(ctx, in.Name, accountsIface.PRefEvent{
		Kind:     accountsIface.PRefEventKindDisable,
		MemberID: in.MemberID,
		Note:     in.Note,
	})
}

func (s *prefStore) Enable(ctx context.Context, in accountsIface.EnablePRefInput) (*accountsIface.PRefEvent, error) {
	if _, err := s.Get(ctx, in.Name); err != nil {
		return nil, err
	}
	return s.writeEvent(ctx, in.Name, accountsIface.PRefEvent{
		Kind:     accountsIface.PRefEventKindEnable,
		MemberID: in.MemberID,
		Note:     in.Note,
	})
}

// Events returns events whose At falls within [from, to] (inclusive), in
// chronological order. A zero-valued bound disables that side. The sort is
// explicit because db.List does not guarantee key order.
func (s *prefStore) Events(ctx context.Context, name string, from, to time.Time) ([]accountsIface.PRefEvent, error) {
	prefix := PRefEventsPrefix(s.accountID, name)
	keys, err := s.db.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("accounts: list pref events: %w", err)
	}
	out := make([]accountsIface.PRefEvent, 0, len(keys))
	for _, k := range keys {
		var ev accountsIface.PRefEvent
		if err := getKV(ctx, s.db, k, &ev); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		if !from.IsZero() && ev.At.Before(from) {
			continue
		}
		if !to.IsZero() && ev.At.After(to) {
			continue
		}
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

func (s *prefStore) LatestEvent(ctx context.Context, name string) (*accountsIface.PRefEvent, error) {
	prefix := PRefEventsPrefix(s.accountID, name)
	keys, err := s.db.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("accounts: list pref events: %w", err)
	}
	if len(keys) == 0 {
		return nil, ErrNotFound
	}
	// Keys are zero-padded by PRefEventPath, so lexical max = chronological max.
	var maxKey string
	for _, k := range keys {
		if k > maxKey {
			maxKey = k
		}
	}
	var ev accountsIface.PRefEvent
	if err := getKV(ctx, s.db, maxKey, &ev); err != nil {
		return nil, err
	}
	return &ev, nil
}

func (s *prefStore) writeEvent(ctx context.Context, name string, ev accountsIface.PRefEvent) (*accountsIface.PRefEvent, error) {
	if ev.MemberID == "" {
		return nil, errors.New("accounts: member_id required")
	}
	ev.At = time.Now().UTC()
	key := PRefEventPath(s.accountID, name, ev.At.UnixNano())
	if err := putKV(ctx, s.db, key, ev); err != nil {
		return nil, err
	}
	return &ev, nil
}

// validatePRefName enforces varname rules: [a-zA-Z_][a-zA-Z0-9_]*, max 64.
// Case-sensitive — "Pro" and "pro" are distinct names.
func validatePRefName(name string) error {
	if name == "" {
		return errors.New("accounts: pref name required")
	}
	if len(name) > 64 {
		return errors.New("accounts: pref name too long (>64)")
	}
	if !isVarnameStart(rune(name[0])) {
		return fmt.Errorf("accounts: pref name must start with a letter or underscore (got %q)", string(name[0]))
	}
	for _, r := range name[1:] {
		if !isVarnameRune(r) {
			return fmt.Errorf("accounts: pref name must be varname (a-zA-Z0-9_); bad char %q", string(r))
		}
	}
	return nil
}

func isVarnameStart(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r == '_':
		return true
	}
	return false
}

func isVarnameRune(r rune) bool {
	if isVarnameStart(r) {
		return true
	}
	if r >= '0' && r <= '9' {
		return true
	}
	return false
}
