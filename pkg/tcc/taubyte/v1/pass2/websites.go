package pass2

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
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

	for _, fn := range os.Children() {
		sel := os.Child(fn)

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
	}

	return o, nil

}
