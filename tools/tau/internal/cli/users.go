package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
)

// users.go is a cloud's users, for its admins, and anyone's own API tokens.

func roleName(r authv2.Role) string {
	switch r {
	case authv2.Role_ROLE_ADMIN:
		return "admin"
	case authv2.Role_ROLE_MEMBER:
		return "member"
	}
	return ""
}

func parseRole(s string) (authv2.Role, error) {
	switch s {
	case "admin":
		return authv2.Role_ROLE_ADMIN, nil
	case "member", "":
		return authv2.Role_ROLE_MEMBER, nil
	}
	return 0, fmt.Errorf("a role is admin or member, not %q", s)
}

type userJSON struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Email    string   `json:"email,omitempty"`
	Role     string   `json:"role"`
	Disabled bool     `json:"disabled,omitempty"`
	Builtin  bool     `json:"builtin,omitempty"`
	Links    []string `json:"links,omitempty"`
}

func userOut(u *authv2.User) userJSON {
	j := userJSON{ID: u.GetId(), Name: u.GetName(), Email: u.GetEmail(), Role: roleName(u.GetRole()), Disabled: u.GetDisabled(), Builtin: u.GetBuiltin()}
	for _, l := range u.GetLinks() {
		j.Links = append(j.Links, l.GetIssuer()+"/"+l.GetLogin())
	}
	return j
}

func userLine(env Env, u *authv2.User) string {
	t := theme(env)
	state := ""
	if u.GetDisabled() {
		state = t.Warn.Render(" disabled")
	}
	if u.GetBuiltin() {
		state += t.Muted.Render(" built-in")
	}
	return fmt.Sprintf("%s %s %s%s\n", t.Title.Render(u.GetName()), t.Muted.Render(u.GetId()), roleName(u.GetRole()), state)
}

