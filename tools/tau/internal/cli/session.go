package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/tools/tau/internal/api"
	"github.com/taubyte/tau/tools/tau/internal/config"
)

// session.go is signing in to a cloud and connecting forges to the session.
//
// THE PROFILE HOLDS ONE TOKEN: the tau token (and the refresh token that
// renews it). The forges a developer connects are sealed inside it by the
// cloud, so there is no forge token in the config file, and connecting or
// disconnecting a forge is replacing the token with the one the cloud returns.

// profileClient is a client for profile name that renews its token and saves
// the renewal, unless the token came from $TAU_TOKEN.
func profileClient(env Env, cfg *config.Config, name string, p config.Profile) (*api.Client, error) {
	anon, err := api.New(p.Cloud, nil, env.HTTP)
	if err != nil {
		return nil, err
	}
	fromEnv := os.Getenv(config.EnvToken) != ""
	tokens := &api.Refreshing{
		Token: p.Token, Expires: p.Expires, Refresh: p.Refresh,
		Sessions: anon.Anonymous(),
		Now:      func() int64 { return time.Now().Unix() },
		Save: func(token string, expires int64, refresh string) {
			if fromEnv {
				return
			}
			cur := cfg.Profiles[name]
			cur.Token, cur.Expires, cur.Refresh = token, expires, refresh
			cfg.Profiles[name] = cur
			if err := cfg.Save(); err != nil {
				fmt.Fprintf(env.Stderr, "warning: saving the renewed token: %v\n", err)
			}
		},
	}
	return api.New(p.Cloud, tokens, env.HTTP)
}

// keep stores sess as profile name's token.
func keep(cfg *config.Config, name string, sess *authv2.Session, use bool) error {
	p := cfg.Profiles[name]
	p.Token, p.Expires, p.Refresh = sess.GetAccessToken(), sess.GetAccessExpiresUnix(), sess.GetRefreshToken()
	cfg.Profiles[name] = p
	if use || cfg.Current == "" {
		cfg.Current = name
	}
	return cfg.Save()
}

// browse opens url with the platform's opener.
func browse(url string) error {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	return c.Start()
}

func readLine(r io.Reader, what string) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("reading the %s from stdin: nothing there", what)
	}
	if v := strings.TrimSpace(line); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("the %s is empty", what)
}

// signIn is one sign-in or connection, through sessions — anonymous for a
// sign-in, carrying the tau token for a connection.
type signIn struct {
	cmd      *cobra.Command
	env      Env
	sessions interface {
		Authorize(context.Context, *connect.Request[authv2.AuthorizeRequest]) (*connect.Response[authv2.AuthorizeResponse], error)
		Exchange(context.Context, *connect.Request[authv2.ExchangeRequest]) (*connect.Response[authv2.SignedIn], error)
		DeviceStart(context.Context, *connect.Request[authv2.DeviceStartRequest]) (*connect.Response[authv2.DeviceStartResponse], error)
		DevicePoll(context.Context, *connect.Request[authv2.DevicePollRequest]) (*connect.Response[authv2.DevicePollResponse], error)
		Pasted(context.Context, *connect.Request[authv2.PastedRequest]) (*connect.Response[authv2.SignedIn], error)
	}
	invite string
}

// browser signs in through provider with a loopback redirect (RFC 8252).
func (s signIn) browser(provider string) (*authv2.Session, error) {
	ctx, cancel := context.WithTimeout(s.cmd.Context(), flagDuration(s.cmd, "timeout"))
	defer cancel()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer l.Close()
	redirect := "http://" + l.Addr().String() + "/callback"

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	az, err := s.sessions.Authorize(ctx, connect.NewRequest(&authv2.AuthorizeRequest{
		Provider: provider, RedirectUri: redirect, BindingChallenge: base64.RawURLEncoding.EncodeToString(sum[:]), Invite: s.invite,
	}))
	if err != nil {
		return nil, err
	}

	type callback struct{ code, state, err string }
	got := make(chan callback, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		select {
		case got <- callback{q.Get("code"), q.Get("state"), q.Get("error")}:
		default:
		}
		io.WriteString(w, "Signed in. You can close this tab and go back to the terminal.\n")
	})}
	go srv.Serve(l)
	defer srv.Close()

	url := az.Msg.GetUrl()
	fmt.Fprintf(s.env.Stderr, "Open this URL to sign in with %s:\n  %s\n", provider, url)
	if s.env.Browse != nil {
		if err := s.env.Browse(url); err != nil {
			fmt.Fprintf(s.env.Stderr, "(could not open a browser: %v)\n", err)
		}
	}
	var cb callback
	select {
	case cb = <-got:
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for the sign-in to come back: %w", ctx.Err())
	}
	switch {
	case cb.err != "":
		return nil, fmt.Errorf("%s refused the sign-in: %s", provider, cb.err)
	case cb.state != az.Msg.GetState():
		return nil, errors.New("the sign-in came back for another request; try again")
	}
	in, err := s.sessions.Exchange(ctx, connect.NewRequest(&authv2.ExchangeRequest{State: cb.state, Code: cb.code, BindingVerifier: verifier}))
	if err != nil {
		return nil, err
	}
	return in.Msg.GetSession(), nil
}

