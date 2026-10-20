package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/tools/tau/internal/config"
)

func (x *tester) profile() config.Profile {
	x.t.Helper()
	cfg, err := loadConfig()
	require.NoError(x.t, err)
	return cfg.Profiles[cfg.Current]
}

// The profile holds one token, and a forge sign-in leaves no forge token in
// the config file.
func TestASSOSignInPastedKeepsOnlyTheTauToken(t *testing.T) {
	x := newTester(t)
	x.stdin = forgeToken + "\n"
	out := x.ok("login", "--cloud", x.f.srv.URL, "--sso", "github.com", "--paste", "-o", "json")
	var got sessionJSON
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Equal(t, "dev", got.User)
	require.Equal(t, "github.com", got.Connections[0].GetHost())

	p := x.profile()
	require.NotEmpty(t, p.Token)
	require.NotEmpty(t, p.Refresh)
	require.NotZero(t, p.Expires)
	path, err := config.Path()
	require.NoError(t, err)
	raw, err := readFile(path)
	require.NoError(t, err)
	require.NotContains(t, raw, forgeToken)

	x.stdin = "wrong\n"
	r := x.run("login", "--cloud", x.f.srv.URL, "--sso", "github.com", "--paste")
	require.Contains(t, r.stderr, "does not accept")
	r = x.run("login", "--cloud", x.f.srv.URL, "--sso", "github.com", "--forge-token", "wrong")
	require.Contains(t, r.stderr, "does not accept")
}

func TestABrowserSignInComesBackToThisMachine(t *testing.T) {
	x := newTester(t)
	// The browser: it follows the URL, which the provider redirects to the
	// loopback listener.
	x.browse = func(u string) error {
		resp, err := http.Get(u)
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}
	out := x.ok("login", "--cloud", x.f.srv.URL, "--sso", "github.com")
	require.Contains(t, out, "signed in to")
	require.Contains(t, out, "github.com")

	// An OIDC provider signs in too, with nothing connected.
	out = x.ok("login", "--cloud", x.f.srv.URL, "--sso", "corp")
	require.NotContains(t, out, "connected")

	// A browser that will not open still shows the URL; the sign-in waits
	// until its timeout.
	x.browse = func(string) error { return errors.New("no display") }
	r := x.run("login", "--cloud", x.f.srv.URL, "--sso", "github.com", "--timeout", "50ms")
	require.Contains(t, r.stderr, "Open this URL")
	require.Contains(t, r.stderr, "could not open a browser")
	require.Contains(t, r.stderr, "waiting for the sign-in")
}

func TestADeviceSignIn(t *testing.T) {
	x := newTester(t)
	r := x.run("login", "--cloud", x.f.srv.URL, "--sso", "github.com", "--device")
	require.Equal(t, 0, r.code, r.stderr)
	require.Contains(t, r.stderr, "ABCD-EFGH")
	require.Contains(t, r.stdout, "github.com")

	// A provider with only a device flow uses it without being told.
	x.f.auth.polls = 0
	r = x.run("login", "--cloud", x.f.srv.URL, "--sso", "devices.example")
	require.Equal(t, 0, r.code, r.stderr)

	// One that only takes a pasted token says so, or asks at a terminal.
	r = x.run("login", "--cloud", x.f.srv.URL, "--sso", "plain.example")
	require.Contains(t, r.stderr, "--paste")
	x.interactive = true
	x.prompter.answers = []string{forgeToken}
	x.ok("login", "--cloud", x.f.srv.URL, "--sso", "plain.example")
	require.Equal(t, []string{"plain.example token"}, x.prompter.asked)
}

