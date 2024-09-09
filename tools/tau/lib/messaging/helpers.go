package messagingLib

import (
	"github.com/taubyte/tau/pkg/schema/messaging"
	"github.com/taubyte/tau/pkg/schema/project"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	applicationLib "github.com/taubyte/tau/tools/tau/lib/application"
	"github.com/taubyte/utils/id"
)

type getter struct {
	project     project.Project
	application string
	messaging   messaging.Messaging
}

func get(name string) (info getter, err error) {
	info.project, info.application, err = applicationLib.SelectedProjectAndApp()
	if err != nil {
		return
	}

	info.messaging, err = info.project.Messaging(name, info.application)
	if err != nil {
		return
	}

	return
}

func list() (project project.Project, application string, channels []string, err error) {
	project, application, err = applicationLib.SelectedProjectAndApp()
	if err != nil {
		return
	}

	local, global := project.Get().Messaging(application)
	if len(application) > 0 {
		channels = local
	} else {
		channels = global
	}

	return
}

func set(messaging *structureSpec.Messaging, new bool) error {
	info, err := get(messaging.Name)
	if err != nil {
		return err
	}

	if new {
		messaging.Id = id.Generate(info.project.Get().Id(), messaging.Name)
	}

	return info.messaging.SetWithStruct(true, messaging)
}
