package fixtures

import (
	"context"
	"fmt"
	"time"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/dream"
)

const (
	FakeMemberEmail = "alice@example.com"
	FakeMemberRole  = accountsIface.RoleOwner
)

// fakeMember invites a default Member into the `fakeAccount`-seeded Account.
// Composes with fakeAccount; doesn't pre-issue a session bearer (tests that
// want one mint via `realSvc.IssueTestSessionForMember` — gated by
// -tags=dreaming).
func fakeMember(u *dream.Universe, params ...any) error {
	svc := u.Accounts()
	if svc == nil {
		return fmt.Errorf("fakeMember: accounts service not running in this universe — add `accounts` to UniverseConfig.Services")
	}
	cli := svc.Client()
	if cli == nil {
		return fmt.Errorf("fakeMember: accounts service has no Client")
	}

	ctx, cancel := context.WithTimeout(u.Context(), 10*time.Second)
	defer cancel()

	acc, err := cli.Accounts().GetBySlug(ctx, FakeAccountSlug)
	if err != nil {
		return fmt.Errorf("fakeMember: account %q not found — run `fakeAccount` first or use `injectMember`: %w", FakeAccountSlug, err)
	}
	if _, err := cli.Members(acc.ID).Invite(ctx, accountsIface.InviteMemberInput{
		PrimaryEmail: FakeMemberEmail,
		Role:         FakeMemberRole,
	}); err != nil {
		return fmt.Errorf("fakeMember: invite %q: %w", FakeMemberEmail, err)
	}
	return nil
}
