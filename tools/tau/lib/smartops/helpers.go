package smartopsLib

import (
	"github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/pkg/schema/smartops"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	applicationLib "github.com/taubyte/tau/tools/tau/lib/application"
	"github.com/taubyte/utils/id"
)

type getter struct {
	project     project.Project
	application string
	smartops    smartops.SmartOps
}

func get(name string) (info getter, err error) {
	info.project, info.application, err = applicationLib.SelectedProjectAndApp()
	if err != nil {
		return
	}

	info.smartops, err = info.project.SmartOps(name, info.application)
	if err != nil {
		return
	}

	return
}

func list() (project project.Project, application string, smartops []string, err error) {
	project, application, err = applicationLib.SelectedProjectAndApp()
	if err != nil {
		return
	}

	local, global := project.Get().SmartOps(application)
	if len(application) > 0 {
		smartops = local
	} else {
		smartops = global
	}

	return
}

func set(smartops *structureSpec.SmartOp, new bool) (info getter, err error) {
	info, err = get(smartops.Name)
	if err != nil {
		return
	}

	if new {
		smartops.Id = id.Generate(info.project.Get().Id(), smartops.Name)
	}

	err = info.smartops.SetWithStruct(true, smartops)
	if err != nil {
		return
	}

	return
}