func TestPasswordSignIn(t *testing.T) {
	x := newTester(t)
	x.stdin = "hunter2\n"
	out := x.ok("login", "--cloud", x.f.srv.URL, "--name", "root", "--password-stdin")
	require.Contains(t, out, "as root")
	x.stdin = "wrong\n"
	r := x.run("login", "--cloud", x.f.srv.URL, "--name", "root", "--password-stdin")
	require.Contains(t, r.stderr, "wrong")
	r = x.run("login", "--cloud", x.f.srv.URL, "--name", "root")
	require.Contains(t, r.stderr, "--password-stdin is required")
	x.stdin = ""
	r = x.run("login", "--cloud", x.f.srv.URL, "--password-stdin")
	require.Contains(t, r.stderr, "--name is required")
}

// A token about to expire is renewed before it is used, and the renewal is
// saved; a renewed token the cloud hands back replaces it.
func TestATokenIsRenewedAndKept(t *testing.T) {
	x := newTester(t)
	x.f.auth.expiresIn = 30
	x.stdin = forgeToken + "\n"
	x.ok("login", "--cloud", x.f.srv.URL, "--sso", "github.com", "--paste")
	before := x.profile()

	x.ok("job", "job-1")
	after := x.profile()
	require.NotEqual(t, before.Token, after.Token, "renewed before use")
	require.NotEqual(t, before.Refresh, after.Refresh)

	x.f.auth.mu.Lock()
	x.f.auth.sessions["renewed-by-the-cloud"] = x.f.auth.sessions[after.Token]
	x.f.auth.mu.Unlock()
	x.f.auth.expiresIn = 0
	x.f.renew = "renewed-by-the-cloud"
	// Reset the expiry so no refresh races the renewal.
	cfg, err := loadConfig()
	require.NoError(t, err)
	p := cfg.Profiles[cfg.Current]
	p.Expires = 0
	cfg.Profiles[cfg.Current] = p
	require.NoError(t, cfg.Save())
	x.ok("job", "job-1")
	require.Equal(t, "renewed-by-the-cloud", x.profile().Token)

	// A refresh the cloud refuses is a sign-in again.
	p = x.profile()
	p.Expires, p.Refresh = 1, "taur_gone"
	cfg, _ = loadConfig()
	cfg.Profiles[cfg.Current] = p
	require.NoError(t, cfg.Save())
	r := x.run("job", "job-1")
	require.Contains(t, r.stderr, "tau login")
}

// A token from the environment is never written to the config.
func TestAnEnvironmentTokenIsNotSaved(t *testing.T) {
	x := newTester(t)
	x.login()
	t.Setenv(config.EnvToken, goodToken)
	x.f.renew = "from-the-cloud"
	x.f.auth.mu.Lock()
	x.f.auth.sessions["from-the-cloud"] = x.f.auth.sessions[goodToken]
	x.f.auth.mu.Unlock()
	x.ok("job", "job-1")
	t.Setenv(config.EnvToken, "")
	require.Equal(t, goodToken, x.profile().Token)
}

func TestForgeConnectDisconnectList(t *testing.T) {
	x := newTester(t)
	x.stdin = forgeToken + "\n"
	x.ok("login", "--cloud", x.f.srv.URL, "--sso", "github.com", "--paste")
	first := x.profile()

	x.stdin = forgeToken + "\n"
	out := x.ok("forge", "connect", "plain.example", "--paste")
	require.Contains(t, out, "connected plain.example")
	second := x.profile()
	require.NotEqual(t, first.Token, second.Token, "the connection is a new token")

	var list []map[string]any
	require.NoError(t, json.Unmarshal([]byte(x.ok("forge", "list", "-o", "json")), &list))
	require.Len(t, list, 2)
	require.Contains(t, x.ok("whoami"), "plain.example")

	x.ok("forge", "disconnect", "github.com")
	require.NoError(t, json.Unmarshal([]byte(x.ok("forge", "list", "-o", "json")), &list))
	require.Len(t, list, 1)
	x.ok("forge", "disconnect", "plain.example")
	require.Contains(t, x.ok("forge", "list"), "no forge connected")

	// Through a browser, and the device flow.
	x.browse = func(u string) error {
		resp, err := http.Get(u)
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}
	x.ok("forge", "connect", "github.com")
	x.f.auth.polls = 0
	x.ok("forge", "connect", "github.com", "--device")
	require.Contains(t, x.ok("whoami"), "github.com")

	r := x.run("forge", "connect", "corp")
	require.Contains(t, r.stderr, "no forge")
	r = x.run("forge", "connect", "nowhere.example")
	require.Contains(t, r.stderr, "no forge")
}

