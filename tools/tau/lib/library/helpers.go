package libraryLib

import (
	"github.com/taubyte/tau/pkg/schema/libraries"
	"github.com/taubyte/tau/pkg/schema/project"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	applicationLib "github.com/taubyte/tau/tools/tau/lib/application"
	"github.com/taubyte/utils/id"
)

type getter struct {
	project     project.Project
	application string
	library     libraries.Library
}

func get(name string) (info getter, err error) {
	info.project, info.application, err = applicationLib.SelectedProjectAndApp()
	if err != nil {
		return
	}

	info.library, err = info.project.Library(name, info.application)
	if err != nil {
		return
	}

	return
}

func list() (project project.Project, application string, libraries []string, err error) {
	project, application, err = applicationLib.SelectedProjectAndApp()
	if err != nil {
		return
	}

	local, global := project.Get().Libraries(application)
	if len(application) > 0 {
		libraries = local
	} else {
		libraries = global
	}

	return
}

func set(_library *structureSpec.Library, new bool) error {
	info, err := get(_library.Name)
	if err != nil {
		return err
	}

	if new {
		_library.Id = id.Generate(info.project.Get().Id(), _library.Name)
	}

	err = info.library.SetWithStruct(false, _library)
	if err != nil {
		return err
	}

	return info.library.Set(true,
		libraries.Github(_library.RepoID, _library.RepoName),
	)
}
