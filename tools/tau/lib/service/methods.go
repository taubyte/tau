package serviceLib

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func New(service *structureSpec.Service) error {
	return set(service, true)
}

func Set(service *structureSpec.Service) error {
	return set(service, false)
}

func Delete(name string) error {
	info, err := get(name)
	if err != nil {
		return err
	}

	return info.service.Delete()
}

func List() ([]string, error) {
	_, _, services, err := list()
	if err != nil {
		return nil, err
	}

	return services, nil
}

func ListResources() ([]*structureSpec.Service, error) {
	project, application, relative, err := list()
	if err != nil {
		return nil, err
	}

	services := make([]*structureSpec.Service, len(relative))
	for idx, name := range relative {
		service, err := project.Service(name, application)
		if err != nil {
			return nil, err
		}

		services[idx], err = service.Get().Struct()
		if err != nil {
			return nil, err
		}
	}

	return services, nil
}
