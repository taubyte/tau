package tvm

import (
	"time"
)

func (s *shadows) get() (*instanceShadow, error) {
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

func (s *shadows) cleanUp() {
	close(s.instances)
	close(s.more)

}

func (s *shadows) keep() {
	select {
	case s.more <- struct{}{}:
	default:
	}
}

func (s *shadows) newInstance() (*instanceShadow, error) {
	runtime, pluginApi, err := s.parent.instantiate()
	if err != nil {
		return nil, err
	}

	return &instanceShadow{
		creation:  time.Now(),
		runtime:   runtime,
		pluginApi: pluginApi,
	}, nil
}