// device signs in with the device flow (RFC 8628).
func (s signIn) device(provider string) (*authv2.Session, error) {
	ctx, cancel := context.WithTimeout(s.cmd.Context(), flagDuration(s.cmd, "timeout"))
	defer cancel()
	start, err := s.sessions.DeviceStart(ctx, connect.NewRequest(&authv2.DeviceStartRequest{Provider: provider}))
	if err != nil {
		return nil, err
	}
	d := start.Msg
	fmt.Fprintf(s.env.Stderr, "Go to %s and enter the code %s\n", d.GetVerificationUri(), d.GetUserCode())
	if s.env.Browse != nil && d.GetVerificationUriComplete() != "" {
		s.env.Browse(d.GetVerificationUriComplete())
	}
	for {
		poll, err := s.sessions.DevicePoll(ctx, connect.NewRequest(&authv2.DevicePollRequest{Provider: provider, DeviceCode: d.GetDeviceCode(), Invite: s.invite}))
		if err != nil {
			return nil, err
		}
		if !poll.Msg.GetPending() {
			return poll.Msg.GetSignedIn().GetSession(), nil
		}
		select {
		case <-time.After(time.Duration(max(poll.Msg.GetIntervalSeconds(), 1)) * time.Second):
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for the device sign-in: %w", ctx.Err())
		}
	}
}

// pasted signs in with a forge token the developer already holds.
func (s signIn) pasted(provider, token string) (*authv2.Session, error) {
	in, err := s.sessions.Pasted(s.cmd.Context(), connect.NewRequest(&authv2.PastedRequest{Provider: provider, Token: token, Invite: s.invite}))
	if err != nil {
		return nil, err
	}
	return in.Msg.GetSession(), nil
}

// forgeToken is a pasted forge token: --forge-token, stdin with --paste, or
// asked for.
func forgeTokenFlag(cmd *cobra.Command, env Env, host string) (string, bool, error) {
	switch {
	case flagString(cmd, "forge-token") != "":
		return flagString(cmd, "forge-token"), true, nil
	case flagBool(cmd, "paste"):
		v, err := readLine(env.Stdin, "forge token")
		return v, true, err
	}
	return "", false, nil
}

// through runs the flow cmd's flags name for provider.
func (s signIn) through(provider string, p *authv2.Provider) (*authv2.Session, error) {
	tok, pasted, err := forgeTokenFlag(s.cmd, s.env, provider)
	switch {
	case err != nil:
		return nil, err
	case pasted:
		return s.pasted(provider, tok)
	case flagBool(s.cmd, "device"):
		return s.device(provider)
	case p != nil && !p.GetBrowser() && p.GetDevice():
		return s.device(provider)
	case p != nil && !p.GetBrowser() && p.GetPasted() && prompting(s.cmd, s.env):
		tok, err := s.env.Prompter.Secret(provider+" token", "a token "+provider+" accepts, with write access to your repositories")
		if err != nil {
			return nil, err
		}
		return s.pasted(provider, tok)
	case p != nil && !p.GetBrowser():
		return nil, fmt.Errorf("%s offers no browser sign-in here: pass --paste with a token on stdin", provider)
	}
	return s.browser(provider)
}

func providerNamed(ps []*authv2.Provider, id string) *authv2.Provider {
	for _, p := range ps {
		if p.GetId() == id {
			return p
		}
	}
	return nil
}

func addFlowFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("device", false, "use the device flow instead of a browser redirect")
	cmd.Flags().Bool("paste", false, "read a forge token from stdin instead of signing in through a browser")
	cmd.Flags().String("forge-token", "", "a forge token to sign in with (prefer --paste: a flag lands in shell history)")
	cmd.Flags().Duration("timeout", 5*time.Minute, "how long to wait for a browser or device sign-in")
}

