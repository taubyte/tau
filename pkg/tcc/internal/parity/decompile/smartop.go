package decompile

import (
	"fmt"

	projectLib "github.com/taubyte/tau/pkg/schema/project"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/utils/mapstructure"
)

func smartop(project projectLib.Project, _id string, obj interface{}, appName string) error {
	resource := &structureSpec.SmartOp{}
	mapstructure.Decode(obj, resource)

	iFace, err := project.SmartOps(resource.Name, appName)
	if err != nil {
		return fmt.Errorf("open smart-op `%s/%s` failed: %s", appName, resource.Name, err)
	}

	resource.SetId(_id)
	return iFace.SetWithStruct(false, resource)
}
