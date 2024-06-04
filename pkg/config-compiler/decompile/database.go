package decompile

import (
	"fmt"

	"github.com/mitchellh/mapstructure"
	projectLib "github.com/taubyte/tau/pkg/schema/project"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func database(project projectLib.Project, _id string, obj interface{}, appName string) error {
	resource := &structureSpec.Database{}
	mapstructure.Decode(obj, resource)

	iFace, err := project.Database(resource.Name, appName)
	if err != nil {
		return fmt.Errorf("open database `%s/%s` failed: %s", appName, resource.Name, err)
	}

	resource.SetId(_id)
	return iFace.SetWithStruct(false, resource)
}
