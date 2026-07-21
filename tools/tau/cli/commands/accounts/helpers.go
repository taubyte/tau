package accounts

import (
	"errors"
	"fmt"

	httpaccounts "github.com/taubyte/tau/clients/http/accounts"
	accountsClient "github.com/taubyte/tau/tools/tau/clients/accounts_client"
)

type loadedClient struct {
	HTTP *httpaccounts.Client
	Me   *httpaccounts.MeResponse
}

func requireLoggedIn() (*loadedClient, error) {
	loaded, err := accountsClient.Load()
	if err != nil {
		return nil, err
	}
	if loaded.Profile.AccountsSession == "" {
		return nil, errors.New("no active session — run `tau accounts login` first")
	}
	me, err := loaded.HTTP.Me()
	if err != nil {
		return nil, err
	}
	return &loadedClient{HTTP: loaded.HTTP, Me: me}, nil
}

// resolveAccountID maps a user-facing slug to the wire's account ID. Returns
// "not found" when the slug isn't in the Member's linked Accounts — phrased
// to make the access-control aspect clear rather than implying the Account
// doesn't exist on the server.
func (l *loadedClient) resolveAccountID(slug string) (string, error) {
	if slug == "" {
		return "", errors.New("account slug required (positional arg)")
	}
	for _, acc := range l.Me.Accounts {
		if acc.Slug == slug {
			return acc.ID, nil
		}
	}
	return "", fmt.Errorf("Account %q not found among the Member's linked Accounts (run `tau accounts list` to see what you have access to)", slug)
}

// SessionForAccount logs the caller in and maps a slug to its account ID,
// returning the live HTTP client so a subcommand added through a build seam can
// issue account-scoped calls without re-implementing the session/lookup dance.
func SessionForAccount(slug string) (*httpaccounts.Client, string, error) {
	loaded, err := requireLoggedIn()
	if err != nil {
		return nil, "", err
	}
	id, err := loaded.resolveAccountID(slug)
	if err != nil {
		return nil, "", err
	}
	return loaded.HTTP, id, nil
}
