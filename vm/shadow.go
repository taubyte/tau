package tvm

import (
	"context"
	"sync"
	"time"

	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
)

type shadows struct {
	instances chan *instanceShadow

	more chan struct{}
}

func initShadow(ctx context.Context, s *shadows) {
	s.instances = make(chan *instanceShadow, 1024)
	s.more = make(chan struct{})
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.more:
				var wg sync.WaitGroup
				wg.Add(10)
				for i := 0; i < 10; i++ {
					go func() {
						inst, err := s.newInstance()
						if err == nil {
							s.instances <- inst
						}
					}()
				}
			case <-time.After(5 * time.Minute):
			}
		}
	}()
}

func (s *shadows) get(ctx commonIface.FunctionContext, branch, commit string) (*instanceShadow, error) {
	defer s.keep()

	select {
	case next := <-s.instances:
		return next, nil
	default:
		return s.newInstance(ctx, branch, commit)
	}
}

func (s *shadows) keep() {
	s.more <- struct{}{}
}

func (s *shadows) newInstance(ctx commonIface.FunctionContext, branch, commit string) (*instanceShadow, error) {
	// logic to create an instance -- move from f.inatnciate
	return nil, nil
}
