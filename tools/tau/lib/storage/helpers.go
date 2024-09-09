package storageLib

import (
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/pkg/schema/storages"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	applicationLib "github.com/taubyte/tau/tools/tau/lib/application"
	"github.com/taubyte/utils/id"
)

type getter struct {
	project     project.Project
	application string
	storage     storages.Storage
}

func get(name string) (info getter, err error) {
	info.project, info.application, err = applicationLib.SelectedProjectAndApp()
	if err != nil {
		return
	}

	info.storage, err = info.project.Storage(name, info.application)
	if err != nil {
		return
	}

	return
}

func list() (project project.Project, application string, storages []string, err error) {
	project, application, err = applicationLib.SelectedProjectAndApp()
	if err != nil {
		return
	}

	local, global := project.Get().Storages(application)
	if len(application) > 0 {
		storages = local
	} else {
		storages = global
	}

	return
}

func set(storage *structureSpec.Storage, new bool) error {
	info, err := get(storage.Name)
	if err != nil {
		return err
	}

	storage.Type = strings.ToLower(storage.Type)

	if new {
		storage.Id = id.Generate(info.project.Get().Id(), storage.Name)
	} else if info.storage.Get().Type() != storage.Type {
		err = info.storage.Delete(info.storage.Get().Type())
		if err != nil {
			return err
		}
	}

	return info.storage.SetWithStruct(true, storage)
}

func ShortDur(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}
