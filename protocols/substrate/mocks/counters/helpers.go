package counters

import (
	"errors"

	commonDreamland "github.com/taubyte/tau/libdream/common"
)

func FromDreamland(u commonDreamland.Universe) (*counter, error) {
	if substrate := u.Substrate(); substrate != nil {
		if _counter := substrate.Counter(); _counter != nil {
			if mockCounter, ok := _counter.(*counter); ok {
				return mockCounter, nil
			}
		}
	}

	return nil, errors.New("did you start dreamland with substrate?")
}
