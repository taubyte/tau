//go:build dreaming && !ee

package tests

import (
	"context"
	"testing"
	"time"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	dreamFixtures "github.com/taubyte/tau/dream/fixtures"
	project "github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/services/accounts"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/clients/p2p/accounts/dream"
	_ "github.com/taubyte/tau/dream/fixtures"
	_ "github.com/taubyte/tau/services/accounts/dream"
)

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

	// Post-fixture: same user resolves against the seeded account.
	post, err := cli.Verify(ctx, dreamFixtures.FakeAccountUserProv, dreamFixtures.FakeAccountUserExtID)
	assert.NilError(t, err)
	assert.Equal(t, post.Linked, true)
	assert.Equal(t, len(post.Accounts), 1)
	assert.Equal(t, post.Accounts[0].Slug, dreamFixtures.FakeAccountSlug)
}

// TestStrict_ResolveRejectsBadAccount_Dreaming — fixture seeded, but the
// account slug we ask about doesn't exist; expect valid=false,
// reason=account-not-found. The ee resolve counterpart is in
// strict_ee_test.go.
func TestStrict_ResolveRejectsBadAccount_Dreaming(t *testing.T) {
	u := startAccountsUniverse(t)
	cli := u.Accounts().Client()
	assert.NilError(t, u.RunFixture("fakeAccount"))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	resp, err := cli.Validate(ctx,
		dreamFixtures.FakeAccountUserProv, dreamFixtures.FakeAccountUserExtID,
		project.CloudBinding{Account: "nonexistent-account"},
	)
	assert.NilError(t, err)
	assert.Equal(t, resp.Valid, false)
	assert.Equal(t, resp.Reason, "account not found")
}

// TestStrict_ResolveRejectsUnlinkedUser_Dreaming — the account exists, but the
// git user we're asking about was never linked to it. In the community build,
// linkage IS the access grant, so a linked-but-restricted state doesn't exist.
func TestStrict_ResolveRejectsUnlinkedUser_Dreaming(t *testing.T) {
	u := startAccountsUniverse(t)
	cli := u.Accounts().Client()
	assert.NilError(t, u.RunFixture("fakeAccount"))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// "github:99999" is not the seeded user — the seeded user is "42".
	resp, err := cli.Validate(ctx, "github", "99999", project.CloudBinding{Account: dreamFixtures.FakeAccountSlug})
	assert.NilError(t, err)
	assert.Equal(t, resp.Valid, false)
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
// linkage resolve path picks it up. Proves callers aren't trapped on the
// default "acme/github:42" tuple.
func TestStrict_InjectAccountCustom_Dreaming(t *testing.T) {
	u := startAccountsUniverse(t)
	cli := u.Accounts().Client()

	custom := dreamFixtures.AccountInjection{
		AccountSlug: "umbrella",
		AccountName: "Umbrella Corp",
		UserExtID:   "777",
	}
	assert.NilError(t, u.RunFixture("injectAccount", custom))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// Custom account/user linkage resolves cleanly.
	resp, err := cli.Validate(ctx, "github", "777", project.CloudBinding{Account: "umbrella"})
	assert.NilError(t, err)
	assert.Equal(t, resp.Valid, true)

	// Default seed (acme/github:42) was NOT injected, so it should still
	// reject cleanly — confirms inject is additive, not a wholesale reset.
	miss, err := cli.Validate(ctx, "github", "42", project.CloudBinding{Account: "acme"})
	assert.NilError(t, err)
	assert.Equal(t, miss.Valid, false)
}
