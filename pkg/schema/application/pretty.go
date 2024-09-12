package application

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/pretty"
)

// Prettify takes a Prettier and resources, returns map[string]interface{}
//
// resources is a slice of resource information and methods for building items within the map.
func (s *application) Prettify(p pretty.Prettier, resources []pretty.PrettyResourceIface) map[string]interface{} {
	getter := s.Get()
	appName := getter.Name()

	obj := map[string]interface {
	}{
		"Id":          getter.Id(),
		"Name":        appName,
		"Description": getter.Description(),
		"Tags":        getter.Tags(),
	}

	errors := []error{}
	for _, resource := range resources {
		local, _ := resource.List(appName)
		if len(local) == 0 {
			continue
		}

		resourceMap := map[string]interface{}{}
		for _, name := range local {
			resourceObject, err := resource.Get(name, appName)
			if err != nil {
				errors = append(errors, fmt.Errorf("getting %s/%s failed with: %s", appName, name, err))
				continue
			}

			resourceMap[name] = resourceObject.Prettify(p)
		}

		if len(resourceMap) > 0 {
			obj[resource.Type] = resourceMap
		}
	}

	if len(errors) > 0 {
		obj["Errors"] = errors
	}

	return obj
}
