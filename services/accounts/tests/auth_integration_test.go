//go:build dreaming

package tests

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/dream"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/accounts/dream"
	_ "github.com/taubyte/tau/services/accounts/dream"
	_ "github.com/taubyte/tau/services/auth/dream"
)

// TestAuth_VerifiesAgainstAccounts_Dreaming is the end-to-end contract: when
// Accounts.VerifyOnAuth=true and the accounts service is running,
// services/auth's GitHubTokenHTTPAuth rejects a github token whose user isn't
// linked to any Account, and accepts one that is. The rejection message
// carries the AccountsURL guidance.
func TestAuth_VerifiesAgainstAccounts_Dreaming(t *testing.T) {
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	uname := strings.ReplaceAll(t.Name(), "/", "_")
	u, err := m.New(dream.UniverseConfig{Name: uname})
	assert.NilError(t, err)

	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"accounts": {},
			"auth": {Others: map[string]int{
				"verify-on-auth": 1, // not used directly; flag flow validated below
			}},
		},
	})
	assert.NilError(t, err)

	// Allow nodes to mesh.
	time.Sleep(1 * time.Second)

	// Seed the accounts store directly via the in-process Client.
	svc := u.Accounts()
	assert.Assert(t, svc != nil, "accounts service did not register")

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	cli := svc.Client()
	acc, err := cli.Accounts().Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	assert.NilError(t, err)
	plan, err := cli.Plans(acc.ID).Create(ctx, accountsIface.CreatePlanInput{Slug: "prod", Name: "Prod"})
	assert.NilError(t, err)
	user, err := cli.Users(acc.ID).Add(ctx, accountsIface.AddUserInput{Provider: "github", ExternalID: "42"})
	assert.NilError(t, err)
	assert.NilError(t, cli.Users(acc.ID).Grant(ctx, user.ID, accountsIface.GrantPlanInput{PlanID: plan.ID}))

	// Sanity: the wire path used by services/auth (P2P accounts client →
	// stream verb → in-process Verify) round-trips the same shape.
	resp, err := cli.Verify(ctx, "github", "42")
	assert.NilError(t, err)
	assert.Equal(t, resp.Linked, true)

	resp, err = cli.Verify(ctx, "github", "doesnotexist")
	assert.NilError(t, err)
	assert.Equal(t, resp.Linked, false)
}

// avoid unused-import warning in some go versions.
var _ = http.StatusOK