func TestSessionCommandsNeedASignIn(t *testing.T) {
	x := newTester(t)
	r := x.run("whoami")
	require.Contains(t, r.stderr, "tau login")
	x.login()
	x.ok("logout")
	for _, args := range [][]string{{"whoami"}, {"forge", "list"}, {"forge", "disconnect", "x"}, {"forge", "connect", "x"}, {"users", "list"}, {"tokens", "list"}} {
		r := x.run(args...)
		require.Contains(t, r.stderr, "not signed in", args)
	}
}

func TestLogoutTellsTheCloud(t *testing.T) {
	x := newTester(t)
	x.stdin = "hunter2\n"
	x.ok("login", "--cloud", x.f.srv.URL, "--name", "root", "--password-stdin")
	refresh := x.profile().Refresh
	x.ok("logout")
	require.Equal(t, []string{refresh}, x.f.auth.loggedOut)
	require.Empty(t, x.profile().Refresh)

	// A cloud that cannot be reached does not keep anyone signed in.
	x.login()
	x.f.srv.Close()
	r := x.run("logout")
	require.Equal(t, 0, r.code)
	require.Contains(t, r.stderr, "did not take the sign-out")
	require.Empty(t, x.profile().Token)
}

func TestWhoami(t *testing.T) {
	x := newTester(t)
	x.login()
	require.Contains(t, x.ok("whoami"), "dev")
	var got sessionJSON
	require.NoError(t, json.Unmarshal([]byte(x.ok("whoami", "-o", "json")), &got))
	require.Equal(t, "member", got.Role)
	require.Empty(t, got.Connections)
}

func (x *tester) asAdmin() {
	x.t.Helper()
	x.stdin = "hunter2\n"
	x.ok("login", "--cloud", x.f.srv.URL, "--name", "root", "--password-stdin")
	x.stdin = ""
}

