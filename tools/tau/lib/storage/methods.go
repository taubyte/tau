package storageLib

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

func New(storage *structureSpec.Storage) error {
	return set(storage, true)
}

func Set(storage *structureSpec.Storage) error {
	return set(storage, false)
}

func Delete(name string) error {
	info, err := get(name)
	if err != nil {
		return err
	}

	return info.storage.Delete()
}

func List() ([]string, error) {
	_, _, storages, err := list()
	if err != nil {
		return nil, err
	}

	return storages, nil
}

func ListResources() ([]*structureSpec.Storage, error) {
	project, application, relative, err := list()
	if err != nil {
		return nil, err
	}

	storages := make([]*structureSpec.Storage, len(relative))
	for idx, name := range relative {
		storage, err := project.Storage(name, application)
		if err != nil {
			return nil, err
		}

		storages[idx], err = storage.Get().Struct()
		if err != nil {
			return nil, err
		}

		// TODO do this in prompts
		storages[idx].Type = cases.Title(language.English).String(storages[idx].Type)
	}

	return storages, nil
}
