package kvdb

import (
	"time"

	"golang.org/x/sys/unix"
)

// bootClock is CLOCK_BOOTTIME: it counts time suspended too, so an absence
// spent asleep is an absence.
func bootClock() time.Duration {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts); err != nil {
		return time.Since(processStart)
	}
	return time.Duration(ts.Nano())
}
