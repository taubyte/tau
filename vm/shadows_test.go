package vm

import (
	"context"
	"testing"
	"time"

	"github.com/taubyte/utils/id"
	"gotest.tools/v3/assert"
)

func TestInstantiate(t *testing.T) {
	vmModule, err := New(context.Background(), &mockServiceable{}, "master", id.Generate())
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

func TestShadowClose(t *testing.T) {
	vmModule, err := New(context.Background(), &mockServiceable{}, "master", id.Generate())
	assert.NilError(t, err)

	vmModule.shadows.close()

	_, ok := <-vmModule.shadows.instances
	assert.Equal(t, ok, false)

	_, ok = <-vmModule.shadows.more
	assert.Equal(t, ok, false)
}

func TestShadowContextCancel(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	vmModule, err := New(ctx, &mockServiceable{}, "master", id.Generate())
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

	ShadowCleanInterval = 250 * time.Millisecond
	ShadowMaxAge = 100 * time.Millisecond
	defer func() {
		ShadowCleanInterval = cleanInterval
		ShadowMaxAge = maxAge
	}()

	vmModule, err := New(context.Background(), &mockServiceable{}, "master", id.Generate())
	assert.NilError(t, err)

	vmModule.shadows.more <- struct{}{}
	<-time.After(1 * time.Second)

	select {
	case <-vmModule.shadows.instances:
		t.Error("expected garbage collector to clean shadows")
		return
	case <-time.After(1 * time.Second):
	}

}
