package fixtures

import (
	"context"
	"fmt"
	"time"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/dream"
)

// AccountInjection is the params shape for `injectAccount`. Empty fields
// fill from the `FakeAccount*` defaults.
type AccountInjection struct {
	AccountSlug string
	AccountName string
	PlanSlug    string
	PlanName    string
	PlanMode    accountsIface.PlanMode
	UserProv    string
	UserExtID   string
	UserDisplay string
}

// injectAccount is the param-driven sibling of `fakeAccount`. Pass one
// AccountInjection. Call multiple times for multiple seeds.
func injectAccount(u *dream.Universe, params ...any) error {
	svc := u.Accounts()
	if svc == nil {
		return fmt.Errorf("injectAccount: accounts service not running in this universe — add `accounts` to UniverseConfig.Services")
	}
	cli := svc.Client()
	if cli == nil {
		return fmt.Errorf("injectAccount: accounts service has no Client")
	}
	if len(params) != 1 {
		return fmt.Errorf("injectAccount: expected exactly 1 param (AccountInjection), got %d", len(params))
	}
	inj, ok := params[0].(AccountInjection)
	if !ok {
		return fmt.Errorf("injectAccount: param 0 is %T, want AccountInjection", params[0])
	}

	if inj.AccountSlug == "" {
		inj.AccountSlug = FakeAccountSlug
	}
	if inj.AccountName == "" {
		inj.AccountName = FakeAccountName
	}
	if inj.PlanSlug == "" {
		inj.PlanSlug = FakeAccountPlan
	}
	if inj.PlanName == "" {
		inj.PlanName = "Production"
	}
	if inj.PlanMode == "" {
		inj.PlanMode = accountsIface.PlanModeQuota
	}
	if inj.UserProv == "" {
		inj.UserProv = FakeAccountUserProv
	}
	if inj.UserExtID == "" {
		inj.UserExtID = FakeAccountUserExtID
	}
	if inj.UserDisplay == "" {
		inj.UserDisplay = "alice"
	}

	ctx, cancel := context.WithTimeout(u.Context(), 10*time.Second)
	defer cancel()

	acc, err := cli.Accounts().Create(ctx, accountsIface.CreateAccountInput{
		Slug: inj.AccountSlug,
		Name: inj.AccountName,
	})
	if err != nil {
		return fmt.Errorf("injectAccount: create account %q: %w", inj.AccountSlug, err)
	}
	plan, err := cli.Plans(acc.ID).Create(ctx, accountsIface.CreatePlanInput{
		Slug: inj.PlanSlug,
		Name: inj.PlanName,
		Mode: inj.PlanMode,
	})
	if err != nil {
		return fmt.Errorf("injectAccount: create plan %q under %q: %w", inj.PlanSlug, inj.AccountSlug, err)
	}
	user, err := cli.Users(acc.ID).Add(ctx, accountsIface.AddUserInput{
		Provider:    inj.UserProv,
		ExternalID:  inj.UserExtID,
		DisplayName: inj.UserDisplay,
	})
	if err != nil {
		return fmt.Errorf("injectAccount: add user %s/%s: %w", inj.UserProv, inj.UserExtID, err)
	}
	if err := cli.Users(acc.ID).Grant(ctx, user.ID, accountsIface.GrantPlanInput{PlanID: plan.ID}); err != nil {
		return fmt.Errorf("injectAccount: grant plan %q to user %s/%s: %w", inj.PlanSlug, inj.UserProv, inj.UserExtID, err)
	}
	return nil
}
