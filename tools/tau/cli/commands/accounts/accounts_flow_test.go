package accounts_test

import (
	"os"
	"strings"
	"testing"

	"github.com/h2non/gock"
	"github.com/taubyte/tau/tools/tau/cli"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

const accountsMockURL = "https://accounts.mock"

// A profile carrying an accounts session bearer, on the test cloud, so the
// accounts client resolves its URL from TAUBYTE_ACCOUNTS_URL.
func accountsConfig(projectPath string) string {
	return `profiles:
  test:
    provider: github
    token: "123456"
    default: true
    git_username: taubyte-test
    git_email: t@t.com
    type: test
    network: sandbox.taubyte.com
    accounts_session: "sess-abc"
projects:
  test_project:
    defaultprofile: test
    location: ` + projectPath + "\n"
}

// mockAccounts gocks the accounts-service endpoints the CLI calls: /me, the
// member/user management actions (dispatched by the request body's "action"),
// and /logout. Returns a cleanup.
func mockAccounts(t *testing.T) func() {
	t.Helper()
	os.Setenv("TAUBYTE_ACCOUNTS_URL", accountsMockURL)
	gock.DisableNetworking()

	member := map[string]any{"id": "m2", "primary_email": "invitee@t.com", "role": "admin", "status": "invited"}
	gock.New(accountsMockURL).Get("/me").Persist().Reply(200).JSON(map[string]any{
		"member":   map[string]any{"id": "m1", "primary_email": "me@t.com", "role": "owner", "status": "active"},
		"accounts": []map[string]any{{"id": "acc-1", "slug": "acme", "name": "Acme Inc"}},
		"session":  map[string]any{"id": "s1", "expires_at": "2030-01-02T15:04:05Z"},
	})

	// One reply per (path, action); the action is matched in the request body.
	act := func(path, action string, body map[string]any) {
		gock.New(accountsMockURL).Post(path).
			BodyString(`"action":"` + action + `"`).
			Persist().Reply(200).JSON(body)
	}
	act("/members", "invite", map[string]any{"member": member})
	act("/members", "list", map[string]any{"ids": []string{"m2"}})
	act("/members", "get", map[string]any{"member": member})
	act("/users", "add", map[string]any{"user": map[string]any{"id": "u2", "provider": "github", "external_id": "42", "display_name": "octocat"}})
	act("/users", "list", map[string]any{"ids": []string{"u2"}})
	act("/users", "remove", map[string]any{"ok": true})

	gock.New(accountsMockURL).Post("/logout").Persist().Reply(200).JSON(map[string]any{"ok": true})

	gock.Intercept()
	return func() {
		gock.Off()
		gock.EnableNetworking()
		os.Unsetenv("TAUBYTE_ACCOUNTS_URL")
	}
}

// rawRun runs the full app without cli.Run's argument reordering, which only
// rewrites one subcommand level and scrambles the 3-level accounts tree
// (accounts members invite ...). The args here are already well-formed.
func rawRun(args ...string) error {
	app, err := cli.New()
	if err != nil {
		return err
	}
	return app.Run(args)
}

func runAcc(t *testing.T, dir, projectPath string, args ...string) (string, error) {
	stdout, _, err := testutil.RunCLIWithDirAndCwdWithAuthMock(t, rawRun, dir, projectPath,
		accountsConfig(projectPath), append([]string{"accounts"}, args...)...)
	return stdout, err
}

// Each command is a subtest so the harness's stdout capture (cleanups fire at
// the subtest's end) doesn't nest across calls.
func TestAccountsFlow(t *testing.T) {
	defer mockAccounts(t)()
	dir := t.TempDir()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"whoami", []string{"whoami"}, "me@t.com"},
		{"list", []string{"list"}, "acme"},
		{"members-list", []string{"members", "list", "acme"}, "invitee@t.com"},
		{"members-invite", []string{"members", "invite", "--email", "x@t.com", "--role", "admin", "acme"}, "Invited"},
		{"users-list", []string{"users", "list", "acme"}, "u2"},
		{"users-add", []string{"users", "add", "--external-id", "42", "--display", "octocat", "acme"}, "Linked"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := runAcc(t, dir, dir, c.args...)
			assert.NilError(t, err)
			assert.Assert(t, strings.Contains(out, c.want), out)
		})
	}

	t.Run("users-remove", func(t *testing.T) {
		_, err := runAcc(t, dir, dir, "users", "remove", "acme", "u2")
		assert.NilError(t, err)
	})
	t.Run("logout", func(t *testing.T) {
		_, err := runAcc(t, dir, dir, "logout")
		assert.NilError(t, err)
	})
}

// An unknown account slug and a bad role are rejected with clear errors.
func TestAccountsErrors(t *testing.T) {
	defer mockAccounts(t)()
	dir := t.TempDir()

	t.Run("unknown-slug", func(t *testing.T) {
		_, err := runAcc(t, dir, dir, "members", "list", "ghost")
		assert.ErrorContains(t, err, "not found")
	})
	t.Run("bad-role", func(t *testing.T) {
		_, err := runAcc(t, dir, dir, "members", "invite", "--email", "x@t.com", "--role", "wizard", "acme")
		assert.ErrorContains(t, err, "invalid --role")
	})
}
