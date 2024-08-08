package runtime

import (
	"context"
	"sync"
	"time"
)

func (f *Function) Shadows() *Shadows {
	return f.shadows
}

func (f *Function) initShadow() {
	f.shadows = &Shadows{
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

		for {
			select {
			case <-f.shadows.ctx.Done():
				return
			case <-coolDown.C:
				if errCount := f.errorCount.Load(); errCount > 0 {
					f.errorCount.Store(errCount / 2)
				}
			case <-ticker.C:
				f.shadows.gc()
			case <-f.shadows.more:
				var wg sync.WaitGroup
				for i := 0; i < ShadowBuff; i++ {
					wg.Add(1)
					go func() { // too much go routines
						defer wg.Done()
						if f.errorCount.Load() < InstanceMaxError && len(f.shadows.instances) < InstanceMaxRequests {
							shadow, err := f.shadows.newInstance()
							if err != nil {
								logger.Errorf("creating new shadow instance failed with: %s", err.Error())
								f.errorCount.Add(1)
								return
							}
							select {
							case <-f.shadows.ctx.Done():
								return
							case f.shadows.instances <- shadow:
								f.shadows.available.Add(1)
							}
						}
					}()
				}
				wg.Wait()
				if f.errorCount.Load() >= InstanceMaxError {
					return
				}
			}
		}
	}()
}

func (s *Shadows) get() (*shadowInstance, error) {
	select {
	case next := <-s.instances:
		defer s.keep()
		s.available.Add(-1)
		return next, nil
	default:
		i, err := s.newInstance()
		if err == nil {
			s.keep()
		}
		return i, err
	}
}

func (s *Shadows) gc() {
	now := time.Now()
	shadowInstances := make([]*shadowInstance, 0, InstanceMaxRequests)
	defer func() {
		s.available.Store(0)
		for _, instance := range shadowInstances {
			s.instances <- instance
			s.available.Add(1)
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

func (s *Shadows) keep() {
	select {
	case s.more <- struct{}{}: // Send if not blocking
	default:
	}
}

func (s *Shadows) newInstance() (*shadowInstance, error) {
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

func (s *Shadows) Count() int64 {
	return s.available.Load()
}
