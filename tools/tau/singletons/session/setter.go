package session

import "github.com/taubyte/tau/tools/tau/constants"

type setter struct{}

func Set() Setter {
	getOrCreateSession()

	return setter{}
}

func (setter) ProfileName(value string) (err error) {
	return setKey(constants.CurrentSelectedProfileNameEnvVarName, value)
}

func (setter) SelectedProject(value string) (err error) {
	return setKey(constants.CurrentProjectEnvVarName, value)
}

func (setter) SelectedApplication(value string) (err error) {
	return setKey(constants.CurrentApplicationEnvVarName, value)
}

func (setter) SelectedNetwork(value string) (err error) {
	return setKey(constants.CurrentSelectedNetworkName, value)
}

func (setter) CustomNetworkUrl(value string) (err error) {
	return setKey(constants.CustomNetworkUrlName, value)
}
