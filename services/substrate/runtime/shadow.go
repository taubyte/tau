package runtime

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

var globalInstanceCount int64

func (f *Function) Shadows() *Shadows {
	return f.shadows
}

func (f *Function) initShadow() {
	f.shadows = &Shadows{
		instances: make(chan *shadowInstance, InstanceMaxRequests),
		more:      make(chan int, 1),
		parent:    f,
		mu:        sync.Mutex{},
		lastCheck: time.Now(),
	}
	f.shadows.ctx, f.shadows.ctxC = context.WithCancel(f.ctx)

	ticker := time.NewTicker(ShadowCleanInterval)
	coolDown := time.NewTicker(InstanceErrorCoolDown)

	go func() {
		defer func() {
			f.shadows.ctxC()
			close(f.shadows.more)
			close(f.shadows.instances)

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
			case desiredCount := <-f.shadows.more:
				for i := 0; i < desiredCount; i++ {
					for {
						if f.errorCount.Load() < InstanceMaxError && atomic.LoadInt64(&globalInstanceCount) < MaxGlobalInstances {
							break
						}
						select {
						case <-f.shadows.ctx.Done():
							return
						case <-time.After(50 * time.Millisecond):
						}
					}

					totalMemAndSwap, usedMemAndSwap, memTotal, memUsed, err := getTotalAndUsedMemory()
					if err != nil {
						logger.Errorf("failed to get memory stats: %s", err.Error())
						continue
					}

					usedMemoryPercentage := (usedMemAndSwap * 100) / totalMemAndSwap
					if usedMemoryPercentage < MemoryThreshold {
						maxMemory := f.config.Memory
						if maxMemory == 0 {
							maxMemory = DefaultWasmMemory
						}

						if memUsed+maxMemory <= memTotal {
							shadow, err := f.shadows.newInstance()
							if err != nil {
								logger.Errorf("creating new shadow instance failed with: %s", err.Error())
								f.errorCount.Add(1)
								continue
							}
							select {
							case <-f.shadows.ctx.Done():
								return
							case f.shadows.instances <- shadow:
								f.shadows.available.Add(1)
								atomic.AddInt64(&globalInstanceCount, 1)
							}
						} else {
							logger.Warnf("insufficient memory to create new instance, required: %d bytes", maxMemory)
						}
					} else {
						logger.Warnf("memory usage is high: %d%%, throttling instance creation", usedMemoryPercentage)
					}
				}
				if f.errorCount.Load() >= InstanceMaxError {
					return
				}
			}
		}
	}()
}

func (s *Shadows) get() (*shadowInstance, error) {
	s.mu.Lock()
	now := time.Now()
	elapsed := now.Sub(s.lastCheck).Seconds()
	if elapsed >= 1.0 {
		s.currentRPS = float64(s.requestCount) / elapsed
		s.requestCount = 0
		s.lastCheck = now
	}
	s.requestCount++

	desiredBuffer := int(math.Ceil(s.currentRPS*1.2)) % (int(MaxGlobalInstances) - int(atomic.LoadInt64(&globalInstanceCount)))
	if desiredBuffer < 1 {
		desiredBuffer = ShadowMinBuff
	}
	currentCount := len(s.instances)
	needMore := desiredBuffer > currentCount
	s.mu.Unlock()

	if needMore {
		select {
		case s.more <- desiredBuffer:
		default:
		}
	}

	ctx, cancel := context.WithTimeout(s.ctx, ShadowMaxWait)
	defer cancel()
	select {
	case next := <-s.instances:
		s.available.Add(-1)
		atomic.AddInt64(&globalInstanceCount, -1)
		return next, nil
	case <-ctx.Done():
		return nil, context.Canceled
	}
}

func (s *Shadows) gc() {
	s.mu.Lock()
	now := time.Now()
	elapsed := now.Sub(s.lastCheck).Seconds()
	if elapsed >= 1.0 {
		s.currentRPS = float64(s.requestCount) / elapsed
		s.requestCount = 0
		s.lastCheck = now
	}

	desiredBuffer := int(math.Ceil(s.currentRPS * 1.2))
	if desiredBuffer < 1 {
		desiredBuffer = 0
	}
	s.mu.Unlock()

	shadowInstances := make([]*shadowInstance, 0, desiredBuffer)
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
				if len(shadowInstances) < desiredBuffer {
					shadowInstances = append(shadowInstances, instance)
				} else {
					instance.runtime.Close()
				}
			}
		default:
			return
		}
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