func usersCommand(env Env) *cobra.Command {
	list := &cobra.Command{
		Use:   "list",
		Short: "list the cloud's users",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _, _, cl, err := current(cmd, env)
			if err != nil {
				return err
			}
			resp, err := cl.Users().ListUsers(cmd.Context(), connect.NewRequest(&authv2.ListUsersRequest{}))
			if err != nil {
				return err
			}
			out := []userJSON{}
			var b strings.Builder
			for _, u := range resp.Msg.GetUsers() {
				out = append(out, userOut(u))
				b.WriteString(userLine(env, u))
			}
			return emit(cmd, env, out, b.String())
		},
	}

	create := &cobra.Command{
		Use:   "create <name>",
		Short: "create a local user: one who signs in with a password (--login) or holds API tokens (--apikey)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _, _, cl, err := current(cmd, env)
			if err != nil {
				return err
			}
			role, err := parseRole(flagString(cmd, "role"))
			if err != nil {
				return err
			}
			req := &authv2.CreateUserRequest{Name: args[0], Email: flagString(cmd, "email"), Role: role}
			if flagBool(cmd, "login") {
				req.Capabilities = append(req.Capabilities, authv2.Capability_CAPABILITY_LOGIN)
				if req.Password, err = newPassword(cmd, env); err != nil {
					return err
				}
			}
			if flagBool(cmd, "apikey") {
				req.Capabilities = append(req.Capabilities, authv2.Capability_CAPABILITY_APIKEY)
			}
			if len(req.Capabilities) == 0 {
				return errors.New("a local user needs --login, --apikey, or both")
			}
			u, err := cl.Users().CreateUser(cmd.Context(), connect.NewRequest(req))
			if err != nil {
				return err
			}
			return emit(cmd, env, userOut(u.Msg), theme(env).Done("created "+u.Msg.GetName())+userLine(env, u.Msg))
		},
	}
	create.Flags().String("email", "", "the user's email")
	create.Flags().String("role", "member", "admin or member")
	create.Flags().Bool("login", false, "may sign in with a password")
	create.Flags().Bool("apikey", false, "may hold API tokens")
	create.Flags().Bool("password-stdin", false, "read the password from stdin")

	idCmd := func(use, short, done string, call func(cmd *cobra.Command, id string) (*authv2.User, error)) *cobra.Command {
		return &cobra.Command{
			Use:   use,
			Short: short,
			Args:  cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				u, err := call(cmd, args[0])
				if err != nil {
					return err
				}
				if u == nil {
					return emit(cmd, env, map[string]string{"id": args[0]}, theme(env).Done(done+" "+args[0]))
				}
				return emit(cmd, env, userOut(u), theme(env).Done(done+" "+u.GetName())+userLine(env, u))
			},
		}
	}
	setDisabled := func(disabled bool) func(*cobra.Command, string) (*authv2.User, error) {
		return func(cmd *cobra.Command, id string) (*authv2.User, error) {
			_, _, _, cl, err := current(cmd, env)
			if err != nil {
				return nil, err
			}
			u, err := cl.Users().SetDisabled(cmd.Context(), connect.NewRequest(&authv2.SetDisabledRequest{Id: id, Disabled: disabled}))
			if err != nil {
				return nil, err
			}
			return u.Msg, nil
		}
	}
	disable := idCmd("disable <id>", "stop a user signing in; their tokens stop working at once", "disabled", setDisabled(true))
	enable := idCmd("enable <id>", "let a disabled user sign in again", "enabled", setDisabled(false))
	remove := idCmd("delete <id>", "delete a user", "deleted", func(cmd *cobra.Command, id string) (*authv2.User, error) {
		if err := confirm(cmd, env, "Delete user "+id+"?"); err != nil {
			return nil, err
		}
		_, _, _, cl, err := current(cmd, env)
		if err != nil {
			return nil, err
		}
		_, err = cl.Users().DeleteUser(cmd.Context(), connect.NewRequest(&authv2.UserRequest{Id: id}))
		return nil, err
	})
	role := &cobra.Command{
		Use:   "role <id> <admin|member>",
		Short: "set a user's role",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := parseRole(args[1])
			if err != nil {
				return err
			}
			_, _, _, cl, err := current(cmd, env)
			if err != nil {
				return err
			}
			u, err := cl.Users().SetRole(cmd.Context(), connect.NewRequest(&authv2.SetRoleRequest{Id: args[0], Role: r}))
			if err != nil {
				return err
			}
			return emit(cmd, env, userOut(u.Msg), theme(env).Done(u.Msg.GetName()+" is "+args[1]))
		},
	}

	password := &cobra.Command{
		Use:   "password [id]",
		Short: "set a password: your own, or (admin) a user's",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _, _, cl, err := current(cmd, env)
			if err != nil {
				return err
			}
			pw, err := newPassword(cmd, env)
			if err != nil {
				return err
			}
			if _, err := cl.Users().SetPassword(cmd.Context(), connect.NewRequest(&authv2.SetPasswordRequest{Id: arg(args, 0), Password: pw})); err != nil {
				return err
			}
			return emit(cmd, env, map[string]string{"id": arg(args, 0)}, theme(env).Done("password set"))
		},
	}
	password.Flags().Bool("password-stdin", false, "read the password from stdin")

	invite := &cobra.Command{
		Use:   "invite",
		Short: "make an invite code a person signs in with for the first time",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _, _, cl, err := current(cmd, env)
			if err != nil {
				return err
			}
			r, err := parseRole(flagString(cmd, "role"))
			if err != nil {
				return err
			}
			inv, err := cl.Users().CreateInvite(cmd.Context(), connect.NewRequest(&authv2.CreateInviteRequest{
				Role: r, TtlSeconds: int64(flagDuration(cmd, "ttl") / time.Second), Note: flagString(cmd, "note"),
			}))
			if err != nil {
				return err
			}
			i := inv.Msg
			return emit(cmd, env, inviteOut(i), theme(env).Done("invite "+i.GetId())+"  code (shown once): "+i.GetCode()+"\n")
		},
	}
	invite.Flags().String("role", "member", "the role the invite grants")
	invite.Flags().Duration("ttl", 0, "how long the invite lasts (default the cloud's, seven days)")
	invite.Flags().String("note", "", "who the invite is for")

	invites := &cobra.Command{
		Use:   "invites",
		Short: "list open invites",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _, _, cl, err := current(cmd, env)
			if err != nil {
				return err
			}
			resp, err := cl.Users().ListInvites(cmd.Context(), connect.NewRequest(&authv2.Empty{}))
			if err != nil {
				return err
			}
			out := []map[string]any{}
			var b strings.Builder
			for _, i := range resp.Msg.GetInvites() {
				out = append(out, inviteOut(i))
				fmt.Fprintf(&b, "%s %s %s %s\n", theme(env).Title.Render(i.GetId()), roleName(i.GetRole()), time.Unix(i.GetExpiresUnix(), 0).UTC().Format(time.RFC3339), i.GetNote())
			}
			return emit(cmd, env, out, b.String())
		},
	}
	uninvite := idCmd("uninvite <id>", "withdraw an invite", "withdrew", func(cmd *cobra.Command, id string) (*authv2.User, error) {
		_, _, _, cl, err := current(cmd, env)
		if err != nil {
			return nil, err
		}
		_, err = cl.Users().DeleteInvite(cmd.Context(), connect.NewRequest(&authv2.UserRequest{Id: id}))
		return nil, err
	})

	cmd := &cobra.Command{Use: "users", Short: "the cloud's users (admins)"}
	cmd.AddCommand(list, create, remove, role, disable, enable, password, invite, invites, uninvite)
	return cmd
}

