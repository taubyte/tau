package loginLib

import (
	"github.com/taubyte/tau/tools/tau/config"
)

func GetSelectedProfile() (profile config.Profile, err error) {
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
