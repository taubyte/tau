package templates

import (
	"github.com/taubyte/tau/pkg/git"
)

// Get is the template repository, loaded on first use.
//
// IT ANSWERS WHAT getOrCreateTemplates ANSWERED, and not the package variable:
// a load that failed deliberately leaves that variable nil so the next call
// retries, so reading it back handed every caller nothing at all.
func Get() *templates {
	return getOrCreateTemplates()
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
