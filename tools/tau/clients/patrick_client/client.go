package patrickClient

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/clients/http"
	client "github.com/taubyte/tau/clients/http/patrick"
	"github.com/taubyte/tau/tools/tau/clients/dream"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/config"
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
		port, err := dream.HTTPPort(context.TODO(), "patrick")
		if err != nil {
			return "", err
		}

		url = fmt.Sprintf("http://127.0.0.1:%d", port)
	case common.RemoteCloud:
		url = fmt.Sprintf("https://patrick.tau.%s", profile.Cloud)
	default:
		err = cloudI18n.ErrorUnknownCloud(profile.CloudType)
	}

	return
}

func loadClient() (config.Profile, Client, error) {
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
	// Use Background: the HTTP client stores this context for all requests. Canceling it when loadClient returns would cause "context canceled" on every subsequent request.
	c, err := client.New(context.Background(), ops...)
	if err != nil {
		return profile, nil, singletonsI18n.CreatingPatrickClientFailed(err)
	}

	return profile, c, nil
}
