package runtime

import (
	"sync/atomic"
	"time"
)

func (f *Function) averageDuration(duration *atomic.Int64, count *atomic.Uint64) time.Duration {
	if n := count.Load(); n > 0 {
		return time.Duration(duration.Load() / int64(n))
	}

	return 0
}

func (f *Function) ColdStart() time.Duration {
	return f.averageDuration(f.totalColdStart, f.coldStarts)
}

func (f *Function) MemoryMax() uint64 {
	return uint64(f.maxMemory.Load())
}

func (f *Function) CallTime() time.Duration {
	return f.averageDuration(f.totalCallTime, f.calls)
}
