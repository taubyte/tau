package vm

import (
	"context"
	"fmt"

	components "github.com/taubyte/go-interfaces/services/substrate/components"
)

func New(ctx context.Context, serviceable components.FunctionServiceable, branch, commit string) (*DFunc, error) {
	if config := serviceable.Config(); config != nil {
		dFunc := &DFunc{
			serviceable: serviceable,
			ctx:         ctx,
			structure:   config,
			branch:      branch,
			commit:      commit,
		}

		dFunc.initShadow()

		return dFunc, nil
	}

	return nil, fmt.Errorf("serviceable `%s` function structure is nil", serviceable.Id())
}
