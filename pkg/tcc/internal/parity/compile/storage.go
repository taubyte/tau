package compile

import (
	"fmt"
	"time"

	"github.com/alecthomas/units"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"
)

func storage(name string, application string, project projectSchema.Project) (_id string, returnMap map[string]interface{}, err error) {
	iFace, err := project.Storage(name, application)
	if err != nil {
		return "", nil, err
	}
	getter := iFace.Get()
	_id = getter.Id()

	_size, err := units.ParseStrictBytes(getter.Size())
	if err != nil {
		return "", nil, fmt.Errorf("Storage( %s/`%s` ): converting size `%s` failed with: %v", application, name, getter.Size(), err)
	}

	returnMap = map[string]interface{}{
		"name":        getter.Name(),
		"description": getter.Description(),
		"type":        getter.Type(),
		"match":       getter.Match(),
		"useRegex":    getter.Regex(),
		"public":      getter.Public(),
		"size":        _size,
	}

	_tags := getter.Tags()
	if len(_tags) > 0 {
		returnMap["tags"] = _tags
	}

	err = attachSmartOpsFromTags(returnMap, _tags, application, project, "")
	if err != nil {
		return "", nil, fmt.Errorf("Storage( %s/`%s` ): Getting smartOps failed with: %v", application, name, err)
	}

	switch getter.Type() {
	case "object":
		returnMap["versioning"] = getter.Versioning()
	case "streaming":
		_ttl, err := time.ParseDuration(getter.TTL())
		if err != nil {
			return "", nil, fmt.Errorf("Storage( %s/`%s` ): converting time `%s` failed with: %v", application, name, getter.TTL(), err)
		}
		returnMap["ttl"] = _ttl.Nanoseconds()
	}

	return _id, returnMap, nil
}