func signedInText(env Env, cloud string, sess *authv2.Session) string {
	t := theme(env)
	var b strings.Builder
	b.WriteString(t.Done(fmt.Sprintf("signed in to %s as %s", cloud, sess.GetUser().GetName())))
	for _, c := range sess.GetConnections() {
		fmt.Fprintf(&b, "  %s %s\n", t.Muted.Render("connected"), c.GetHost()+" ("+c.GetLogin()+")")
	}
	return b.String()
}

type sessionJSON struct {
	Profile     string               `json:"profile"`
	Cloud       string               `json:"cloud"`
	User        string               `json:"user"`
	Role        string               `json:"role,omitempty"`
	Connections []*authv2.Connection `json:"connections"`
}

func sessionOut(name, cloud string, sess *authv2.Session) sessionJSON {
	conns := sess.GetConnections()
	if conns == nil {
		conns = []*authv2.Connection{}
	}
	return sessionJSON{Profile: name, Cloud: cloud, User: sess.GetUser().GetName(), Role: roleName(sess.GetUser().GetRole()), Connections: conns}
}

// loginCommand signs in to a cloud and keeps the tau token as a profile.
func loginCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "sign in to a cloud",
		Long: "Signs in with a password (--name, --password-stdin), through a provider (--sso <provider>:\n" +
			"a browser redirect to this machine, --device, or --paste a forge token on stdin), or with an\n" +
			"API token (--token-stdin). The cloud's tau token is stored 0600 in the user config directory;\n" +
			"a forge signed in with is connected inside it (see `tau forge`).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			name := flagString(cmd, "profile")
			if name == "" {
				name = config.DefaultProfile
			}
			p := cfg.Profiles[name]
			if v := flagString(cmd, "cloud"); v != "" {
				p.Cloud = v
			}
			if p.Cloud == "" {
				if p.Cloud, err = need(cmd, env, "cloud", "Cloud URL (the build frontend)", validCloud); err != nil {
					return err
				}
			}
			if err := validCloud(p.Cloud); err != nil {
				return err
			}
			cfg.Profiles[name] = p
			anon, err := api.New(p.Cloud, nil, env.HTTP)
			if err != nil {
				return err
			}

			// AN API TOKEN is kept as it is, once the cloud says who it is.
			if tok, ok, err := apiTokenFlag(cmd, env); err != nil {
				return err
			} else if ok {
				who, err := anon.Anonymous().Whoami(cmd.Context(), bearerReq(&authv2.WhoamiRequest{}, tok))
				if err != nil {
					return err
				}
				if err := keep(cfg, name, &authv2.Session{AccessToken: tok}, flagBool(cmd, "use")); err != nil {
					return err
				}
				sess := &authv2.Session{User: who.Msg}
				return emit(cmd, env, sessionOut(name, p.Cloud, sess), signedInText(env, p.Cloud, sess))
			}

			s := signIn{cmd: cmd, env: env, sessions: anon.Anonymous(), invite: flagString(cmd, "invite")}
			var sess *authv2.Session
			provider := flagString(cmd, "sso")
			user := flagString(cmd, "name")
			switch {
			case provider != "":
				ps, err := anon.Anonymous().Providers(cmd.Context(), connect.NewRequest(&authv2.ProvidersRequest{}))
				if err != nil {
					return err
				}
				if sess, err = s.through(provider, providerNamed(ps.Msg.GetProviders(), provider)); err != nil {
					return err
				}
			case user != "" || flagBool(cmd, "password-stdin"):
				if sess, err = passwordLogin(cmd, env, anon, user); err != nil {
					return err
				}
			case prompting(cmd, env):
				ps, err := anon.Anonymous().Providers(cmd.Context(), connect.NewRequest(&authv2.ProvidersRequest{}))
				if err != nil {
					return err
				}
				var options []string
				if ps.Msg.GetPassword() {
					options = append(options, "password")
				}
				for _, pr := range ps.Msg.GetProviders() {
					options = append(options, pr.GetId())
				}
				pick, err := env.Prompter.Select("Sign in with", options)
				if err != nil {
					return err
				}
				if pick == "password" {
					sess, err = passwordLogin(cmd, env, anon, "")
				} else {
					sess, err = s.through(pick, providerNamed(ps.Msg.GetProviders(), pick))
				}
				if err != nil {
					return err
				}
			default:
				return errors.New("say how to sign in: --name with --password-stdin, --sso <provider>, or --token-stdin")
			}
			if err := keep(cfg, name, sess, flagBool(cmd, "use")); err != nil {
				return err
			}
			return emit(cmd, env, sessionOut(name, p.Cloud, sess), signedInText(env, p.Cloud, sess))
		},
	}
	cmd.Flags().String("name", "", "sign in with a password as this user")
	cmd.Flags().Bool("password-stdin", false, "read the password from stdin")
	cmd.Flags().String("sso", "", "sign in through this provider (a forge host, or an OIDC provider's name)")
	cmd.Flags().String("invite", "", "an invite code, for a first sign-in on an invite-only cloud")
	cmd.Flags().String("token", "", "an API token (prefer --token-stdin: a flag lands in shell history)")
	cmd.Flags().Bool("token-stdin", false, "read an API token from stdin")
	cmd.Flags().Bool("use", true, "make this the current profile")
	addFlowFlags(cmd)
	return cmd
}

