package compile

import (
	"fmt"

	"github.com/alecthomas/units"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"
)

func database(name string, application string, project projectSchema.Project) (_id string, returnMap map[string]interface{}, err error) {
	iFace, err := project.Database(name, application)
	if err != nil {
		return "", nil, fmt.Errorf("opening Database( %s/`%s` ) failed with: %v", application, name, err)
	}

	getter := iFace.Get()
	_id = getter.Id()

	size, err := units.ParseStrictBytes(getter.Size())
	if err != nil {
		return "", nil, fmt.Errorf("database( %s/`%s` ): converting size `%s` failed with: %v", application, name, getter.Size(), err)
	}

	returnMap = map[string]interface{}{
		"name":        getter.Name(),
		"description": getter.Description(),
		"match":       getter.Match(),
		"useRegex":    getter.Regex(),
		"local":       getter.Local(),
		"min":         getter.Min(),
		"max":         getter.Max(),
		"size":        size,
	}

	_tags := getter.Tags()
	if len(_tags) > 0 {
		returnMap["tags"] = _tags
	}

	err = attachSmartOpsFromTags(returnMap, _tags, application, project, "")
	if err != nil {
		return "", nil, fmt.Errorf("database( %s/`%s` ): Getting smartOps failed with: %v", application, name, err)
	}

	if getter.Secret() {
		key, keyType := getter.Encryption()
		returnMap["key"] = key
		returnMap["keyType"] = keyType
	}

	return _id, returnMap, nil
}
