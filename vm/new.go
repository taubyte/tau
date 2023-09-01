package vm

import (
	"context"
	"fmt"

	components "github.com/taubyte/go-interfaces/services/substrate/components"
)

func New(ctx context.Context, serviceable components.FunctionServiceable, branch, commit string) (*WasmModule, error) {
	if config := serviceable.Config(); config != nil {
		w := &WasmModule{
			serviceable: serviceable,
			ctx:         ctx,
			structure:   config,
			branch:      branch,
			commit:      commit,
		}

		w.initShadow()

		return w, nil
	}

	return nil, fmt.Errorf("serviceable `%s` function structure is nil", serviceable.Id())
}
