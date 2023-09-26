package vm

import (
	"sync/atomic"
	"time"
)

func (f *Function) averageDuration(duration *atomic.Int64, count *atomic.Int64) time.Duration {
	if n := count.Load(); n > 0 {
		return time.Duration(duration.Load() / n)
	}

	return 0
}

func (f *Function) ColdStart() time.Duration {
	return f.averageDuration(f.totalColdStart, f.coldStarts)
}

func (f *Function) MemoryMax() int64 {
	return f.maxMemory.Load()
}

func (f *Function) CallTime() time.Duration {
	return f.averageDuration(f.totalCallTime, f.calls)
}
