package loginLib

import (
	"github.com/pterm/pterm"
	loginI18n "github.com/taubyte/tau/tools/tau/i18n/login"
	"github.com/taubyte/tau/tools/tau/singletons/config"
)

func New(name, provider, token string, setDefault bool) error {
	profiles := config.Profiles()
	_profiles := profiles.List(true)

	// Remove current default:
	for _name, profile := range _profiles {
		if profile.Default {
			profile.Default = false

			err := profiles.Set(_name, profile)
			if err != nil {
				return loginI18n.RemovingDefaultFailed(err)
			}
		}
	}

	gitName, gitEmail, err := extractInfo(token, provider)
	if err != nil {
		pterm.Warning.Println(loginI18n.GitNameOrEmailFailed(err))
	}

	return profiles.Set(name, config.Profile{
		Provider:    provider,
		Token:       token,
		Default:     setDefault,
		GitUsername: gitName,
		GitEmail:    gitEmail,
	})
}
