package pass2

import (
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
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

	for _, smartop := range osmartops.Children() {
		sel := osmartops.Child(smartop)

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
