package session

import "github.com/taubyte/tau/tools/tau/constants"

type unSetter struct{}

func Unset() UnSetter {
	getOrCreateSession()

	return unSetter{}
}

func (unSetter) ProfileName() (err error) {
	return deleteKey(constants.CurrentSelectedProfileNameEnvVarName)
}

func (unSetter) SelectedProject() (err error) {
	return deleteKey(constants.CurrentProjectEnvVarName)
}

func (unSetter) SelectedApplication() (err error) {
	return deleteKey(constants.CurrentApplicationEnvVarName)
}

func (unSetter) CustomNetworkUrl() (err error) {
	return deleteKey(constants.CustomNetworkUrlName)
}
