package libdream

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

	lastUniversePortShift     = 9000
	lastUniversePortShiftLock sync.Mutex

	maxUniverses     = 100
	portsPerUniverse = 100
)

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
		lastUniversePortShift += int(rand.NewSource(time.Now().UnixNano()).Int63()%int64(maxUniverses)) * portsPerUniverse
		l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", common.DefaultHost, lastUniversePortShift))
		if err == nil {
			l.Close()
			break
		}
	}
	return lastUniversePortShift
}
