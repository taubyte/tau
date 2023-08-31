package vm

import (
	"context"
	"sync"
	"time"

	"github.com/ipfs/go-log/v2"
)

var logger = log.Logger("substrate.service.vm")

func (w *WasmModule) initShadow() {
	w.shadows = shadows{
		instances: make(chan *shadowInstance, InstanceMaxRequests),
		more:      make(chan struct{}),
		parent:    w,
	}
	w.shadows.ctx, w.shadows.ctxC = context.WithCancel(w.ctx)
	ticker := time.NewTicker(ShadowCleanInterval)
	go func() {
		// defer clean up
		var errCount uint64
		for {
			select {
			case <-ticker.C:
				w.shadows.gc()
			case <-w.shadows.ctx.Done():
				return
			case <-w.shadows.more:
				var wg sync.WaitGroup
				for i := 0; i < 5; i++ {
					if errCount >= InstanceMaxError {
						w.shadows.close()
						return
					}
					wg.Add(1)
					go func() {
						defer wg.Done()
						shadow, err := w.shadows.newInstance()
						if err != nil {
							logger.Errorf("creating new shadow instance failed with: %s", err.Error())
							errCount++
							return
						}
						w.shadows.instances <- shadow
					}()
				}
				wg.Wait()
			}
		}
	}()
}

func (s *shadows) get() (*shadowInstance, error) {
	s.gcLock.RLock()
	defer s.gcLock.RUnlock()

	select {
	case next := <-s.instances:
		defer s.keep()
		return next, nil
	default:
		i, err := s.newInstance()
		if err == nil {
			s.keep()
		}
		return i, err
	}
}

func (s *shadows) gc() {
	s.gcLock.Lock()
	defer s.gcLock.Unlock()
	close(s.instances)

	now := time.Now()
	shadowInstances := make(chan *shadowInstance, InstanceMaxRequests)
	for instance := range s.instances {
		if instance != nil && instance.creation.Sub(now) < ShadowMaxAge {
			shadowInstances <- instance
		}
	}

	s.instances = shadowInstances
}

func (s *shadows) close() {
	s.gcLock.Lock()
	defer s.gcLock.Unlock()
	close(s.instances)
	close(s.more)

	s.parent.serviceable.Service().Cache().Remove(s.parent.serviceable)
	s.ctxC()
}

func (s *shadows) keep() {
	select {
	case s.more <- struct{}{}: // Send if not blocking
	default:
	}
}

func (s *shadows) newInstance() (*shadowInstance, error) {
	runtime, pluginApi, err := s.parent.instantiate()
	if err != nil {
		return nil, err
	}

	return &shadowInstance{
		creation:  time.Now(),
		runtime:   runtime,
		pluginApi: pluginApi,
	}, nil
}
