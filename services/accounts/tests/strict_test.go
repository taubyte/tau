//go:build dreaming

package tests

import (
	"context"
	"testing"
	"time"

	commonIface "github.com/taubyte/tau/core/common"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/dream"
	dreamFixtures "github.com/taubyte/tau/dream/fixtures"
	"github.com/taubyte/tau/services/accounts"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/accounts/dream"
	_ "github.com/taubyte/tau/dream/fixtures"
	_ "github.com/taubyte/tau/services/accounts/dream"
)

// startAccountsUniverse boots a dream universe with only the accounts
// service running, so each strict-mode test gets fresh KV state without
// any of the other services' setup cost.
func startAccountsUniverse(t *testing.T) *dream.Universe {
	t.Helper()

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	assert.NilError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"accounts": {},
		},
	}))

	// Service settle window matches the existing TestAccounts_Dreaming
	// pattern. Without this the first wire call sometimes lands before
	// the stream registers.
	time.Sleep(500 * time.Millisecond)
	return u
}

// TestStrict_VerifyRejectsUnlinked_Dreaming — a real accounts service running in
// dream, no fixture seeded. Any (provider, external_id) we ask about
// should come back as `linked: false`. This is the http_auth.go gate
// that turns into "no tau account linked to this github identity — sign
// up at <url>" at the user-facing edge.
func TestStrict_VerifyRejectsUnlinked_Dreaming(t *testing.T) {
	u := startAccountsUniverse(t)
	cli := u.Accounts().Client()
	assert.Assert(t, cli != nil)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	resp, err := cli.Verify(ctx, "github", "99999")
	assert.NilError(t, err)
	assert.Equal(t, resp.Linked, false, "expected unlinked github user to verify as not-linked")
	assert.Equal(t, len(resp.Accounts), 0)
}

// TestStrict_VerifyAcceptsAfterFixture_Dreaming — sanity check that the fixture
// itself works: same "linked" identity flips from false to true after
// the fakeAccount fixture seeds the default state. Demonstrates the
// fixture's effect on the verify path.
func TestStrict_VerifyAcceptsAfterFixture_Dreaming(t *testing.T) {
	u := startAccountsUniverse(t)
	cli := u.Accounts().Client()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// Pre-fixture: the seeded user doesn't exist yet.
	pre, err := cli.Verify(ctx, dreamFixtures.FakeAccountUserProv, dreamFixtures.FakeAccountUserExtID)
	assert.NilError(t, err)
	assert.Equal(t, pre.Linked, false)

	assert.NilError(t, u.RunFixture("fakeAccount"))

	// Post-fixture: same user resolves with the seeded account+plan.
	post, err := cli.Verify(ctx, dreamFixtures.FakeAccountUserProv, dreamFixtures.FakeAccountUserExtID)
	assert.NilError(t, err)
	assert.Equal(t, post.Linked, true)
	assert.Equal(t, len(post.Accounts), 1)
	assert.Equal(t, post.Accounts[0].Slug, dreamFixtures.FakeAccountSlug)
	assert.Equal(t, len(post.Accounts[0].Plans), 1)
	assert.Equal(t, post.Accounts[0].Plans[0].Slug, dreamFixtures.FakeAccountPlan)
}

// TestStrict_ResolvePlanRejectsBadPlan_Dreaming — fixture seeded, but the
// plan slug we ask for doesn't exist; expect valid=false, reason=plan-not-found.
func TestStrict_ResolvePlanRejectsBadPlan_Dreaming(t *testing.T) {
	u := startAccountsUniverse(t)
	cli := u.Accounts().Client()
	assert.NilError(t, u.RunFixture("fakeAccount"))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	resp, err := cli.ResolvePlan(ctx,
		dreamFixtures.FakeAccountSlug, "nonexistent-plan",
		dreamFixtures.FakeAccountUserProv, dreamFixtures.FakeAccountUserExtID,
	)
	assert.NilError(t, err)
	assert.Equal(t, resp.Valid, false)
	assert.Equal(t, resp.Reason, "plan not found")
}

// TestStrict_ResolvePlanRejectsBadAccount_Dreaming — same shape, but the account
// slug doesn't exist. Distinct rejection reason so the user can tell
// "I typo'd the account" from "I typo'd the plan".
func TestStrict_ResolvePlanRejectsBadAccount_Dreaming(t *testing.T) {
	u := startAccountsUniverse(t)
	cli := u.Accounts().Client()
	assert.NilError(t, u.RunFixture("fakeAccount"))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	resp, err := cli.ResolvePlan(ctx,
		"nonexistent-account", dreamFixtures.FakeAccountPlan,
		dreamFixtures.FakeAccountUserProv, dreamFixtures.FakeAccountUserExtID,
	)
	assert.NilError(t, err)
	assert.Equal(t, resp.Valid, false)
	assert.Equal(t, resp.Reason, "account not found")
}

