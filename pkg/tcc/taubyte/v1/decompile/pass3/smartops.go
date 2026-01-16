package pass3

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

		// Reverse timeout/memory parsing
		if err := utils.FormatTimeout(sel, "timeout"); err != nil {
			return nil, err
		}

		if err := utils.FormatMemory(sel, "memory"); err != nil {
			return nil, err
		}

		// Reverse attribute moves
		sel.Move("channel", "pubsub-channel")
		sel.Move("command", "p2p-command")
		sel.Move("method", "http-method")
		sel.Move("methods", "http-methods")
		sel.Move("domains", "http-domains")
		sel.Move("paths", "http-paths")

		// Delete secure (computed field)
		sel.Delete("secure")

		// Swap ID/name back
		_, err = utils.RenameByName(sel)
		if err != nil {
			return nil, fmt.Errorf("renaming smartop %s failed with %w", fn, err)
		}
	}

	return o, nil
}
