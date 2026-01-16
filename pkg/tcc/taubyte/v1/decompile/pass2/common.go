package pass2

import (
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

// buildIdToNameMap builds a map from resource ID to name by iterating through
// all resources of the given type. The resources are keyed by ID and have a "name" attribute.
// When in an application context, also checks the root object for global resources.
func buildIdToNameMap(ct transform.Context[object.Refrence], o object.Object[object.Refrence], group string) (map[string]string, error) {
	idToName := make(map[string]string)

	// Check current object (application or root)
	groupObj, err := o.Child(group).Object()
	if err == nil {
		for _, id := range groupObj.Children() {
			sel := groupObj.Child(id)
			name, err := sel.GetString("name")
			if err != nil {
				// Skip resources without name attribute
				continue
			}
			idToName[id] = name
		}
	}

	// If we're in an application context, also check root for global resources
	ctp := ct.Path()
	if len(ctp) > 1 {
		// We're in an application context, check root object
		root, ok := ctp[0].(object.Object[object.Refrence])
		if ok {
			rootGroupObj, err := root.Child(group).Object()
			if err == nil {
				for _, id := range rootGroupObj.Children() {
					// Only add if not already in map (local resources take precedence)
					if _, exists := idToName[id]; !exists {
						sel := rootGroupObj.Child(id)
						name, err := sel.GetString("name")
						if err == nil {
							idToName[id] = name
						}
					}
				}
			}
		}
	}

	return idToName, nil
}
