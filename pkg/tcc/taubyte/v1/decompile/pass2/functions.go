package pass2

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type functions struct{}

func Functions() transform.Transformer[object.Refrence] {
	return &functions{}
}

func (a *functions) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	ofuncs, err := o.Child("functions").Object()
	if err != nil {
		if err == object.ErrNotExist {
			return o, nil
		}
		return nil, fmt.Errorf("fetching functions failed with %w", err)
	}

	// Build ID->name map for domains
	domainIdToName, err := buildIdToNameMap(ct, o, "domains")
	if err != nil {
		return nil, fmt.Errorf("building domain ID map failed with %w", err)
	}

	// Build ID->name map for libraries
	libraryIdToName, err := buildIdToNameMap(ct, o, "libraries")
	if err != nil {
		return nil, fmt.Errorf("building library ID map failed with %w", err)
	}

	for _, fn := range ofuncs.Children() {
		sel := ofuncs.Child(fn)

		// Resolve domain IDs back to names
		domains, err := sel.Get("domains")
		if err == nil && domains != nil {
			domainsSlice, ok := domains.([]string)
			if !ok {
				return nil, fmt.Errorf("domains is not a []string")
			}

			names := make([]string, 0, len(domainsSlice))
			for _, id := range domainsSlice {
				name, ok := domainIdToName[id]
				if !ok {
					return nil, fmt.Errorf("domain ID %s not found", id)
				}
				names = append(names, name)
			}

			sel.Set("domains", names)
		}

		// Resolve library ID in source back to name
		source, err := sel.Get("source")
		if err == nil && source != nil {
			sourceVal, ok := source.(string)
			if !ok {
				return nil, fmt.Errorf("source is not a string")
			}

			if len(sourceVal) > 10 && sourceVal[:10] == "libraries/" {
				libraryId := sourceVal[10:]
				name, ok := libraryIdToName[libraryId]
				if !ok {
					return nil, fmt.Errorf("library ID %s not found", libraryId)
				}
				sel.Set("source", "libraries/"+name)
			}
		}
	}

	return o, nil
}
