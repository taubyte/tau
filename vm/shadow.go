package tvm

import (
	"context"
	"sync"
	"time"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
)

type shadows struct {
	instances chan *instanceShadow
	gcLock    sync.RWMutex

	more chan struct{}
}

func initShadow(ctx context.Context, s *shadows) {
	s.instances = make(chan *instanceShadow, 1024)
	s.more = make(chan struct{})
	go func() {
		errCount := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.more:
				if errCount < 10 {
					var wg sync.WaitGroup
					for i := 0; i < 10; i++ {
						if errCount > 10 {
							break
						}
						wg.Add(1)
						go func() {
							defer wg.Done()
							inst, err := s.newInstance()
							if err == nil {
								s.instances <- inst
							} else {
								// log the error
								errCount++
							}
						}()
					}
					wg.Wait()
				}
				// cool off
				time.Sleep(time.Second)

			case <-time.After(5 * time.Minute):
				// cleanup
				s.gcLock.Lock()
				insts := s.instances
				close(insts)
				s.instances = make(chan *instanceShadow, cap(insts))
				s.gcLock.Unlock()

				for inst := range insts {
					// check ttl of inst
					s.instances <- inst
				}

				errCount /= 2
			case <-time.After(10 * time.Minute):
				errCount = 0
			}
		}
	}()
}

func (s *shadows) get(ctx commonIface.FunctionContext, branch, commit string) (*instanceShadow, error) {
	s.gcLock.RLock()
	defer s.gcLock.RUnlock()

	select {
	case next := <-s.instances:
		defer s.keep()
		return next, nil
	default:
		i, err := s.newInstance(ctx, branch, commit)
		if err == nil {
			s.keep()
		}
		return i, err
	}
}

func (s *shadows) keep() {
	select {
	case s.more <- struct{}{}:
	default:
	}
}

func (s *shadows) newInstance(ctx commonIface.FunctionContext, branch, commit string) (*instanceShadow, error) {
	// logic to create an instance -- move from f.inatnciate
	return nil, nil
}
