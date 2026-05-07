// Package accountsClient is the tau-cli-side wrapper around clients/http/accounts.
//
// Resolves the accounts-service URL from the active Profile (RemoteCloud →
// `https://accounts.tau.<network>`; DreamCloud → local dream port; TestCloud
// → env override or compiled-in default), constructs the HTTP client with the
// Profile's persisted session bearer (when present), and surfaces the result
// to the CLI commands under tools/tau/cli/commands/accounts/.
package accountsClient

import (
	"context"
	"fmt"
	"os"
	"strings"

	httpaccounts "github.com/taubyte/tau/clients/http/accounts"
	"github.com/taubyte/tau/tools/tau/clients/dream"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/i18n"
	cloudI18n "github.com/taubyte/tau/tools/tau/i18n/cloud"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/shared"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/taubyte/tau/tools/tau/session"
)

// LoadedClient pairs the Profile (so callers can write the session back) with
// a ready-to-use HTTP client.
type LoadedClient struct {
	ProfileName string
	Profile     config.Profile
	HTTP        *httpaccounts.Client
	URL         string
}

// resolveURL returns the accounts-service base URL for the given Profile.
func resolveURL(profile config.Profile) (string, error) {
	switch profile.CloudType {
	case common.DreamCloud:
		port, err := dream.HTTPPort(context.Background(), "accounts")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("http://localhost:%d", port), nil
	case common.RemoteCloud:
		if profile.Cloud == "" {
			return "", fmt.Errorf("no network configured on profile")
		}
		return fmt.Sprintf("https://accounts.tau.%s", profile.Cloud), nil
	case common.TestCloud:
		if u := os.Getenv("TAUBYTE_ACCOUNTS_URL"); u != "" {
			return u, nil
		}
		return "", cloudI18n.ErrorUnknownCloud(profile.CloudType)
	default:
		return "", cloudI18n.ErrorUnknownCloud(profile.CloudType)
	}
}

// Load picks the active profile, builds the URL, and constructs an HTTP
// client. Pre-attaches the session bearer when one is persisted; commands
// like `tau accounts login` overwrite it post-handshake.
func Load() (*LoadedClient, error) {
	profileName, exist := session.Get().ProfileName()
	if !exist {
		profileName, _, _ = loginLib.GetProfiles()
		if profileName == "" {
			i18n.Help().HaveYouLoggedIn()
			return nil, singletonsI18n.ProfileDoesNotExist()
		}
	}
	profile, err := config.Profiles().Get(profileName)
	if err != nil {
		return nil, err
	}
	selectedCloud, _ := session.GetSelectedCloud()
	if selectedCloud == "" {
		i18n.Help().HaveYouSelectedACloud()
		return nil, singletonsI18n.NoCloudSelected()
	}
	url, err := resolveURL(profile)
	if err != nil {
		return nil, err
	}
	opts := []httpaccounts.Option{httpaccounts.WithURL(url)}
	if strings.HasPrefix(url, "http://") {
		// Test / dream URLs are http; skip TLS verification for them.
		opts = append(opts, httpaccounts.WithUnsecure())
	}
	if profile.AccountsSession != "" {
		opts = append(opts, httpaccounts.WithSession(profile.AccountsSession))
	}
	client, err := httpaccounts.New(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("creating accounts client failed: %w", err)
	}
	return &LoadedClient{
		ProfileName: profileName,
		Profile:     profile,
		HTTP:        client,
		URL:         url,
	}, nil
}

// PersistSession writes the session bearer back to the active profile so
// subsequent `tau accounts ...` invocations can reuse it.
func PersistSession(profileName string, profile config.Profile, token string) error {
	profile.AccountsSession = token
	return config.Profiles().Set(profileName, profile)
}
