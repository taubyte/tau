package loginLib

import (
	"github.com/taubyte/tau/tools/tau/env"
	loginI18n "github.com/taubyte/tau/tools/tau/i18n/login"
	"github.com/taubyte/tau/tools/tau/singletons/config"
	"github.com/urfave/cli/v2"
)

/*
if setDefault is true it will remove current default and set the
newly selected profile as the default
*/
func Select(ctx *cli.Context, name string, setDefault bool) error {
	if setDefault {
		configProfiles := config.Profiles()
		profiles := configProfiles.List(true)
		for profileName, profile := range profiles {
			if profileName == name {
				profile.Default = true
				err := configProfiles.Set(profileName, profile)
				if err != nil {
					return loginI18n.SettingDefaultFailed(err)
				}
				continue
			}

			if profile.Default {
				profile.Default = false

				err := configProfiles.Set(profileName, profile)
				if err != nil {
					return loginI18n.RemovingDefaultFailed(err)
				}
			}
		}
	}

	profile, err := config.Profiles().Get(name)
	if err != nil {
		return err
	}

	env.SetSelectedNetwork(ctx, profile.NetworkType)
	env.SetNetworkUrl(ctx, profile.Network)
	return env.SetSelectedUser(ctx, name)
}
