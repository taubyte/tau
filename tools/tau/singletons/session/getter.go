package session

import "github.com/taubyte/tau/tools/tau/constants"

type getter struct{}

func Get() getter {
	getOrCreateSession()

	return getter{}
}

func (getter) ProfileName() (value string, exist bool) {
	return getKey[string](constants.CurrentSelectedProfileNameEnvVarName)
}

func (getter) SelectedProject() (value string, exist bool) {
	return getKey[string](constants.CurrentProjectEnvVarName)
}

func (getter) SelectedApplication() (value string, exist bool) {
	return getKey[string](constants.CurrentApplicationEnvVarName)
}

func (getter) SelectedNetwork() (value string, exist bool) {
	return getKey[string](constants.CurrentSelectedNetworkName)
}

func (getter) CustomNetworkUrl() (value string, exist bool) {
	return getKey[string](constants.CustomNetworkUrlName)
}
