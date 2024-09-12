package databaseLib

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func New(database *structureSpec.Database) error {
	return set(database, true)
}

func Set(database *structureSpec.Database) error {
	return set(database, false)
}

func Delete(name string) error {
	info, err := get(name)
	if err != nil {
		return err
	}

	return info.database.Delete()
}

func List() ([]string, error) {
	_, _, databases, err := list()
	if err != nil {
		return nil, err
	}

	return databases, nil
}

func ListResources() ([]*structureSpec.Database, error) {
	project, application, relative, err := list()
	if err != nil {
		return nil, err
	}

	databases := make([]*structureSpec.Database, len(relative))
	for idx, name := range relative {
		database, err := project.Database(name, application)
		if err != nil {
			return nil, err
		}

		databases[idx], err = database.Get().Struct()
		if err != nil {
			return nil, err
		}
	}

	return databases, nil
}
