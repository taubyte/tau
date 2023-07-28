package services

import (
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/taubyte/tau/libdream/common"
)

var (
	lastSimplePortAllocated     = 50
	lastSimplePortAllocatedLock sync.Mutex
)

var (
	lastUniversePortShift     int
	lastUniversePortShiftLock sync.Mutex
)

func init() {
	lastUniversePortShift = 9000 + int(rand.NewSource(time.Now().UnixNano()).Int63()%10000)
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
