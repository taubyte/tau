//go:build !ee

package accounts

import (
	"context"
	"testing"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	project "github.com/taubyte/tau/pkg/schema/project"
)

// TestValidate_Linkage covers the community Validate: valid iff the account
// named by the binding exists, is active, and the git user is linked to it.
func TestValidate_Linkage(t *testing.T) {
	srv := newTestService(t)
	cli := newInProcessClient(srv)
	ctx := context.Background()

	// Account not found.
	res, err := cli.Validate(ctx, "github", "42", project.CloudBinding{Account: "ghost"})
	if err != nil {
		t.Fatalf("validate ghost: %v", err)
	}
	if res.Valid || res.Reason != "account not found" {
		t.Fatalf("validate ghost: %+v", res)
	}

	accID := seedAccountUser(t, srv, "acme", "github", "42")

	// Account active but git user not linked.
	res, err = cli.Validate(ctx, "github", "unlinked", project.CloudBinding{Account: "acme"})
	if err != nil {
		t.Fatalf("validate unlinked: %v", err)
	}
	if res.Valid || res.Reason != "git user not linked to account" {
		t.Fatalf("validate unlinked: %+v", res)
	}

	// Valid: account active + git user linked.
	res, err = cli.Validate(ctx, "github", "42", project.CloudBinding{Account: "acme"})
	if err != nil || !res.Valid {
		t.Fatalf("validate valid: %v %+v", err, res)
	}

	// Account not active.
	suspended := accountsIface.AccountStatusSuspended
	if _, err := cli.Accounts().Update(ctx, accID, accountsIface.UpdateAccountInput{Status: &suspended}); err != nil {
		t.Fatalf("suspend account: %v", err)
	}
	res, err = cli.Validate(ctx, "github", "42", project.CloudBinding{Account: "acme"})
	if err != nil {
		t.Fatalf("validate suspended: %v", err)
	}
	if res.Valid || res.Reason != "account not active" {
		t.Fatalf("validate suspended: %+v", res)
	}
}
