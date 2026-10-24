//go:build !linux

package kvdb

import "time"

// bootClock is the monotonic clock since this process started where the
// platform has no boot clock that counts suspend.
func bootClock() time.Duration { return time.Since(processStart) }
