package websiteLib

import (
	"github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/pkg/schema/website"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	applicationLib "github.com/taubyte/tau/tools/tau/lib/application"
	"github.com/taubyte/utils/id"
)

type getter struct {
	project     project.Project
	application string
	website     website.Website
}

func get(name string) (info getter, err error) {
	info.project, info.application, err = applicationLib.SelectedProjectAndApp()
	if err != nil {
		return
	}

	info.website, err = info.project.Website(name, info.application)
	if err != nil {
		return
	}

	return
}

func list() (project project.Project, application string, websites []string, err error) {
	project, application, err = applicationLib.SelectedProjectAndApp()
	if err != nil {
		return
	}

	local, global := project.Get().Websites(application)
	if len(application) > 0 {
		websites = local
	} else {
		websites = global
	}

	return
}

func set(_website *structureSpec.Website, new bool) error {
	info, err := get(_website.Name)
	if err != nil {
		return err
	}

	if new {
		_website.Id = id.Generate(info.project.Get().Id(), _website.Name)
	}

	err = info.website.SetWithStruct(false, _website)
	if err != nil {
		return err
	}

	return info.website.Set(true,
		website.Github(_website.RepoID, _website.RepoName),
	)
}
