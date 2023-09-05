package vm

import (
	"context"
	"testing"
	"time"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/utils/id"
	"gotest.tools/v3/assert"
)

func init() {
	log.SetAllLoggers(log.LevelDPanic)
}

func TestInstantiate(t *testing.T) {
	vmModule, err := New(context.Background(), newMockServiceable(), "master", id.Generate())
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
			assert.Equal(t, shadowCount, ShadowBuff) // one trigger of Instantiate should have created n shadows, where ShadowBuff = n
			return
		}
	}
}

func TestShadowContextCancel(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	vmModule, err := New(ctx, newMockServiceable(), "master", id.Generate())
	assert.NilError(t, err)

	ctxC()

	// On context cancel all shadow channels should be closed
	_, ok := <-vmModule.shadows.instances
	assert.Equal(t, ok, false)

	_, ok = <-vmModule.shadows.more
	assert.Equal(t, ok, false)
}

func TestShadowGC(t *testing.T) {
	cleanInterval := ShadowCleanInterval
	maxAge := ShadowMaxAge

	// Set interval slightly shorter than the max age, so one interval wont clean
	ShadowCleanInterval = 500 * time.Millisecond
	ShadowMaxAge = 750 * time.Millisecond
	defer func() {
		ShadowCleanInterval = cleanInterval
		ShadowMaxAge = maxAge
	}()

	vmModule, err := New(context.Background(), newMockServiceable(), "master", id.Generate())
	assert.NilError(t, err)

	// trigger shadow creation
	vmModule.shadows.more <- struct{}{}
	// wait for one clean interval
	<-time.After(550 * time.Millisecond)

	var shadowCount int
	for shadowCount < ShadowBuff {
		select {
		// get shadows
		case <-vmModule.shadows.instances:
			shadowCount++
		// if receiving no more shadows after 1 sec of no select case
		case <-time.After(1 * time.Second):
			// check if number of shadows is equal to ShadowBuff var
			if shadowCount != ShadowBuff {
				t.Errorf("expected %d shadows got %d", shadowCount, ShadowBuff)
				return
			}
		}
	}

	// create more shadows
	vmModule.shadows.more <- struct{}{}
	// wait 2 clean intervals, by now all shadows should be clean, as they have reached max age
	<-time.After(1 * time.Second)

	select {
	case <-vmModule.shadows.instances: //case where there are shadows available
		t.Error("expected garbage collector to clean shadows")
		return
	case <-time.After(1 * time.Second): //else none at all
	}
}

func TestMaxError(t *testing.T) {
	serviceable := newMockServiceable()
	serviceable.service.vm.failInstance = true // creating an instance will always fail

	vmModule, err := New(context.Background(), serviceable, "master", id.Generate())
	assert.NilError(t, err)

	// Instantiate will check channel for shadows
	// If none, one will be created, and returned, if it is successful then shadows are created
	_, _, err = vmModule.Instantiate()
	assert.ErrorIs(t, err, errorTest)

	// Instantiate failed to create a runtime, thus need to trigger more manually
	vmModule.shadows.more <- struct{}{}
	// max errors for failure = shadowBuff, thus 10/10 errors will close channels
	if _, ok := <-vmModule.shadows.instances; ok {
		t.Error("expected expected instances to close upon max errors")
		return
	}

	// Essentially same thing as previous calls, but earlier error limit for coverage
	vmModule.initShadow()
	maxErrors := InstanceMaxError
	InstanceMaxError = 5
	defer func() {
		InstanceMaxError = maxErrors
	}()

	vmModule.shadows.more <- struct{}{}
	if _, ok := <-vmModule.shadows.instances; ok {
		t.Error("expected expected instances to close upon max errors")
		return
	}
}

func TestCoolDown(t *testing.T) {
	serviceable := newMockServiceable()
	serviceable.service.vm.failInstance = true // creating an instance will always fail
	InstanceErrorCoolDown = 750 * time.Millisecond

	vmModule, err := New(context.Background(), serviceable, "master", id.Generate())
	assert.NilError(t, err)

	maxErrors := InstanceMaxError
	// more will attempt to create 10 shadows
	// 2 consecutive mores will hit the max error max
	InstanceMaxError = 19
	defer func() {
		InstanceMaxError = maxErrors
	}()

	vmModule.shadows.more <- struct{}{} // nErrors = 10
	select {
	case _, ok := <-vmModule.shadows.instances:
		if !ok {
			// 10/19 max errors, channel should still be open
			t.Error("expected open channel")
			return
		}
	case <-time.After(1 * time.Second): // cool down, half the errors nErrors = 5
		vmModule.shadows.more <- struct{}{} // nErrors = 15
		select {
		case _, ok := <-vmModule.shadows.instances:
			if !ok {
				// 15/19 max errors, channel should still be open
				t.Error("expected open channel")
				return
			}
		case <-time.After(1 * time.Second): // cool down, half the errors nErrors = 8
			vmModule.shadows.more <- struct{}{} // nErrors = 18
			vmModule.shadows.more <- struct{}{} // nErrors = 28
			// hit the max errors, therefore shadow channels should close
			if _, ok := <-vmModule.shadows.instances; ok {
				t.Error("expected closed channel")
			}
		}
	}
}
