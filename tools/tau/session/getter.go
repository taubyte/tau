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
	value, exist = getKey[string](constants.KeyProfile)
	debugSession("Get ProfileName => value=%q exist=%v", value, exist)
	return value, exist
}

func (getter) SelectedProject() (value string, exist bool) {
	value, exist = getKey[string](constants.KeyProject)
	debugSession("Get SelectedProject => value=%q exist=%v", value, exist)
	return value, exist
}

func (getter) SelectedApplication() (value string, exist bool) {
	value, exist = getKey[string](constants.KeyApplication)
	debugSession("Get SelectedApplication => value=%q exist=%v", value, exist)
	return value, exist
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

// GetSelectedCloud returns the selected cloud type from the session ("remote" | "dream").
func GetSelectedCloud() (string, bool) {
	return Get().SelectedCloud()
}

// GetCustomCloudUrl returns the cloud value: FQDN when type=remote, universe name when type=dream.
func GetCustomCloudUrl() (string, bool) {
	return Get().CustomCloudUrl()
}
