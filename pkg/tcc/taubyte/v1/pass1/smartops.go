package pass1

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type smartops struct{}

func Smartops() transform.Transformer[object.Refrence] {
	return &smartops{}
}

func (a *smartops) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	ofuncs, err := o.Child("smartops").Object()
	if err != nil {
		if err == object.ErrNotExist {
			return o, nil
		}
		return nil, fmt.Errorf("fetching smartops failed with %w", err)
	}

	for _, fn := range ofuncs.Children() {
		sel := ofuncs.Child(fn)

		if err := utils.ParseTimeout(sel, "timeout"); err != nil {
			return nil, err
		}

		if err := utils.ParseMemory(sel, "memory"); err != nil {
			return nil, err
		}

		sel.Move("pubsub-channel", "channel") // to resolve to ID
		sel.Move("p2p-command", "command")

		sel.Move("http-method", "method") // compat - should be deprecated
		sel.Move("http-methods", "methods")
		sel.Move("http-domains", "domains") // to resolve to ID
		sel.Move("http-paths", "paths")

		trigerType, err := sel.Get("type")
		if err == nil {
			if trigerType == "http" {
				sel.Set("secure", false)
			} else if trigerType == "https" {
				sel.Set("secure", true)
			}
		}

		idStr, err := utils.RenameById(sel, fn)
		if err != nil {
			return nil, err
		}

		err = utils.IndexById(ct, "smartops", fn, idStr)
		if err != nil {
			return nil, fmt.Errorf("indexing smartops %s failed with %w", fn, err)
		}
	}

	return o, nil

}
