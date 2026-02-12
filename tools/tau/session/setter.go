package session

import "github.com/taubyte/tau/tools/tau/constants"

type setter struct{}

func Set() Setter {
	getOrCreateSession()
	return setter{}
}

func (setter) ProfileName(value string) (err error) {
	debugSession("Set ProfileName=%q", value)
	return setKey(constants.KeyProfile, value)
}

func (setter) SelectedProject(value string) (err error) {
	return setKey(constants.KeyProject, value)
}

func (setter) SelectedApplication(value string) (err error) {
	return setKey(constants.KeyApplication, value)
}

func (setter) SelectedCloud(value string) (err error) {
	debugSession("Set SelectedCloud=%q", value)
	return setKey(constants.KeySelectedCloud, value)
}

func (setter) CustomCloudUrl(value string) (err error) {
	debugSession("Set CustomCloudUrl=%q", value)
	return setKey(constants.KeyCustomCloudURL, value)
}
