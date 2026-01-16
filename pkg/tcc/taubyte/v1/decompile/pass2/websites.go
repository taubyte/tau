package pass2

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type websites struct{}

func Websites() transform.Transformer[object.Refrence] {
	return &websites{}
}

func (a *websites) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	os, err := o.Child("websites").Object()
	if err != nil {
		if err == object.ErrNotExist {
			return o, nil
		}
		return nil, fmt.Errorf("fetching websites failed with %w", err)
	}

	// Build ID->name map for domains
	domainIdToName, err := buildIdToNameMap(ct, o, "domains")
	if err != nil {
		return nil, fmt.Errorf("building domain ID map failed with %w", err)
	}

	for _, fn := range os.Children() {
		sel := os.Child(fn)

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
	}

	return o, nil
}
