package session

import "github.com/taubyte/tau/tools/tau/constants"

type unSetter struct{}

func Unset() UnSetter {
	getOrCreateSession()

	return unSetter{}
}

func (unSetter) ProfileName() (err error) {
	return deleteKey(constants.KeyProfile)
}

func (unSetter) SelectedProject() (err error) {
	return deleteKey(constants.KeyProject)
}

func (unSetter) SelectedApplication() (err error) {
	return deleteKey(constants.KeyApplication)
}

func (unSetter) SelectedCloud() (err error) {
	return deleteKey(constants.KeySelectedCloud)
}

func (unSetter) CustomCloudUrl() (err error) {
	return deleteKey(constants.KeyCustomCloudURL)
}

func (unSetter) AuthURL() (err error) {
	return deleteKey(constants.KeyAuthURL)
}

func (unSetter) DreamAPIURL() (err error) {
	return deleteKey(constants.KeyDreamAPIURL)
}
