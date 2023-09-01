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
		case shadow := <-vmModule.shadows.instances:
			if shadow != nil {
				shadowCount++
			}
		case <-time.After(1 * time.Second): // we are doing no ops, shadow creation should be instant
			assert.Equal(t, shadowCount, ShadowBuff)
			return
		}
	}
}

func TestShadowContextCancel(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	vmModule, err := New(ctx, newMockServiceable(), "master", id.Generate())
	assert.NilError(t, err)

	ctxC()

	_, ok := <-vmModule.shadows.instances
	assert.Equal(t, ok, false)

	_, ok = <-vmModule.shadows.more
	assert.Equal(t, ok, false)
}

func TestShadowGC(t *testing.T) {
	cleanInterval := ShadowCleanInterval
	maxAge := ShadowMaxAge

	ShadowCleanInterval = 500 * time.Millisecond
	ShadowMaxAge = 750 * time.Millisecond
	defer func() {
		ShadowCleanInterval = cleanInterval
		ShadowMaxAge = maxAge
	}()

	vmModule, err := New(context.Background(), newMockServiceable(), "master", id.Generate())
	assert.NilError(t, err)

	vmModule.shadows.more <- struct{}{}
	<-time.After(550 * time.Millisecond)

	var shadowCount int
	for shadowCount < ShadowBuff {
		select {
		case <-vmModule.shadows.instances:
			shadowCount++
		case <-time.After(1 * time.Second):
			if shadowCount != ShadowBuff {
				t.Errorf("expected %d shadows got %d", shadowCount, ShadowBuff)
				return
			}
		}
	}

	vmModule.shadows.more <- struct{}{}
	<-time.After(1 * time.Second)

	select {
	case <-vmModule.shadows.instances:
		t.Error("expected garbage collector to clean shadows")
		return
	case <-time.After(1 * time.Second):
	}
}

func TestMaxError(t *testing.T) {
	serviceable := newMockServiceable()
	serviceable.service.vm.failInstance = true

	vmModule, err := New(context.Background(), serviceable, "master", id.Generate())
	assert.NilError(t, err)

	_, _, err = vmModule.Instantiate()
	assert.ErrorIs(t, err, errorTest)

	vmModule.shadows.more <- struct{}{}
	if _, ok := <-vmModule.shadows.instances; ok {
		t.Error("expected expected instances to close upon max errors")
		return
	}

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
	serviceable.service.vm.failInstance = true
	InstanceErrorCoolDown = 750 * time.Millisecond

	vmModule, err := New(context.Background(), serviceable, "master", id.Generate())
	assert.NilError(t, err)
	maxErrors := InstanceMaxError
	InstanceMaxError = 19
	defer func() {
		InstanceMaxError = maxErrors
	}()

	vmModule.shadows.more <- struct{}{}
	select {
	case _, ok := <-vmModule.shadows.instances:
		if !ok {
			t.Error("expected open channel")
			return
		}
	case <-time.After(1 * time.Second):
		vmModule.shadows.more <- struct{}{}
		select {
		case _, ok := <-vmModule.shadows.instances:
			if !ok {
				t.Error("expected open channel")
				return
			}
		case <-time.After(1 * time.Second):
		}
	}
}
