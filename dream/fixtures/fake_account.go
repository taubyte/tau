package fixtures

import (
	"context"
	"fmt"
	"time"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/dream"
)

// Default seed values for `fakeAccount`. Use `injectAccount` for custom shapes.
const (
	FakeAccountSlug      = "acme"
	FakeAccountName      = "Acme Corp"
	FakeAccountPlan      = "prod"
	FakeAccountUserProv  = "github"
	FakeAccountUserExtID = "42"
)

// fakeAccount seeds one (account, plan, user, grant) tuple. After the fixture
// runs, Verify and ResolvePlan succeed for the seeded identity. Tests needing
// a different shape script CRUD inline or call `injectAccount`.
func fakeAccount(u *dream.Universe, params ...any) error {
	svc := u.Accounts()
	if svc == nil {
		return fmt.Errorf("fakeAccount: accounts service not running in this universe — add `accounts` to UniverseConfig.Services")
	}
	cli := svc.Client()
	if cli == nil {
		return fmt.Errorf("fakeAccount: accounts service has no Client")
	}

	ctx, cancel := context.WithTimeout(u.Context(), 10*time.Second)
	defer cancel()

	acc, err := cli.Accounts().Create(ctx, accountsIface.CreateAccountInput{
		Slug: FakeAccountSlug,
		Name: FakeAccountName,
	})
	if err != nil {
		return fmt.Errorf("fakeAccount: create account: %w", err)
	}
	plan, err := cli.Plans(acc.ID).Create(ctx, accountsIface.CreatePlanInput{
		Slug: FakeAccountPlan,
		Name: "Production",
		Mode: accountsIface.PlanModeQuota,
	})
	if err != nil {
		return fmt.Errorf("fakeAccount: create plan: %w", err)
	}
	user, err := cli.Users(acc.ID).Add(ctx, accountsIface.AddUserInput{
		Provider:    FakeAccountUserProv,
		ExternalID:  FakeAccountUserExtID,
		DisplayName: "alice",
	})
	if err != nil {
		return fmt.Errorf("fakeAccount: add user: %w", err)
	}
	if err := cli.Users(acc.ID).Grant(ctx, user.ID, accountsIface.GrantPlanInput{PlanID: plan.ID}); err != nil {
		return fmt.Errorf("fakeAccount: grant plan to user: %w", err)
	}
	return nil
}
