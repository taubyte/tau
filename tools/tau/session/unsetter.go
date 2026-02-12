package session

import "github.com/taubyte/tau/tools/tau/constants"

type unSetter struct{}

func Unset() UnSetter {
	getOrCreateSession()

	return unSetter{}
}

func (unSetter) ProfileName() (err error) {
	debugSession("Unset ProfileName")
	return deleteKey(constants.KeyProfile)
}

func (unSetter) SelectedProject() (err error) {
	debugSession("Unset SelectedProject")
	return deleteKey(constants.KeyProject)
}

func (unSetter) SelectedApplication() (err error) {
	debugSession("Unset SelectedApplication")
	return deleteKey(constants.KeyApplication)
}

func (unSetter) SelectedCloud() (err error) {
	debugSession("Unset SelectedCloud")
	return deleteKey(constants.KeySelectedCloud)
}

func (unSetter) CustomCloudUrl() (err error) {
	debugSession("Unset CustomCloudUrl")
	return deleteKey(constants.KeyCustomCloudURL)
}
