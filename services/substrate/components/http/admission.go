package http

import (
	"errors"
	"os"
	"runtime"
	"strconv"

	"github.com/shirou/gopsutil/v4/mem"
	"github.com/taubyte/tau/pkg/platformerr"
)

// maxInFlight bounds the node's concurrent function requests. Requests spend
// most of their time waiting on the database or the network, so more than a
// few dozen per CPU only queue up behind each other and push every request's
// latency (and the node's memory) up. Past the limit a fast 503 beats a slow
// answer. 64 per CPU matches each function's own queue; 8 MiB per request keeps
// the total inside RAM. 2 CPUs and 2 GB give min(128, 256) = 128.
func maxInFlight(cpus int, memBytes uint64) int {
	return max(1, min(cpus*64, int(memBytes>>23)))
}

func newAdmission() chan struct{} {
	total := uint64(2 << 30) // shortcut: assume 2 GB if the OS will not say
	if v, err := mem.VirtualMemory(); err == nil {
		total = v.Total
	}
	// shortcut: env override for finding the limit under load, remove once maxInFlight is settled.
	if n, err := strconv.Atoi(os.Getenv("TAU_MAX_INFLIGHT")); err == nil && n > 0 {
		return make(chan struct{}, n)
	}
	return make(chan struct{}, maxInFlight(runtime.NumCPU(), total))
}

// admitted runs run holding an in-flight slot, or fails at once with NodeBusy
// when none is free. The slot is freed on return, panic and client disconnect
// alike. A nil channel (no limit set) admits everything.
func admitted(slots chan struct{}, run func() error) error {
	if slots == nil {
		return run()
	}
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
		return run()
	default:
		return platformerr.New(platformerr.NodeBusy, errors.New("node in-flight request limit reached"))
	}
}
