package fixtures

import (
	"context"
	"sync"
	"time"

	"github.com/taubyte/tau/libdream/registry"
	"github.com/taubyte/tau/libdream/services"
)

type Handler func(u *services.Universe) error

var MaxFixtureTimeout = 30 * time.Second

type universe struct {
	*services.Universe
}

func In(u *services.Universe) *universe {
	return &universe{u}
}

func (u *universe) Run(names []string) (err error) {
	var wg sync.WaitGroup
	_, ctxC := context.WithTimeout(u.Context(), MaxFixtureTimeout)
	defer ctxC()

	for _, fixture := range names {
		var handler registry.FixtureHandler
		handler, err = registry.Get(fixture)
		if err != nil {
			return
		}
		wg.Add(1)
		go func() {
			err = handler(u)
			if err != nil {
				ctxC()
			}
		}()
	}
	wg.Wait()
	return err

}
