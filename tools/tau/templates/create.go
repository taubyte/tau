package templates

import "github.com/taubyte/tau/pkg/git"

var _templates *templates

// getOrCreateTemplates loads the template repository once.
//
// IT DOES NOT PANIC. Loading means pulling a repository over the network, and
// an EOF from GitHub is an ordinary Tuesday — taking the process down for it
// means a developer's `tau` dies with a stack trace where it should have said
// it could not reach the templates. Every caller asks through a method that
// already returns an error, so the error goes there.
//
// AND A FAILED LOAD IS NOT REMEMBERED. Caching it would turn one bad moment
// into a process that can never see the templates again.
func getOrCreateTemplates() *templates {
	if _templates != nil {
		return _templates
	}

	if err := loadTemplates(); err != nil {
		_templates = nil
		return &templates{loadErr: err}
	}

	return _templates
}

func (t *templates) Repository() *git.Repository {
	return t.repository
}
