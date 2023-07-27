package services

import (
	"fmt"
	"net"
	"sync"

	"github.com/taubyte/tau/libdream/common"
)

var (
	lastSimplePortAllocated     = 50
	lastSimplePortAllocatedLock sync.Mutex
)

var (
	lastUniversePortShift     = 9000
	lastUniversePortShiftLock sync.Mutex
)

func init() {
	lastUniversePortShift += 100
}

func LastSimplePortAllocated() int {
	lastSimplePortAllocatedLock.Lock()
	defer lastSimplePortAllocatedLock.Unlock()
	lastSimplePortAllocated++
	return lastSimplePortAllocated
}

func LastUniversePortShift() int {
	lastUniversePortShiftLock.Lock()
	defer lastUniversePortShiftLock.Unlock()
	for {
		l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", common.DefaultHost, lastUniversePortShift))
		if err != nil {
			break
		}
		defer l.Close()

	}
	return lastUniversePortShift + 1
}
