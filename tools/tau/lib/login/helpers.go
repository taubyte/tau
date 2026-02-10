package loginLib

import (
	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/i18n"
)

func GetSelectedProfile() (profile config.Profile, err error) {
	defer func() {
		if err != nil {
			i18n.Help().HaveYouLoggedIn()
		}
	}()

	currentProfile, err := config.GetSelectedUser()
	if err != nil {
		return
	}

	profile, err = config.Profiles().Get(currentProfile)
	if err != nil {
		return
	}

	return
}
