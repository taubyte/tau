package templates

import "github.com/taubyte/tau/pkg/git"

func Get() *templates {
	getOrCreateTemplates()

	return _templates
}

func Repository() *git.Repository {
	return Get().repository
}

type templateYaml struct {
	// parameters must be exported for the yaml parser
	Name        string
	Description string
	Icon        string
	URL         string
}
