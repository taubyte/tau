package templates

import git "github.com/taubyte/go-simple-git"

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