func inviteOut(i *authv2.Invite) map[string]any {
	out := map[string]any{"id": i.GetId(), "role": roleName(i.GetRole()), "expires_unix": i.GetExpiresUnix(), "note": i.GetNote()}
	if i.GetCode() != "" {
		out["code"] = i.GetCode()
	}
	return out
}

// newPassword is --password-stdin, or asked for twice at a terminal.
func newPassword(cmd *cobra.Command, env Env) (string, error) {
	if flagBool(cmd, "password-stdin") {
		return readLine(env.Stdin, "password")
	}
	if !prompting(cmd, env) {
		return "", errors.New("--password-stdin is required")
	}
	pw, err := env.Prompter.Secret("Password", "")
	if err != nil {
		return "", err
	}
	again, err := env.Prompter.Secret("Password again", "")
	if err != nil {
		return "", err
	}
	if pw != again {
		return "", errors.New("the passwords do not match")
	}
	return pw, nil
}

func tokenOut(t *authv2.Token) map[string]any {
	out := map[string]any{"id": t.GetId(), "name": t.GetName(), "user": t.GetUser(), "created_unix": t.GetCreatedUnix(), "expires_unix": t.GetExpiresUnix()}
	if t.GetSecret() != "" {
		out["secret"] = t.GetSecret()
	}
	return out
}

// tokensCommand is API tokens: anyone's own, or (admin) a user's.
func tokensCommand(env Env) *cobra.Command {
	list := &cobra.Command{
		Use:   "list",
		Short: "list API tokens",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _, _, cl, err := current(cmd, env)
			if err != nil {
				return err
			}
			resp, err := cl.Users().ListTokens(cmd.Context(), connect.NewRequest(&authv2.ListTokensRequest{User: flagString(cmd, "user")}))
			if err != nil {
				return err
			}
			out := []map[string]any{}
			var b strings.Builder
			for _, t := range resp.Msg.GetTokens() {
				out = append(out, tokenOut(t))
				exp := "never expires"
				if t.GetExpiresUnix() != 0 {
					exp = "expires " + time.Unix(t.GetExpiresUnix(), 0).UTC().Format(time.RFC3339)
				}
				fmt.Fprintf(&b, "%s %s %s\n", theme(env).Title.Render(t.GetId()), t.GetName(), theme(env).Muted.Render(exp))
			}
			return emit(cmd, env, out, b.String())
		},
	}
	list.Flags().String("user", "", "a user's tokens (admin)")

	create := &cobra.Command{
		Use:   "create <name>",
		Short: "make an API token; its secret is shown once",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _, _, cl, err := current(cmd, env)
			if err != nil {
				return err
			}
			resp, err := cl.Users().CreateToken(cmd.Context(), connect.NewRequest(&authv2.CreateTokenRequest{
				Name: args[0], TtlSeconds: int64(flagDuration(cmd, "ttl") / time.Second), User: flagString(cmd, "user"),
			}))
			if err != nil {
				return err
			}
			t := resp.Msg
			return emit(cmd, env, tokenOut(t), theme(env).Done("token "+t.GetId())+"  secret (shown once): "+t.GetSecret()+"\n")
		},
	}
	create.Flags().Duration("ttl", 0, "how long the token lasts (default never expires)")
	create.Flags().String("user", "", "make it for this user (admin; they need the apikey capability)")

	revoke := &cobra.Command{
		Use:   "revoke <id>",
		Short: "revoke an API token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _, _, cl, err := current(cmd, env)
			if err != nil {
				return err
			}
			if _, err := cl.Users().RevokeToken(cmd.Context(), connect.NewRequest(&authv2.RevokeTokenRequest{Id: args[0]})); err != nil {
				return err
			}
			return emit(cmd, env, map[string]string{"id": args[0]}, theme(env).Done("revoked "+args[0]))
		},
	}

	cmd := &cobra.Command{Use: "tokens", Short: "API tokens"}
	cmd.AddCommand(list, create, revoke)
	return cmd
}
