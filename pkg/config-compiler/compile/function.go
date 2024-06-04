package compile

import (
	"fmt"
	"time"

	"github.com/alecthomas/units"
	"github.com/taubyte/tau/pkg/config-compiler/common"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"
	librarySpec "github.com/taubyte/tau/pkg/specs/library"
)

func function(name string, application string, project projectSchema.Project) (_id string, returnMap map[string]interface{}, err error) {
	iFace, err := project.Function(name, application)
	if err != nil {
		return "", nil, fmt.Errorf("opening Function( %s/`%s` ) failed with: %v", application, name, err)
	}

	getter := iFace.Get()
	_id = getter.Id()
	_type := getter.Type()
	if _type == "pub-sub" {
		_type = "pubsub"
	}

	_domains := getter.Domains()

	_timeout, err := time.ParseDuration(getter.Timeout())
	if err != nil {
		return "", nil, fmt.Errorf("function( %s/`%s` ): converting time `%s` failed with: %v", application, name, getter.Timeout(), err)
	}

	_memory, err := units.ParseStrictBytes(getter.Memory())
	if err != nil {
		return "", nil, fmt.Errorf("function( %s/`%s` ): converting memory `%s` failed with: %v", application, name, getter.Memory(), err)
	}

	source := getter.Source()
	library := common.LibraryFromSource(source)

	returnMap = map[string]interface{}{
		"name":        getter.Name(),
		"description": getter.Description(),
		"type":        _type,
		"timeout":     _timeout.Nanoseconds(),
		"memory":      _memory,
		"call":        getter.Call(),
	}

	_tags := getter.Tags()
	if len(_tags) > 0 {
		returnMap["tags"] = _tags
	}

	err = attachSmartOpsFromTags(returnMap, _tags, application, project, library)
	if err != nil {
		return "", nil, fmt.Errorf("function( %s/`%s` ): Getting smartOps failed with: %v", application, name, err)
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

	if len(_domains) > 0 {
		domIDs, err := getDomIDs(_domains, application, project)
		if err != nil {
			return "", nil, fmt.Errorf("function( %s/`%s` ): Getting domains failed with: %v", application, name, err)
		}
		if len(domIDs) > 0 {
			returnMap["domains"] = domIDs
		}
	}

	switch _type {
	case "http":
		returnMap["method"] = getter.Method()
		returnMap["paths"] = getter.Paths()
		returnMap["secure"] = false
	case "https":
		returnMap["method"] = getter.Method()
		returnMap["paths"] = getter.Paths()
		returnMap["secure"] = true
	default:
		switch _type {
		case "p2p":
			returnMap["command"] = getter.Command()
			returnMap["service"] = getter.Protocol()
		case "pubsub":
			returnMap["channel"] = getter.Channel()
		}
		returnMap["local"] = getter.Local()
	}

	return _id, returnMap, nil
}
