package databaseLib

import (
	"github.com/taubyte/tau/pkg/schema/databases"
	"github.com/taubyte/tau/pkg/schema/project"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	applicationLib "github.com/taubyte/tau/tools/tau/lib/application"
	"github.com/taubyte/utils/id"
)

type getter struct {
	project     project.Project
	application string
	database    databases.Database
}

func get(name string) (info getter, err error) {
	info.project, info.application, err = applicationLib.SelectedProjectAndApp()
	if err != nil {
		return
	}

	info.database, err = info.project.Database(name, info.application)
	if err != nil {
		return
	}

	return
}

func list() (project project.Project, application string, databases []string, err error) {
	project, application, err = applicationLib.SelectedProjectAndApp()
	if err != nil {
		return
	}

	local, global := project.Get().Databases(application)
	if len(application) > 0 {
		databases = local
	} else {
		databases = global
	}

	return
}

func set(database *structureSpec.Database, new bool) error {
	info, err := get(database.Name)
	if err != nil {
		return err
	}

	if new {
		database.Id = id.Generate(info.project.Get().Id(), database.Name)
	}

	oldKey, _ := info.database.Get().Encryption()
	if len(oldKey) > 0 && len(database.Key) == 0 {
		err = info.database.Delete("encryption")
		if err != nil {
			return err
		}
	}

	return info.database.SetWithStruct(true, database)
}
