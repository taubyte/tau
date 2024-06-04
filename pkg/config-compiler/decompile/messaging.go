package decompile

import (
	"fmt"

	"github.com/mitchellh/mapstructure"
	projectLib "github.com/taubyte/tau/pkg/schema/project"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func messaging(project projectLib.Project, _id string, obj interface{}, appName string) error {
	resource := &structureSpec.Messaging{}
	mapstructure.Decode(obj, resource)

	iFace, err := project.Messaging(resource.Name, appName)
	if err != nil {
		return fmt.Errorf("open messaging `%s/%s` failed: %s", appName, resource.Name, err)
	}

	resource.SetId(_id)
	return iFace.SetWithStruct(false, resource)
}
