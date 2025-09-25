package runtime

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/ipfs/go-log/v2"
	components "github.com/taubyte/tau/core/services/substrate/components"
)

var logger = log.Logger("tau.substrate.service.vm")

func New(ctx context.Context, serviceable components.FunctionServiceable) (*Function, error) {
	if config := serviceable.Config(); config != nil {
		dFunc := &Function{
			serviceable:    serviceable,
			ctx:            ctx,
			config:         config,
			branch:         serviceable.Branch(),
			commit:         serviceable.Commit(),
			coldStarts:     new(atomic.Uint64),
			totalColdStart: new(atomic.Int64),
			calls:          new(atomic.Uint64),
			totalCallTime:  new(atomic.Int64),
			maxMemory:      new(atomic.Uint64),

			instanceReqs:       make(chan *instanceRequest, InstanceMaxRequests),
			availableInstances: make(chan Instance, InstanceMaxRequests),
			shutdown:           new(atomic.Bool),
			shutdownDone:       make(chan struct{}),
		}

		go dFunc.intanceManager()

		return dFunc, nil
	}

	return nil, fmt.Errorf("serviceable `%s` function structure is nil", serviceable.Id())
}

func (f *Function) Shutdown() {
	// Check if shutdown is already in progress or completed
	if f.shutdown.Load() {
		// Wait for shutdown to complete if it's already in progress
		<-f.shutdownDone
		return
	}

	// Set shutdown flag to prevent new requests
	f.shutdown.Store(true)

	// Close the instanceReqs channel to signal no more requests will be accepted
	// Use a select to avoid panicking if channel is already closed
	select {
	case <-f.instanceReqs:
		// Channel is already closed, do nothing
	default:
		close(f.instanceReqs)
	}

	// Wait for all pending requests to be processed
	<-f.shutdownDone

	// Close all available instances
	f.shutdownMu.Lock()
	defer f.shutdownMu.Unlock()

	// Use a select to avoid panicking if channel is already closed
	select {
	case <-f.availableInstances:
		// Channel is already closed, do nothing
	default:
		close(f.availableInstances)
		for instance := range f.availableInstances {
			instance.Close()
		}
	}
}
