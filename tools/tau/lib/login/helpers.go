package loginLib

import (
	"github.com/taubyte/tau/tools/tau/env"
	"github.com/taubyte/tau/tools/tau/i18n"
	"github.com/taubyte/tau/tools/tau/singletons/config"
)

func GetSelectedProfile() (profile config.Profile, err error) {
	defer func() {
		if err != nil {
			i18n.Help().HaveYouLoggedIn()
		}
	}()

	currentProfile, err := env.GetSelectedUser()
	if err != nil {
		return
	}

	profile, err = config.Profiles().Get(currentProfile)
	if err != nil {
		return
	}

	return
}
