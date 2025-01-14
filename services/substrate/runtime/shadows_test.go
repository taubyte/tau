package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/ipfs/go-log/v2"
	"gotest.tools/v3/assert"
)

func init() {
	log.SetAllLoggers(log.LevelDPanic)
}

var ShadowBuff = 10

func TestInstantiate(t *testing.T) {
	vmModule, err := New(context.Background(), newMockServiceable())
	assert.NilError(t, err)

	rt, _, err := vmModule.Instantiate()
	assert.NilError(t, err)

	if rt == nil {
		t.Error("instantiate returned nil runtime")
		return
	}

	var shadowCount int
	for {
		select {
		case shadow := <-vmModule.shadows.instances: // get all shadows from the channel
			if shadow != nil { // check if nil
				shadowCount++ // if not nil and received up count
			}
		case <-time.After(1 * time.Second): // no more shadows from channel
			assert.Equal(t, shadowCount, 2) // one trigger of Instantiate should have created n shadows, where ShadowBuff = n
			return
		}
	}
}

func TestShadowContextCancel(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	vmModule, err := New(ctx, newMockServiceable())
	assert.NilError(t, err)

	ctxC()

	// On context cancel all shadow channels should be closed
	_, ok := <-vmModule.shadows.instances
	assert.Equal(t, ok, false)

	_, ok = <-vmModule.shadows.more
	assert.Equal(t, ok, false)
}

func TestShadowGC(t *testing.T) {
	vmModule, err := New(context.Background(), newMockServiceable())
	assert.NilError(t, err)

	// trigger shadow creation
	vmModule.shadows.more <- 1

	time.Sleep(1 * time.Second)

	assert.Assert(t, len(vmModule.shadows.instances) > 0)

	vmModule.shadows.gc()

	assert.Equal(t, len(vmModule.shadows.instances), 0)
}

func TestShadowCountWithGC(t *testing.T) {
	ShadowMaxAge = 700 * time.Millisecond
	ShadowCleanInterval = 250 * time.Millisecond
	buffer := 100 * time.Millisecond

	vmModule, err := New(context.Background(), newMockServiceable())
	assert.NilError(t, err)

	count := vmModule.shadows.Count()
	// expected 0 shadows as more has not been requested yet
	assert.Equal(t, count, int64(0))

	// request more shadows
	vmModule.shadows.more <- 1
	// wait for all shadows to be created and one cleanup interval
	// none should be cleaned or consumed
	<-time.After(ShadowCleanInterval + buffer)
	count = vmModule.shadows.Count()

	assert.Equal(t, count, int64(0))

	vmModule.shadows.more <- 1
	// 2nd cleanup interval
	// none should be cleaned or consumed
	<-time.After(ShadowCleanInterval + buffer)
	count = vmModule.shadows.Count()
	assert.Equal(t, count, int64(0))

	// 3rd cleanup interval
	// first shadows created should be cleaned up by now
	<-time.After(ShadowCleanInterval + buffer)
	count = vmModule.shadows.Count()
	assert.Equal(t, count, int64(0))

	// 4th cleanup interval
	// all shadows should be cleaned up by now
	<-time.After(ShadowCleanInterval + buffer)
	count = vmModule.shadows.Count()
	assert.Equal(t, count, int64(0))
}

func TestMetrics(t *testing.T) {
	// Create some delay on mocked runtime creation to log some metrics
	runtimeCreationDelay := 50 * time.Millisecond
	serviceable := newMockServiceable()
	serviceable.service.vm.runtimeDelay = runtimeCreationDelay

	vmModule, err := New(context.Background(), serviceable)
	assert.NilError(t, err)

	assert.Equal(t, vmModule.ColdStart(), time.Duration(0))
	assert.Equal(t, vmModule.MemoryMax(), uint64(0))

	_, _, err = vmModule.Instantiate()
	assert.NilError(t, err)

	// wait for all shadows to be created
	<-time.After(runtimeCreationDelay * time.Duration(ShadowBuff))

	// total time should be greater than or equal to created runtimes (shadows + instantiate request) * delay we set
	assert.Assert(t, vmModule.totalColdStart.Load() >= int64(int(runtimeCreationDelay)*ShadowMinBuff))
	// average cold start should be at least as long as delay
	assert.Assert(t, vmModule.ColdStart() >= runtimeCreationDelay)
	// # of cold starts should be equal to shadowBuff(shadows created) +1 (instantiate request)
	assert.Equal(t, vmModule.coldStarts.Load(), uint64(ShadowMinBuff))
}
