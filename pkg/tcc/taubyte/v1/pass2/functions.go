package pass2

import (
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
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

	for _, fn := range ofuncs.Children() {
		sel := ofuncs.Child(fn)

		domains, err := sel.Get("domains")
		if err == nil && domains != nil {
			domainsSlice, ok := domains.([]string)
			if !ok {
				return nil, fmt.Errorf("domains is not a []string")
			}
			ret, err := utils.ResolveNamesToId(ct, "domains", domainsSlice)
			if err != nil {
				return nil, fmt.Errorf("resolving domain names to IDs failed with %w", err)
			}

			sel.Set("domains", ret)
		}

		source, err := sel.Get("source")
		if err == nil && source != nil {
			sourceVal, ok := source.(string)
			if !ok {
				return nil, fmt.Errorf("source is not a string")
			}

			if strings.HasPrefix(sourceVal, "libraries/") {
				sourceVal = strings.TrimPrefix(sourceVal, "libraries/")

				ret, err := utils.ResolveNamesToId(ct, "libraries", []string{sourceVal})
				if err != nil || len(ret) == 0 {
					return nil, fmt.Errorf("resolving library names to IDs failed with %w", err)
				}

				sel.Set("source", "libraries/"+ret[0])
			}
		}

	}

	return o, nil

}
