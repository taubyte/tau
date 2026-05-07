package accounts

import (
	"errors"
	"fmt"

	"github.com/pterm/pterm"
	accountsClient "github.com/taubyte/tau/tools/tau/clients/accounts_client"
	"github.com/urfave/cli/v2"
)

var loginCommand = &cli.Command{
	Name:  "login",
	Usage: "Sign in to your tau Account via email magic-link",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "email",
			Aliases: []string{"e"},
			Usage:   "primary email associated with your tau Account",
		},
		&cli.StringFlag{
			Name:    "account",
			Aliases: []string{"a"},
			Usage:   "Account slug (when your email is on multiple Accounts)",
		},
	},
	Action: runLogin,
}

func runLogin(ctx *cli.Context) error {
	loaded, err := accountsClient.Load()
	if err != nil {
		return err
	}

	email := ctx.String("email")
	if email == "" {
		input, err := pterm.DefaultInteractiveTextInput.Show("Email")
		if err != nil {
			return err
		}
		email = input
	}
	if email == "" {
		return errors.New("email required")
	}

	chal, err := loaded.HTTP.LoginStart(email, ctx.String("account"))
	if err != nil {
		return fmt.Errorf("login start: %w", err)
	}
	if len(chal.Candidates) > 1 {
		pterm.Info.Println("This email is on multiple Accounts:")
		for _, c := range chal.Candidates {
			pterm.Info.Printf("  - %s (%s)\n", c.Slug, c.Name)
		}
		return errors.New("re-run with --account=<slug>")
	}
	if !chal.MagicLinkSent {
		// In v1 the CLI only handles the magic-link path. Passkey login is
		// a browser flow; the CLI flow can be extended once the device-pair
		// handshake is wired.
		return errors.New("this Account requires a passkey; use the web UI to sign in (CLI passkey support is a follow-up)")
	}

	pterm.Success.Println("A sign-in link has been sent to your email.")
	pterm.Info.Println("Click the link, then paste the code from the URL (after `code=`):")

	code, err := pterm.DefaultInteractiveTextInput.WithMask("*").Show("Code")
	if err != nil {
		return err
	}
	if code == "" {
		return errors.New("code required")
	}

	sess, err := loaded.HTTP.FinishMagic(code)
	if err != nil {
		return fmt.Errorf("login finish: %w", err)
	}
	if sess.Token == "" {
		return errors.New("server returned no session token")
	}

	if err := accountsClient.PersistSession(loaded.ProfileName, loaded.Profile, sess.Token); err != nil {
		return fmt.Errorf("persist session: %w", err)
	}
	pterm.Success.Printf("Signed in. Session valid until %s.\n", sess.ExpiresAt.Format("2006-01-02 15:04 MST"))
	return nil
}
