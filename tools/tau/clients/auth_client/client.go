package authClient

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/taubyte/tau/clients/http"
	client "github.com/taubyte/tau/clients/http/auth"
	"github.com/taubyte/tau/tools/tau/clients/dream"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/constants"
	"github.com/taubyte/tau/tools/tau/i18n"
	cloudI18n "github.com/taubyte/tau/tools/tau/i18n/cloud"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/shared"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/taubyte/tau/tools/tau/session"
)

func getClientUrl() (url string, err error) {
	profile, err := loginLib.GetSelectedProfile()
	if err != nil {
		return "", err
	}

	switch profile.CloudType {
	case common.DreamCloud:
		port, err := dream.HTTPPort(context.TODO(), "auth")
		if err != nil {
			return "", err
		}
		url = fmt.Sprintf("http://localhost:%d", port)
	case common.RemoteCloud:
		url = fmt.Sprintf("https://auth.tau.%s", profile.Cloud)
	case common.TestCloud:
		if u := os.Getenv("TAUBYTE_AUTH_URL"); u != "" {
			url = u
		} else {
			url = constants.ClientURL
		}
	default:
		err = cloudI18n.ErrorUnknownCloud(profile.CloudType)
	}

	return
}

func loadClient() (config.Profile, *client.Client, error) {
	profileName, exist := session.Get().ProfileName()
	if !exist {
		// Check for a default if no profiles are selected
		profileName, _, _ = loginLib.GetProfiles()
		if len(profileName) == 0 {
			i18n.Help().HaveYouLoggedIn()
			return config.Profile{}, nil, singletonsI18n.ProfileDoesNotExist()
		}
	}

	profile, err := config.Profiles().Get(profileName)
	if err != nil {
		return config.Profile{}, nil, err
	}

	selectedCloud, _ := session.GetSelectedCloud()
	if selectedCloud == "" {
		i18n.Help().HaveYouSelectedACloud()
		return config.Profile{}, nil, singletonsI18n.NoCloudSelected()
	}

	url, err := getClientUrl()
	if err != nil {
		return config.Profile{}, nil, err
	}

	ops := []http.Option{http.URL(url), http.Auth(profile.Token)}
	if strings.HasPrefix(url, "http://") {
		ops = append(ops, http.UseDefaultTransport())
	}
	// Use Background: the HTTP client stores this context for all requests (see clients/http/methods.go do()).
	// Canceling it when loadClient returns would cause "context canceled" on every subsequent request.
	client, err := client.New(context.Background(), ops...)
	if err != nil {
		return profile, nil, singletonsI18n.CreatingAuthClientFailed(err)
	}

	return profile, client, nil
}
