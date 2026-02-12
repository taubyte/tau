package session

import seer "github.com/taubyte/tau/pkg/yaseer"

type tauSession struct {
	root *seer.Seer
}

type Setter interface {
	ProfileName(value string) (err error)
	SelectedProject(value string) (err error)
	SelectedApplication(value string) (err error)
	SelectedCloud(value string) (err error)
	CustomCloudUrl(value string) (err error) // FQDN when remote, universe name when dream
}

type Getter interface {
	ProfileName() (value string, exist bool)
	SelectedProject() (value string, exist bool)
	SelectedApplication() (value string, exist bool)
	SelectedCloud() (value string, exist bool)
	CustomCloudUrl() (value string, exist bool)
}

type UnSetter interface {
	ProfileName() (err error)
	SelectedProject() (err error)
	SelectedApplication() (err error)
	SelectedCloud() (err error)
	CustomCloudUrl() (err error)
}
