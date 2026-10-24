package kvdb

import "time"

var processStart = time.Now()

// millis is t in milliseconds since the epoch; a clock set before it reads 0.
func millis(t time.Time) uint64 {
	if ms := t.UnixMilli(); ms > 0 {
		return uint64(ms)
	}
	return 0
}

func durationMillis(d time.Duration) uint64 {
	if d <= 0 {
		return 0
	}
	return uint64(d / time.Millisecond)
}
