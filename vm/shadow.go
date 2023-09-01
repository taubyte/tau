package vm

import (
	"context"
	"sync"
	"time"

	"github.com/ipfs/go-log/v2"
)

var logger = log.Logger("substrate.service.vm")

func (d *DFunc) initShadow() {
	d.shadows = shadows{
		instances: make(chan *shadowInstance, InstanceMaxRequests),
		more:      make(chan struct{}, 1),
		parent:    d,
	}
	d.shadows.ctx, d.shadows.ctxC = context.WithCancel(d.ctx)
	ticker := time.NewTicker(ShadowCleanInterval)
	coolDown := time.NewTicker(InstanceErrorCoolDown)
	go func() {
		defer func() {
			d.shadows.ctxC()
			close(d.shadows.instances)
			close(d.shadows.more)

			d.serviceable.Service().Cache().Remove(d.serviceable)
		}()
		var errCount int
		for {
			select {
			case <-coolDown.C:
				if errCount > 0 {
					errCount = errCount / 2
				}
			case <-ticker.C:
				d.shadows.gc()
			case <-d.shadows.ctx.Done():
				return
			case <-d.shadows.more:
				var wg sync.WaitGroup
				for i := 0; i < ShadowBuff; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						if errCount < InstanceMaxError && len(d.shadows.instances) < InstanceMaxRequests {
							shadow, err := d.shadows.newInstance()
							if err != nil {
								logger.Errorf("creating new shadow instance failed with: %s", err.Error())
								errCount++
								return
							}
							select {
							case <-d.shadows.ctx.Done():
								return
							case d.shadows.instances <- shadow:
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
