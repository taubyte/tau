package vm

import (
	"context"
	"sync"
	"time"

	"github.com/ipfs/go-log/v2"
)

var logger = log.Logger("substrate.service.vm")

func (f *Function) initShadow() {
	f.shadows = shadows{
		instances: make(chan *shadowInstance, InstanceMaxRequests),
		more:      make(chan struct{}, 1),
		parent:    f,
	}
	f.shadows.ctx, f.shadows.ctxC = context.WithCancel(f.ctx)
	ticker := time.NewTicker(ShadowCleanInterval)
	coolDown := time.NewTicker(InstanceErrorCoolDown)
	go func() {
		defer func() {
			f.shadows.ctxC()
			close(f.shadows.instances)
			close(f.shadows.more)

			f.serviceable.Service().Cache().Remove(f.serviceable)
		}()
		var errCount int
		for {
			select {
			case <-coolDown.C:
				if errCount > 0 {
					errCount = errCount / 2
				}
			case <-ticker.C:
				f.shadows.gc()
			case <-f.shadows.ctx.Done():
				return
			case <-f.shadows.more:
				var wg sync.WaitGroup
				for i := 0; i < ShadowBuff; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						if errCount < InstanceMaxError && len(f.shadows.instances) < InstanceMaxRequests {
							shadow, err := f.shadows.newInstance()
							if err != nil {
								logger.Errorf("creating new shadow instance failed with: %s", err.Error())
								errCount++
								return
							}
							select {
							case <-f.shadows.ctx.Done():
								return
							case f.shadows.instances <- shadow:
							}
						}
					}()
				}
				wg.Wait()
				if errCount >= InstanceMaxError {
					return
				}
			}
		}
	}()
}

func (s *shadows) get() (*shadowInstance, error) {
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
	now := time.Now()
	shadowInstances := make([]*shadowInstance, 0, InstanceMaxRequests)
	defer func() {
		for _, instance := range shadowInstances {
			s.instances <- instance
		}
	}()

	for {
		select {
		case instance := <-s.instances:
			if instance != nil && now.Sub(instance.creation) < ShadowMaxAge {
				shadowInstances = append(shadowInstances, instance)
			}
		default:
			return
		}
	}
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
