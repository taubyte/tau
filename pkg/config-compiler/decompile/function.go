package decompile

import (
	"fmt"

	"github.com/mitchellh/mapstructure"
	"github.com/taubyte/tau/pkg/config-compiler/common"
	lib "github.com/taubyte/tau/pkg/schema/functions"
	projectLib "github.com/taubyte/tau/pkg/schema/project"
	librarySpec "github.com/taubyte/tau/pkg/specs/library"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func function(project projectLib.Project, _id string, obj interface{}, appName string) error {
	resource := &structureSpec.Function{}
	mapstructure.Decode(obj, resource)

	iFace, err := project.Function(resource.Name, appName)
	if err != nil {
		return fmt.Errorf("open function `%s/%s` failed: %s", appName, resource.Name, err)
	}

	resource.SetId(_id)
	return iFace.SetWithStruct(false, resource)
}

func function_clean(project projectLib.Project, name, app string) (err error) {
	function, err := project.Function(name, app)
	if err != nil {
		return fmt.Errorf("couldn't open function `%s/%s` to clean: %v", app, name, err)
	}

	oldSource := function.Get().Source()
	if oldLib := common.LibraryFromSource(oldSource); len(oldLib) > 0 {
		newLib, err := cleanLibs(project, oldLib, app)
		if err != nil {
			return fmt.Errorf("clean libraries of function `%s/%s` failed with: %v", app, name, err)
		}

		if err = function.Set(false, lib.Source(librarySpec.PathVariable.String()+"/"+newLib)); err != nil {
			return fmt.Errorf("set libraries of function  `%s`%s` failed with: %w", app, name, err)
		}
	}

	old_domains := function.Get().Domains()
	new_domains, err := cleanDoms(project, old_domains, app)
	if err != nil {
		return fmt.Errorf("clean domains of function `%s/%s` failed with: %v", app, name, err)
	}

	if len(new_domains) > 0 {
		err = function.Set(false, lib.Domains(new_domains))
		if err != nil {
			return fmt.Errorf("set domains of website `%s/%s` failed with: %v", app, name, err)
		}
	}
	return
}
