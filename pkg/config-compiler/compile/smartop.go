package compile

import (
	"fmt"
	"time"

	"github.com/alecthomas/units"
	"github.com/taubyte/tau/pkg/config-compiler/common"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"
	librarySpec "github.com/taubyte/tau/pkg/specs/library"
)

func smartOps(name string, application string, project projectSchema.Project) (_id string, returnMap map[string]interface{}, err error) {
	iFace, err := project.SmartOps(name, application)
	if err != nil {
		return "", nil, err
	}

	getter := iFace.Get()
	_id = getter.Id()

	_timeout, err := time.ParseDuration(getter.Timeout())
	if err != nil {
		return "", nil, fmt.Errorf("SmartOps( %s/`%s` ): converting time `%s` failed with: %v", application, name, getter.Timeout(), err)
	}

	_memory, err := units.ParseStrictBytes(getter.Memory())
	if err != nil {
		return "", nil, fmt.Errorf("SmartOps( %s/`%s` ): converting memory `%s` failed with: %v", application, name, getter.Memory(), err)
	}

	source := getter.Source()
	library := common.LibraryFromSource(source)

	returnMap = map[string]interface{}{
		"name":        getter.Name(),
		"description": getter.Description(),
		"timeout":     _timeout.Nanoseconds(),
		"memory":      _memory,
		"call":        getter.Call(),
	}

	_tags := getter.Tags()
	if len(_tags) > 0 {
		returnMap["tags"] = _tags
	}

	if len(library) > 0 {
		libId, err := getLibID(library, application, project)
		if err != nil {
			return "", nil, fmt.Errorf("getting library `%s` for function %s/%s failed with: %s", library, application, name, err)
		}

		returnMap["source"] = librarySpec.PathVariable.String() + "/" + libId
	} else {
		returnMap["source"] = source
	}

	return _id, returnMap, nil
}
