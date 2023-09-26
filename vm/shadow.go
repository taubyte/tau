package vm

import (
	"context"
	"runtime"
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
		coldStart: &Metrics{},
		calls:     &Metrics{},
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
			case <-coolDown.C:
				if errCount := f.shadows.errors.Load(); errCount > 0 {
					f.shadows.errors.Swap(errCount / 2)
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
						if f.shadows.errors.Load() < InstanceMaxError && len(f.shadows.instances) < InstanceMaxRequests {
							shadow, err := f.shadows.newInstance()
							if err != nil {
								logger.Errorf("creating new shadow instance failed with: %s", err.Error())
								f.shadows.errors.Add(1)
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
				if f.shadows.errors.Load() >= InstanceMaxError {
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
		s.available.Swap(0)
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

type runtimeMetric struct {
	startTime time.Time
	initAlloc uint64
	maxAlloc  uint64
	ctx       context.Context
	ctxC      context.CancelFunc
}

func (s *Shadows) startMetric(ctx context.Context) *runtimeMetric {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	metric := &runtimeMetric{
		startTime: time.Now(),
		initAlloc: m.Alloc,
	}
	metric.ctx, metric.ctxC = context.WithCancel(ctx)

	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				runtime.ReadMemStats(&m)
				if m.Alloc > metric.maxAlloc {
					metric.maxAlloc = m.Alloc
				}
			case <-metric.ctx.Done():
				return
			}
		}
	}()

	return metric
}

func (m *runtimeMetric) stop() (time.Duration, int64) {
	m.ctxC()
	return time.Since(m.startTime), int64(m.maxAlloc - m.initAlloc)
}

func (s *Shadows) ColdStart() *Metrics {
	return s.coldStart
}

func (s *Shadows) Calls() *Metrics {
	return s.calls
}

func (m *Metrics) DurationAverage() time.Duration {
	if totalCount := m.totalCount.Load(); totalCount > 0 {
		return time.Duration(m.totalTime.Load() / totalCount)
	}

	return 0
}

func (m *Metrics) MemoryMax() int64 {
	return m.maxMemory.Load()
}
