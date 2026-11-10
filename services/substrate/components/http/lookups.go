package http

import (
	"sync"
	"sync/atomic"
	"time"
)

var (
	lookups   sync.Map // id -> start time, for requests inside Lookup
	lookupSeq atomic.Uint64
)

// trackLookup marks a request as being in the lookup step until the returned
// func is called. Provisioning and the function's execution are not tracked, so
// a long-running function can never look stuck.
func trackLookup() func() {
	id := lookupSeq.Add(1)
	lookups.Store(id, time.Now())
	return func() { lookups.Delete(id) }
}

// StuckLookups counts requests that have been in the lookup step longer than d.
func StuckLookups(d time.Duration) (n int) {
	lookups.Range(func(_, v any) bool {
		if time.Since(v.(time.Time)) > d {
			n++
		}
		return true
	})
	return n
}
