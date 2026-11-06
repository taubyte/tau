package auto

import (
	"testing"
	"time"
)

// A refused domain is refused for its TTL, however often it is asked about:
// reads used to restart the TTL, so a client retrying a domain checked before
// its project was published kept the refusal alive indefinitely.
func TestValidationCacheReadsDoNotExtend(t *testing.T) {
	ttl := 200 * time.Millisecond
	cache := newValidationCache(ttl)
	cache.Set("pending.example.com", true, ttl)

	deadline := time.Now().Add(3 * ttl)
	for time.Now().Before(deadline) {
		if item := cache.Get("pending.example.com"); item == nil {
			return
		}
		time.Sleep(ttl / 8)
	}
	t.Fatalf("an entry read every %s was still cached after %s", ttl/8, 3*ttl)
}
