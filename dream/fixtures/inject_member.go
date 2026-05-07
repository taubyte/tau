package fixtures

import (
	"context"
	"fmt"
	"time"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/dream"
)

// MemberInjection is the params shape for `injectMember`. Empty AccountSlug
// falls back to the fakeAccount default.
type MemberInjection struct {
	AccountSlug string
	Email       string
	Role        accountsIface.Role
}

// injectMember is the param-driven sibling of `fakeMember`. Pass one
// MemberInjection. Call multiple times for multiple Members.
func injectMember(u *dream.Universe, params ...any) error {
	svc := u.Accounts()
	if svc == nil {
		return fmt.Errorf("injectMember: accounts service not running in this universe — add `accounts` to UniverseConfig.Services")
	}
	cli := svc.Client()
	if cli == nil {
		return fmt.Errorf("injectMember: accounts service has no Client")
	}
	if len(params) != 1 {
		return fmt.Errorf("injectMember: expected exactly 1 param (MemberInjection), got %d", len(params))
	}
	inj, ok := params[0].(MemberInjection)
	if !ok {
		return fmt.Errorf("injectMember: param 0 is %T, want MemberInjection", params[0])
	}

	if inj.AccountSlug == "" {
		inj.AccountSlug = FakeAccountSlug
	}
	if inj.Email == "" {
		inj.Email = FakeMemberEmail
	}
	if inj.Role == "" {
		inj.Role = FakeMemberRole
	}

	ctx, cancel := context.WithTimeout(u.Context(), 10*time.Second)
	defer cancel()

	acc, err := cli.Accounts().GetBySlug(ctx, inj.AccountSlug)
	if err != nil {
		return fmt.Errorf("injectMember: account %q not found: %w", inj.AccountSlug, err)
	}
	if _, err := cli.Members(acc.ID).Invite(ctx, accountsIface.InviteMemberInput{
		PrimaryEmail: inj.Email,
		Role:         inj.Role,
	}); err != nil {
		return fmt.Errorf("injectMember: invite %q on %q: %w", inj.Email, inj.AccountSlug, err)
	}
	return nil
}
