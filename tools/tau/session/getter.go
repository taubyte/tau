package session

import (
	"github.com/taubyte/tau/tools/tau/constants"
)

type getter struct{}

func Get() getter {
	getOrCreateSession()

	return getter{}
}

func (getter) ProfileName() (value string, exist bool) {
	return getKey[string](constants.KeyProfile)
}

func (getter) SelectedProject() (value string, exist bool) {
	return getKey[string](constants.KeyProject)
}

func (getter) SelectedApplication() (value string, exist bool) {
	return getKey[string](constants.KeyApplication)
}

func (getter) SelectedCloud() (value string, exist bool) {
	value, exist = getKey[string](constants.KeySelectedCloud)
	debugSession("Get SelectedCloud => value=%q exist=%v", value, exist)
	return value, exist
}

func (getter) CustomCloudUrl() (value string, exist bool) {
	value, exist = getKey[string](constants.KeyCustomCloudURL)
	debugSession("Get CustomCloudUrl => value=%q exist=%v", value, exist)
	return value, exist
}

func (getter) AuthURL() (value string, exist bool) {
	return getKey[string](constants.KeyAuthURL)
}

func (getter) DreamAPIURL() (value string, exist bool) {
	return getKey[string](constants.KeyDreamAPIURL)
}

// GetSelectedCloud returns the selected cloud from the session.
func GetSelectedCloud() (string, bool) {
	return Get().SelectedCloud()
}

// GetCustomCloudUrl returns the custom cloud URL from the session.
func GetCustomCloudUrl() (string, bool) {
	return Get().CustomCloudUrl()
}