// TestStrict_ResolvePlanRejectsUngrantedUser_Dreaming — the account+plan exist,
// but the git user we're asking about has no grant on the plan. This is
// the "team member tried to deploy under a plan they don't have access
// to" path. Reason carries enough info for the user to ask their admin
// for a grant.
func TestStrict_ResolvePlanRejectsUngrantedUser_Dreaming(t *testing.T) {
	u := startAccountsUniverse(t)
	cli := u.Accounts().Client()
	assert.NilError(t, u.RunFixture("fakeAccount"))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// "github:99999" is not the seeded user — the seeded user is "42".
	resp, err := cli.ResolvePlan(ctx,
		dreamFixtures.FakeAccountSlug, dreamFixtures.FakeAccountPlan,
		"github", "99999",
	)
	assert.NilError(t, err)
	assert.Equal(t, resp.Valid, false)
	// The reason for "user exists in no Account on this network" is the
	// same as "user is in another Account but not this one" — both lead
	// to "git user not linked to account".
	assert.Equal(t, resp.Reason, "git user not linked to account")
}

// TestStrict_FakeMemberLogin_Dreaming exercises the full Member-side fixture chain:
// fakeAccount + fakeMember invites a default Member, then we mint a session
// bearer through the dream-tagged helper. Proves that "I want a logged-in
// caller in dream" is a one-fixture-pair away from working.
//
// This is the supported way to skip the magic-link round-trip in tests
// that aren't *about* login. The bearer is a valid Member-session token —
// it round-trips through the real `sessions.Verify` path on subsequent
// requests, just without the email side-channel.
func TestStrict_FakeMemberLogin_Dreaming(t *testing.T) {
	u := startAccountsUniverse(t)
	assert.NilError(t, u.RunFixture("fakeAccount"))
	assert.NilError(t, u.RunFixture("fakeMember"))

	svc := u.Accounts()
	realSvc, ok := svc.(*accounts.AccountsService)
	assert.Assert(t, ok, "accounts service is not the expected type")

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	bearer, accID, memberID, err := realSvc.IssueTestSessionForMember(ctx, dreamFixtures.FakeMemberEmail)
	assert.NilError(t, err, "fakeMember should have invited %q", dreamFixtures.FakeMemberEmail)
	assert.Assert(t, bearer != "", "bearer empty")
	assert.Assert(t, accID != "", "accountID empty")
	assert.Assert(t, memberID != "", "memberID empty")
}

// TestStrict_InjectMemberCustom_Dreaming proves `injectMember` accepts a custom
// account / email / role. Pairs with `injectAccount` to seed multi-account
// dream universes where each Account has its own admin Member.
func TestStrict_InjectMemberCustom_Dreaming(t *testing.T) {
	u := startAccountsUniverse(t)

	// Seed a non-default Account first so `injectMember` has somewhere to
	// invite into.
	assert.NilError(t, u.RunFixture("injectAccount", dreamFixtures.AccountInjection{
		AccountSlug: "umbrella",
		AccountName: "Umbrella Corp",
		PlanSlug:    "enterprise",
	}))

	assert.NilError(t, u.RunFixture("injectMember", dreamFixtures.MemberInjection{
		AccountSlug: "umbrella",
		Email:       "wesker@umbrella.test",
		Role:        accountsIface.RoleAdmin,
	}))

	svc := u.Accounts()
	realSvc, ok := svc.(*accounts.AccountsService)
	assert.Assert(t, ok)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	bearer, _, _, err := realSvc.IssueTestSessionForMember(ctx, "wesker@umbrella.test")
	assert.NilError(t, err)
	assert.Assert(t, bearer != "")
}

// TestStrict_InjectAccountCustom_Dreaming — verifies the param-driven fixture path:
// inject a non-default account (different slug + user) and assert the
// resolve path picks it up. Proves callers aren't trapped on the default
// "acme/prod/github:42" tuple.
func TestStrict_InjectAccountCustom_Dreaming(t *testing.T) {
	u := startAccountsUniverse(t)
	cli := u.Accounts().Client()

	custom := dreamFixtures.AccountInjection{
		AccountSlug: "umbrella",
		AccountName: "Umbrella Corp",
		PlanSlug:    "enterprise",
		UserExtID:   "777",
	}
	assert.NilError(t, u.RunFixture("injectAccount", custom))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// Custom plan resolves cleanly.
	resp, err := cli.ResolvePlan(ctx, "umbrella", "enterprise", "github", "777")
	assert.NilError(t, err)
	assert.Equal(t, resp.Valid, true)

	// Default seed (acme/prod/42) was NOT injected, so it should still
	// reject cleanly — confirms inject is additive, not a wholesale reset.
	miss, err := cli.ResolvePlan(ctx, "acme", "prod", "github", "42")
	assert.NilError(t, err)
	assert.Equal(t, miss.Valid, false)
}
