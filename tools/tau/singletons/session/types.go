package session

import "github.com/taubyte/go-seer"

type tauSession struct {
	root *seer.Seer
}

type Setter interface {
	ProfileName(value string) (err error)
	SelectedProject(value string) (err error)
	SelectedApplication(value string) (err error)
	SelectedNetwork(value string) (err error)
	CustomNetworkUrl(value string) (err error)
}

type Getter interface {
	ProfileName() (value string, exist bool)
	SelectedProject() (value string, exist bool)
	SelectedApplication() (value string, exist bool)
	SelectedNetwork() (value string, exist bool)
	CustomNetworkUrl() (value string, exist bool)
}

type UnSetter interface {
	ProfileName() (err error)
	SelectedProject() (err error)
	SelectedApplication() (err error)
	CustomNetworkUrl() (err error)
}
