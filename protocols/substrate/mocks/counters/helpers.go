package counters

import (
	"errors"

	"github.com/taubyte/tau/libdream"
)

func FromDreamland(u *libdream.Universe) (*counter, error) {
	if substrate := u.Substrate(); substrate != nil {
		if _counter := substrate.Counter(); _counter != nil {
			if mockCounter, ok := _counter.(*counter); ok {
				return mockCounter, nil
			}
		}
	}

	return nil, errors.New("did you start dreamland with substrate?")
}
