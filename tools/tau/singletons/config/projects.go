package config

import (
	"github.com/taubyte/go-seer"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/singletons"
)

func Projects() *projectHandler {
	getOrCreateConfig()

	return &projectHandler{}
}

func (p *projectHandler) Root() *seer.Query {
	return _config.Document().Get(singletonsI18n.ProjectsKey).Fork()
}

func (p *projectHandler) Get(name string) (project Project, err error) {
	err = p.Root().Get(name).Value(&project)
	if err != nil {
		err = singletonsI18n.GettingProjectFailedWith(name, err)
		return
	}

	return project, nil
}

func (p *projectHandler) Set(name string, project Project) error {
	err := p.Root().Get(name).Set(project).Commit()
	if err != nil {
		return singletonsI18n.SettingProjectFailedWith(name, err)
	}

	return _config.root.Sync()
}

func (p *projectHandler) Delete(name string) error {
	err := p.Root().Get(name).Delete().Commit()
	if err != nil {
		return singletonsI18n.DeletingProjectFailedWith(name, err)
	}

	return _config.root.Sync()
}

func (p *projectHandler) List() (projects map[string]Project) {
	// Ignoring error here as it will just return an empty map
	p.Root().Value(&projects)

	return projects
}
