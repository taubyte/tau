package counters

import (
	"errors"

	"github.com/taubyte/tau/dream"
)

func FromDream(u *dream.Universe) (*counter, error) {
	if substrate := u.Substrate(); substrate != nil {
		if _counter := substrate.Counter(); _counter != nil {
			if mockCounter, ok := _counter.(*counter); ok {
				return mockCounter, nil
			}
		}
	}

	return nil, errors.New("did you start dream with substrate?")
}
