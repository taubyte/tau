package cache

import (
	"context"
	"testing"
	"time"

	"github.com/taubyte/odo/protocols/node/components/smartops/instance"
)

var (
	testProject     = "project"
	testApplication = "application"
	smartOp1        = "smartOpId1"
	smartOp2        = "smartOpId2"
)

// TODO: Need to redo cache
func TestCache(t *testing.T) {
	t.Skip("smartops needs to be redone")
	cacheItemTTL = 10000 * time.Nanosecond

	c := New(context.Background())

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	_ctx1, _cancel1 := context.WithCancel(context.Background())
	defer _cancel1()
	_ctx2, _cancel2 := context.WithCancel(context.Background())
	defer _cancel2()

	instance1 := instance.MockInstance(_ctx1)
	instance2 := instance.MockInstance(_ctx2)

	_, ok := c.Get(testProject, testApplication, smartOp1, ctx1)
	if ok == true {
		t.Errorf("Expected ok to be false, got %v", ok)
	}

	err := c.Put(testProject, testApplication, smartOp1, ctx1, instance1)
	if err != nil {
		t.Errorf("failed to put instance in cache: %s", err)
		return
	}

	err = c.Put(testProject, testApplication, smartOp2, ctx2, instance2)
	if err != nil {
		t.Errorf("failed to put instance in cache: %s", err)
		return
	}

	instance, ok := c.Get(testProject, testApplication, smartOp1, ctx1)
	if ok == false {
		t.Errorf("Expected ok to be true, got %v", ok)
	}
	if instance != instance1 {
		t.Errorf("Expected instance to be %v, got %v", instance1, instance)
	}

	instance, ok = c.Get(testProject, testApplication, smartOp2, ctx2)
	if ok == false {
		t.Errorf("Expected ok to be true, got %v", ok)
	}
	if instance != instance2 {
		t.Errorf("Expected instance to be %v, got %v", instance2, instance)
	}

	cancel1()
	cancel2()

	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel3()
	ctx4, cancel4 := context.WithCancel(context.Background())
	defer cancel4()

	i1, ok := c.Get(testProject, testApplication, smartOp1, ctx3)
	if ok == true {
		// Wait for context of the instance to potentially cancel
		time.Sleep(1 * time.Second)
		if i1.Context().Err() != nil {
			t.Errorf("Expected ok to be false, got %v", ok)
			return
		}
	}

	time.Sleep(1 * time.Second) // wait for item to clear
	_, ok = c.Get(testProject, testApplication, smartOp2, ctx4)
	if ok == true {
		t.Errorf("Expected ok to be false, got %v", ok)
		return
	}
}
