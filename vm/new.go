package vm

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/ipfs/go-log/v2"
	components "github.com/taubyte/go-interfaces/services/substrate/components"
)

var logger = log.Logger("tau.substrate.service.vm")

func New(ctx context.Context, serviceable components.FunctionServiceable, branch, commit string) (*Function, error) {
	if config := serviceable.Config(); config != nil {
		dFunc := &Function{
			serviceable:    serviceable,
			ctx:            ctx,
			config:         config,
			branch:         branch,
			commit:         commit,
			coldStarts:     new(atomic.Uint64),
			totalColdStart: new(atomic.Int64),
			calls:          new(atomic.Uint64),
			totalCallTime:  new(atomic.Int64),
			maxMemory:      new(atomic.Uint64),
		}

		dFunc.maxMemory.Store(uint64(serviceable.Config().Memory))

		dFunc.initShadow()

		return dFunc, nil
	}

	return nil, fmt.Errorf("serviceable `%s` function structure is nil", serviceable.Id())
}
