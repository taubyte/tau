package compile

import (
	"fmt"

	projectSchema "github.com/taubyte/tau/pkg/schema/project"
)

func domain(name string, application string, project projectSchema.Project) (_id string, returnMap map[string]interface{}, err error) {
	iFace, err := project.Domain(name, application)
	if err != nil {
		return "", nil, fmt.Errorf("opening Domain( %s/`%s` ) failed with: %v", application, name, err)
	}

	getter := iFace.Get()
	_id = getter.Id()

	// TODO handle auto cert
	returnMap = map[string]interface{}{
		"name":        getter.Name(),
		"description": getter.Description(),
		"fqdn":        getter.FQDN(),
		"cert-type":   getter.Type(),
	}

	_tags := getter.Tags()
	if len(_tags) > 0 {
		returnMap["tags"] = _tags
	}

	err = attachSmartOpsFromTags(returnMap, _tags, application, project, "")
	if err != nil {
		return "", nil, fmt.Errorf("domain( %s/`%s` ): Getting smartOps failed with: %v", application, name, err)
	}

	if getter.Type() == "inline" {
		returnMap["cert-file"] = getter.Cert()
		returnMap["key-file"] = getter.Key()
	}

	return _id, returnMap, nil
}
