package pass3

import (
	"fmt"

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

		// Handle type and secure
		secure, err := sel.GetBool("secure")
		if err == nil {
			// Delete secure (computed field)
			sel.Delete("secure")
			// Determine type from secure
			trigerType, err := sel.Get("type")
			if err == nil {
				if trigerType == "p2p" {
					sel.Move("service", "p2p-protocol")
				} else {
					// For http/https, determine from secure
					if secure {
						sel.Set("type", "https")
					} else {
						sel.Set("type", "http")
					}
				}
			}
		} else {
			// No secure field, check if it's p2p
			trigerType, err := sel.Get("type")
			if err == nil && trigerType == "p2p" {
				sel.Move("service", "p2p-protocol")
			}
		}

		// Swap ID/name back
		_, err = utils.RenameByName(sel)
		if err != nil {
			return nil, fmt.Errorf("renaming function %s failed with %w", fn, err)
		}
	}

	return o, nil
}
