package pass2

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type smartops struct{}

func Smartops() transform.Transformer[object.Refrence] {
	return &smartops{}
}

func (a *smartops) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	osmartops, err := o.Child("smartops").Object()
	if err != nil {
		if err == object.ErrNotExist {
			return o, nil
		}
		return nil, fmt.Errorf("fetching smartops failed with %w", err)
	}

	// Build ID->name map for libraries
	libraryIdToName, err := buildIdToNameMap(ct, o, "libraries")
	if err != nil {
		return nil, fmt.Errorf("building library ID map failed with %w", err)
	}

	for _, smartop := range osmartops.Children() {
		sel := osmartops.Child(smartop)

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
