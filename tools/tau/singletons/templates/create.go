package templates

import "github.com/taubyte/tau/pkg/git"

var _templates *templates

func getOrCreateTemplates() *templates {
	if _templates == nil {
		err := loadTemplates()
		if err != nil {
			panic(err)
		}
	}

	return _templates
}

func (t *templates) Repository() *git.Repository {
	return t.repository
}
