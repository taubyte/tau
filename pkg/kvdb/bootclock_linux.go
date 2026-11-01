package kvdb

import (
	"time"

	"golang.org/x/sys/unix"
)

// CLOCK_BOOTTIME counts time suspended, so an absence spent asleep counts.
func bootClock() time.Duration {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts); err != nil {
		return time.Since(processStart)
	}
	return time.Duration(ts.Nano())
}