func apiTokenFlag(cmd *cobra.Command, env Env) (string, bool, error) {
	switch {
	case flagBool(cmd, "token-stdin"):
		v, err := readLine(env.Stdin, "token")
		return v, true, err
	case flagString(cmd, "token") != "":
		return flagString(cmd, "token"), true, nil
	}
	return "", false, nil
}

func bearerReq[T any](msg *T, token string) *connect.Request[T] {
	r := connect.NewRequest(msg)
	r.Header().Set("Authorization", "Bearer "+token)
	return r
}

func passwordLogin(cmd *cobra.Command, env Env, anon *api.Client, user string) (*authv2.Session, error) {
	var err error
	if user == "" {
		if user, err = need(cmd, env, "name", "User", nil); err != nil {
			return nil, err
		}
	}
	var pw string
	switch {
	case flagBool(cmd, "password-stdin"):
		if pw, err = readLine(env.Stdin, "password"); err != nil {
			return nil, err
		}
	case prompting(cmd, env):
		if pw, err = env.Prompter.Secret("Password", ""); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("--password-stdin is required")
	}
	in, err := anon.Anonymous().Login(cmd.Context(), connect.NewRequest(&authv2.LoginRequest{Name: user, Password: pw}))
	if err != nil {
		return nil, err
	}
	return in.Msg.GetSession(), nil
}

// current is the selected profile, which must be signed in.
func current(cmd *cobra.Command, env Env) (*config.Config, string, config.Profile, *api.Client, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, "", config.Profile{}, nil, err
	}
	name, p, err := cfg.Resolve(flagString(cmd, "profile"), flagString(cmd, "cloud"))
	if err != nil {
		return nil, "", p, nil, err
	}
	if p.Token == "" {
		return nil, "", p, nil, fmt.Errorf("profile %q is not signed in: run `tau login`", name)
	}
	cl, err := profileClient(env, cfg, name, p)
	return cfg, name, p, cl, err
}

func logoutCommand(env Env) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "sign out, and forget the profile's token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			name := flagString(cmd, "profile")
			if name == "" {
				name = cfg.Current
			}
			p, ok := cfg.Profiles[name]
			if !ok {
				return fmt.Errorf("no profile %q", name)
			}
			// THE TOKEN IS FORGOTTEN WHATEVER THE CLOUD SAYS: a cloud that
			// cannot be reached must not keep a developer signed in here.
			if p.Token != "" {
				if cl, err := api.New(p.Cloud, api.Static(p.Token), env.HTTP); err == nil {
					if _, err := cl.Sessions().Logout(cmd.Context(), connect.NewRequest(&authv2.LogoutRequest{RefreshToken: p.Refresh})); err != nil {
						fmt.Fprintf(env.Stderr, "warning: the cloud did not take the sign-out: %v\n", err)
					}
				}
			}
			p.Token, p.Expires, p.Refresh = "", 0, ""
			cfg.Profiles[name] = p
			if err := cfg.Save(); err != nil {
				return err
			}
			return emit(cmd, env, map[string]string{"profile": name}, theme(env).Done(fmt.Sprintf("signed out of %q", name)))
		},
	}
}

