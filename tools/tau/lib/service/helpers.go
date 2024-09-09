package serviceLib

import (
	"github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/pkg/schema/services"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	applicationLib "github.com/taubyte/tau/tools/tau/lib/application"
	"github.com/taubyte/utils/id"
)

type getter struct {
	project     project.Project
	application string
	service     services.Service
}

func get(name string) (info getter, err error) {
	info.project, info.application, err = applicationLib.SelectedProjectAndApp()
	if err != nil {
		return
	}

	info.service, err = info.project.Service(name, info.application)
	if err != nil {
		return
	}

	return
}

func list() (project project.Project, application string, services []string, err error) {
	project, application, err = applicationLib.SelectedProjectAndApp()
	if err != nil {
		return
	}

	local, global := project.Get().Services(application)
	if len(application) > 0 {
		services = local
	} else {
		services = global
	}

	return
}

func set(service *structureSpec.Service, new bool) error {
	info, err := get(service.Name)
	if err != nil {
		return err
	}

	if new {
		service.Id = id.Generate(info.project.Get().Id(), service.Name)
	}

	return info.service.SetWithStruct(true, service)
}
