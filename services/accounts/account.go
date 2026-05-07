package accounts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taubyte/tau/core/kvdb"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	protocolCommon "github.com/taubyte/tau/services/common"
)

// accountStore implements accountsIface.Accounts (the top-level Account CRUD
// surface) over a KVDB.
type accountStore struct {
	db kvdb.KVDB
}

func newAccountStore(db kvdb.KVDB) *accountStore { return &accountStore{db: db} }

// Compile-time check.
var _ accountsIface.Accounts = (*accountStore)(nil)

// Create persists a new Account. The slug must be unique. Defaults: managed
// auth_mode and active status when unspecified.
func (s *accountStore) Create(ctx context.Context, in accountsIface.CreateAccountInput) (*accountsIface.Account, error) {
	if err := validateAccountSlug(in.Slug); err != nil {
		return nil, err
	}
	if in.Name == "" {
		return nil, errors.New("accounts: name required")
	}
	if in.Kind == "" {
		in.Kind = accountsIface.AccountKindOrg
	}
	if in.AuthMode == "" {
		in.AuthMode = accountsIface.AuthModeManaged
	}

	// Slug uniqueness: lookup index.
	if existing, _ := s.lookupIDBySlug(ctx, in.Slug); existing != "" {
		return nil, fmt.Errorf("accounts: slug %q already in use", in.Slug)
	}

	now := time.Now().UTC()
	acc := &accountsIface.Account{
		ID:           protocolCommon.GetNewAccountID(in.Slug, in.Kind, now.UnixNano()),
		Slug:         in.Slug,
		Name:         in.Name,
		Kind:         in.Kind,
		Status:       accountsIface.AccountStatusActive,
		PlanTemplate: in.PlanTemplate,
		AuthMode:     in.AuthMode,
		AuthConfig:   in.AuthConfig,
		Metadata:     in.Metadata,
		CreatedAt:    now,
	}

	if err := putKV(ctx, s.db, AccountProfilePath(acc.ID), acc); err != nil {
		return nil, err
	}
	if err := s.db.Put(ctx, LookupAccountSlugPath(in.Slug), []byte(acc.ID)); err != nil {
		// best-effort rollback of the profile write
		_ = s.db.Delete(ctx, AccountProfilePath(acc.ID))
		return nil, fmt.Errorf("accounts: index slug: %w", err)
	}
	return acc, nil
}

// Get loads an Account by ID.
func (s *accountStore) Get(ctx context.Context, accountID string) (*accountsIface.Account, error) {
	var acc accountsIface.Account
	if err := getKV(ctx, s.db, AccountProfilePath(accountID), &acc); err != nil {
		return nil, err
	}
	return &acc, nil
}

// GetBySlug resolves slug → id then loads the Account.
func (s *accountStore) GetBySlug(ctx context.Context, slug string) (*accountsIface.Account, error) {
	id, err := s.lookupIDBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, ErrNotFound
	}
	return s.Get(ctx, id)
}

// List returns the IDs of all Accounts in the store.
func (s *accountStore) List(ctx context.Context) ([]string, error) {
	return listChildIDs(ctx, s.db, prefixAccounts)
}

// Update applies a partial update to an Account. Slug and ID are immutable.
func (s *accountStore) Update(ctx context.Context, accountID string, in accountsIface.UpdateAccountInput) (*accountsIface.Account, error) {
	acc, err := s.Get(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		acc.Name = *in.Name
	}
	if in.AuthMode != nil {
		acc.AuthMode = *in.AuthMode
	}
	if in.AuthConfig != nil {
		acc.AuthConfig = in.AuthConfig
	}
	if in.PlanTemplate != nil {
		acc.PlanTemplate = *in.PlanTemplate
	}
	if in.Status != nil {
		acc.Status = *in.Status
	}
	if in.Metadata != nil {
		// merge: caller can set keys to "" to clear (we keep them — explicit
		// removal can be a follow-up if needed).
		if acc.Metadata == nil {
			acc.Metadata = map[string]string{}
		}
		for k, v := range in.Metadata {
			acc.Metadata[k] = v
		}
	}
	if err := putKV(ctx, s.db, AccountProfilePath(accountID), acc); err != nil {
		return nil, err
	}
	return acc, nil
}

// Delete removes the Account profile and its slug index. It does NOT
// recursively delete Members/Users/Plans/Tokens — callers should empty those
// first. (Cascading deletion is a follow-up; explicit emptying is safer.)
func (s *accountStore) Delete(ctx context.Context, accountID string) error {
	acc, err := s.Get(ctx, accountID)
	if err != nil {
		return err
	}
	batch, err := s.db.Batch(ctx)
	if err != nil {
		return fmt.Errorf("accounts: batch: %w", err)
	}
	if err := batch.Delete(AccountProfilePath(accountID)); err != nil {
		return fmt.Errorf("accounts: batch delete profile: %w", err)
	}
	if err := batch.Delete(LookupAccountSlugPath(acc.Slug)); err != nil {
		return fmt.Errorf("accounts: batch delete slug index: %w", err)
	}
	if err := batch.Commit(); err != nil {
		return fmt.Errorf("accounts: commit delete: %w", err)
	}
	return nil
}

// lookupIDBySlug returns the account_id for a slug, or "" if not present.
func (s *accountStore) lookupIDBySlug(ctx context.Context, slug string) (string, error) {
	raw, err := s.db.Get(ctx, LookupAccountSlugPath(slug))
	if err != nil {
		if isMissing(err) {
			return "", nil
		}
		return "", fmt.Errorf("accounts: lookup slug: %w", err)
	}
	return string(raw), nil
}

// validateAccountSlug enforces a small, URL-safe alphabet so slugs can appear
// in URLs (e.g. accounts.<network>/<slug>) and in project config without
// quoting.
func validateAccountSlug(slug string) error {
	if slug == "" {
		return errors.New("accounts: slug required")
	}
	if len(slug) > 64 {
		return errors.New("accounts: slug too long (>64)")
	}
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return fmt.Errorf("accounts: slug must be lowercase alnum/-/_; bad char %q", string(r))
		}
	}
	if strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") {
		return errors.New("accounts: slug cannot start/end with '-'")
	}
	return nil
}