func whoamiCommand(env Env) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "who the profile is signed in as, and the forges connected",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, name, p, cl, err := current(cmd, env)
			if err != nil {
				return err
			}
			who, err := cl.Sessions().Whoami(cmd.Context(), connect.NewRequest(&authv2.WhoamiRequest{}))
			if err != nil {
				return err
			}
			conns, err := cl.Sessions().Connections(cmd.Context(), connect.NewRequest(&authv2.ConnectionsRequest{}))
			if err != nil {
				return err
			}
			sess := &authv2.Session{User: who.Msg, Connections: conns.Msg.GetConnections()}
			t := theme(env)
			text := fmt.Sprintf("%s %s on %s\n", t.Title.Render(who.Msg.GetName()), t.Muted.Render("("+roleName(who.Msg.GetRole())+")"), p.Cloud)
			text += connectionsText(env, sess.GetConnections())
			return emit(cmd, env, sessionOut(name, p.Cloud, sess), text)
		},
	}
}

func connectionsText(env Env, conns []*authv2.Connection) string {
	t := theme(env)
	if len(conns) == 0 {
		return t.Muted.Render("no forge connected: run `tau forge connect <host>`") + "\n"
	}
	var b strings.Builder
	for _, c := range conns {
		exp := "no expiry"
		if c.GetExpiresUnix() != 0 {
			exp = "expires " + time.Unix(c.GetExpiresUnix(), 0).UTC().Format(time.RFC3339)
			if c.GetRefreshable() {
				exp += ", renewed by the cloud"
			}
		}
		fmt.Fprintf(&b, "  %s %s %s\n", t.Title.Render(c.GetHost()), c.GetLogin(), t.Muted.Render("("+exp+")"))
	}
	return b.String()
}

// forgeCommand is the forges connected to the session: "connect your git".
func forgeCommand(env Env) *cobra.Command {
	connectCmd := &cobra.Command{
		Use:   "connect <host>",
		Short: "connect a forge to your session, so the cloud acts on its repositories as you",
		Long: "Signs in to the forge through the cloud's OAuth app there (a browser redirect to this\n" +
			"machine, or --device), or takes a token you paste (--paste). The cloud seals the forge's\n" +
			"credential into a new tau token, which replaces the profile's; it stores none itself.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, name, p, cl, err := current(cmd, env)
			if err != nil {
				return err
			}
			host := args[0]
			ps, err := cl.Sessions().Providers(cmd.Context(), connect.NewRequest(&authv2.ProvidersRequest{}))
			if err != nil {
				return err
			}
			pr := providerNamed(ps.Msg.GetProviders(), host)
			if pr == nil || pr.GetKind() != authv2.ProviderKind_PROVIDER_KIND_FORGE {
				return fmt.Errorf("this cloud serves no forge %q", host)
			}
			s := signIn{cmd: cmd, env: env, sessions: cl.Sessions()}
			sess, err := s.through(host, pr)
			if err != nil {
				return err
			}
			if err := keep(cfg, name, sess, false); err != nil {
				return err
			}
			return emit(cmd, env, sessionOut(name, p.Cloud, sess), theme(env).Done("connected "+host)+connectionsText(env, sess.GetConnections()))
		},
	}
	addFlowFlags(connectCmd)

	disconnectCmd := &cobra.Command{
		Use:   "disconnect <host>",
		Short: "drop a forge from your session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, name, p, cl, err := current(cmd, env)
			if err != nil {
				return err
			}
			in, err := cl.Sessions().Disconnect(cmd.Context(), connect.NewRequest(&authv2.DisconnectRequest{Host: args[0]}))
			if err != nil {
				return err
			}
			sess := in.Msg.GetSession()
			if err := keep(cfg, name, sess, false); err != nil {
				return err
			}
			return emit(cmd, env, sessionOut(name, p.Cloud, sess), theme(env).Done("disconnected "+args[0])+connectionsText(env, sess.GetConnections()))
		},
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "the forges connected to your session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _, _, cl, err := current(cmd, env)
			if err != nil {
				return err
			}
			conns, err := cl.Sessions().Connections(cmd.Context(), connect.NewRequest(&authv2.ConnectionsRequest{}))
			if err != nil {
				return err
			}
			list := conns.Msg.GetConnections()
			if list == nil {
				list = []*authv2.Connection{}
			}
			return emit(cmd, env, list, connectionsText(env, list))
		},
	}

	cmd := &cobra.Command{Use: "forge", Short: "connect your git: the forges your session acts on"}
	cmd.AddCommand(connectCmd, disconnectCmd, listCmd)
	return cmd
}
