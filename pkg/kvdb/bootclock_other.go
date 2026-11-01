//go:build !linux

package kvdb

import "time"

// Without a boot clock that counts suspend, the process's monotonic clock.
func bootClock() time.Duration { return time.Since(processStart) }
