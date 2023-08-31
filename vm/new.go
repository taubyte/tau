package vm

import (
	"context"
	"fmt"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
)

func New(ctx context.Context, serviceable commonIface.Serviceable, branch, commit string) (*WasmModule, error) {
	if structure := serviceable.Structure(); structure != nil {
		w := &WasmModule{
			serviceable: serviceable,
			ctx:         ctx,
			structure:   structure,
			branch:      branch,
			commit:      commit,
		}

		w.initShadow()

		return w, nil
	}

	return nil, fmt.Errorf("serviceable `%s` function structure is nil", serviceable.Id())
}