func TestUsers(t *testing.T) {
	x := newTester(t)
	x.login()
	r := x.run("users", "list")
	require.Contains(t, r.stderr, "admins only")

	x.asAdmin()
	out := x.ok("users", "list")
	require.Contains(t, out, "root")
	require.Contains(t, out, "built-in")
	var users []userJSON
	require.NoError(t, json.Unmarshal([]byte(x.ok("users", "list", "-o", "json")), &users))
	require.Len(t, users, 2)

	x.stdin = "s3cret!\n"
	out = x.ok("users", "create", "ada", "--login", "--apikey", "--role", "admin", "--email", "ada@example.com", "--password-stdin")
	require.Contains(t, out, "created ada")
	r = x.run("users", "create", "bot")
	require.Contains(t, r.stderr, "--login, --apikey")
	r = x.run("users", "create", "bot", "--role", "owner", "--apikey")
	require.Contains(t, r.stderr, "admin or member")
	r = x.run("users", "create", "bot", "--login")
	require.Contains(t, r.stderr, "--password-stdin is required")
	x.ok("users", "create", "bot", "--apikey")

	require.Contains(t, x.ok("users", "role", "u-dev", "admin"), "dev is admin")
	x.ok("users", "role", "u-dev", "member")
	r = x.run("users", "role", "u-dev", "owner")
	require.Contains(t, r.stderr, "admin or member")
	r = x.run("users", "role", "u-nobody", "admin")
	require.Contains(t, r.stderr, "no such user")
	require.Contains(t, x.ok("users", "disable", "u-dev"), "disabled dev")
	require.Contains(t, x.ok("users", "list"), "disabled")
	require.Contains(t, x.ok("users", "enable", "u-dev"), "enabled dev")
	r = x.run("users", "disable", "u-nobody")
	require.Contains(t, r.stderr, "no such user")

	r = x.run("users", "delete", "u-dev")
	require.Contains(t, r.stderr, "--yes")
	require.Contains(t, x.ok("users", "delete", "u-dev", "--yes"), "deleted u-dev")
	r = x.run("users", "delete", "u-nobody", "--yes")
	require.Contains(t, r.stderr, "no such user")

	x.stdin = "longenough\n"
	require.Contains(t, x.ok("users", "password", "u-dev", "--password-stdin"), "password set")
	x.stdin = "no\n"
	r = x.run("users", "password", "--password-stdin")
	require.Contains(t, r.stderr, "too short")

	// At a terminal, twice.
	x.interactive = true
	x.prompter.answers = []string{"longenough", "longenough"}
	x.ok("users", "password")
	x.prompter.answers = []string{"longenough", "different"}
	r = x.run("users", "password")
	require.Contains(t, r.stderr, "do not match")
	x.prompter.err = errors.New("cancelled")
	r = x.run("users", "password")
	require.Contains(t, r.stderr, "cancelled")
	x.prompter.answers, x.prompter.err = []string{"longenough"}, nil
	r = x.run("users", "password")
	require.NotZero(t, r.code)
	x.interactive = false

	out = x.ok("users", "invite", "--note", "for ada", "--ttl", "48h")
	require.Contains(t, out, "taui_code")
	var inv map[string]any
	require.NoError(t, json.Unmarshal([]byte(x.ok("users", "invite", "-o", "json")), &inv))
	require.Equal(t, "taui_code", inv["code"])
	r = x.run("users", "invite", "--role", "owner")
	require.Contains(t, r.stderr, "admin or member")
	require.Contains(t, x.ok("users", "invites"), "for ada")
	require.Contains(t, x.ok("users", "uninvite", "inv-1"), "withdrew inv-1")

	x.login()
	for _, args := range [][]string{{"users", "create", "x", "--apikey"}, {"users", "role", "u-dev", "admin"}, {"users", "disable", "u-dev"}, {"users", "delete", "u-dev", "--yes"}, {"users", "invite"}, {"users", "invites"}, {"users", "uninvite", "inv-1"}} {
		r := x.run(args...)
		require.Contains(t, r.stderr, "admins only", args)
	}
	x.stdin = "longenough\n"
	x.ok("users", "password", "--password-stdin")
}

func TestTokens(t *testing.T) {
	x := newTester(t)
	x.login()
	out := x.ok("tokens", "list")
	require.Contains(t, out, "never expires")
	require.Contains(t, out, "expires 2023")
	var list []map[string]any
	require.NoError(t, json.Unmarshal([]byte(x.ok("tokens", "list", "-o", "json", "--user", "u-dev")), &list))
	require.Len(t, list, 2)
	out = x.ok("tokens", "create", "ci", "--ttl", "1h")
	require.Contains(t, out, "tau_secret")
	require.Contains(t, x.ok("tokens", "revoke", "tok-1"), "revoked tok-1")
	r := x.run("tokens", "revoke", "tok-9")
	require.Contains(t, r.stderr, "no such token")

	x.ok("logout")
	t.Setenv(config.EnvToken, "tok-bad")
	for _, args := range [][]string{{"tokens", "list"}, {"tokens", "create", "x"}, {"tokens", "revoke", "tok-1"}, {"users", "password", "--password-stdin"}, {"whoami"}, {"forge", "list"}, {"forge", "disconnect", "x"}} {
		x.stdin = "longenough\n"
		r := x.run(append(args, "--cloud", x.f.srv.URL)...)
		require.Contains(t, r.stderr, "unauthenticated", args)
	}
}

func TestRoleNames(t *testing.T) {
	require.Equal(t, "", roleName(0))
}

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}
