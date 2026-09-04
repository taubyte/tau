package build

import (
	"context"
	"fmt"

	tauBuild "github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/tools/tau/clients/dream"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/i18n"
	cloudI18n "github.com/taubyte/tau/tools/tau/i18n/cloud"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/shared"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/taubyte/tau/tools/tau/session"
)

// Load builds a client for the cloud the session has selected.
func Load() (Client, error) {
	profileName, exist := session.Get().ProfileName()
	if !exist {
		// No profile selected: fall back to a default if there is one.
		profileName, _, _ = loginLib.GetProfiles()
		if len(profileName) == 0 {
			i18n.Help().HaveYouLoggedIn()
			return nil, singletonsI18n.ProfileDoesNotExist()
		}
	}

	profile, err := config.Profiles().Get(profileName)
	if err != nil {
		return nil, err
	}

	if selected, _ := session.GetSelectedCloud(); selected == "" {
		i18n.Help().HaveYouSelectedACloud()
		return nil, singletonsI18n.NoCloudSelected()
	}

	base, err := address(profile)
	if err != nil {
		return nil, err
	}
	return New(base, profile.Token)
}

// address is where this cloud's build frontend answers.
//
// THE PUBLIC ROLE'S NAME IS THE BUILD SERVICE'S OWN, not a string spelled again
// here: a rename would otherwise leave the CLI looking for a host nothing
// serves, and the failure would read as "the cloud is down".
func address(profile config.Profile) (string, error) {
	switch profile.CloudType {
	case common.DreamCloud:
		port, err := dream.HTTPPort(context.TODO(), tauBuild.Frontend.Name)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("http://127.0.0.1:%d", port), nil
	case common.RemoteCloud:
		return fmt.Sprintf("https://%s.tau.%s", tauBuild.Frontend.Name, profile.Cloud), nil
	default:
		return "", cloudI18n.ErrorUnknownCloud(profile.CloudType)
	}
}
