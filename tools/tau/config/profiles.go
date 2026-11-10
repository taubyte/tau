package config

import (
	seer "github.com/taubyte/tau/pkg/yaseer"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/shared"
)

func Profiles() *profileHandler {
	getOrCreateConfig()

	return &profileHandler{}
}

func (p *profileHandler) Root() *seer.Query {
	return _config.Document().Get(singletonsI18n.ProfilesKey)
}

func (p *profileHandler) Get(name string) (profile Profile, err error) {
	err = p.Root().Get(name).Value(&profile)
	if err != nil {
		err = singletonsI18n.GettingProfileFailedWith(name, err)
		return
	}

	profile.name = name
	return profile, nil
}

func (p *profileHandler) Set(name string, profile Profile) error {
	err := p.Root().Get(name).Set(profile).Commit()
	if err != nil {
		return singletonsI18n.SettingProfileFailedWith(name, err)
	}

	return _config.root.Sync()
}

func (p *profileHandler) List(loginCommand bool) (profiles map[string]Profile) {
	// An error leaves the map empty; the caller's error then says to log in.
	_ = p.Root().Value(&profiles)
	return profiles
}
