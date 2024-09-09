package libraryLib

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func New(library *structureSpec.Library) error {
	return set(library, true)
}

func Set(library *structureSpec.Library) error {
	return set(library, false)
}

func Delete(name string) error {
	info, err := get(name)
	if err != nil {
		return err
	}

	return info.library.Delete()
}

func List() ([]string, error) {
	_, _, libraries, err := list()
	if err != nil {
		return nil, err
	}

	return libraries, nil
}

func ListResources() ([]*structureSpec.Library, error) {
	project, application, relative, err := list()
	if err != nil {
		return nil, err
	}

	libraries := make([]*structureSpec.Library, len(relative))
	for idx, name := range relative {
		library, err := project.Library(name, application)
		if err != nil {
			return nil, err
		}

		libraries[idx], err = library.Get().Struct()
		if err != nil {
			return nil, err
		}
	}

	return libraries, nil
}
