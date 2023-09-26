package vm

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/ipfs/go-log/v2"
	components "github.com/taubyte/go-interfaces/services/substrate/components"
)

var logger = log.Logger("substrate.service.vm")

func New(ctx context.Context, serviceable components.FunctionServiceable, branch, commit string) (*Function, error) {
	if config := serviceable.Config(); config != nil {
		dFunc := &Function{
			serviceable:    serviceable,
			ctx:            ctx,
			config:         config,
			branch:         branch,
			commit:         commit,
			coldStarts:     new(atomic.Int64),
			totalColdStart: new(atomic.Int64),
			calls:          new(atomic.Int64),
			totalCallTime:  new(atomic.Int64),
			maxMemory:      new(atomic.Int64),
		}

		dFunc.initShadow()

		return dFunc, nil
	}

	return nil, fmt.Errorf("serviceable `%s` function structure is nil", serviceable.Id())
}
