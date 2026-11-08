package tns

import (
	"testing"
	"time"

	"github.com/taubyte/tau/clients/p2p/tns/common"
)

// Keys cached in the same burst must not share one expiry, or they all miss together.
func TestInitialLifetimeIsSpreadOverTwentyPercent(t *testing.T) {
	lifetime := common.ClientKeyCacheLifetime
	seen := map[time.Duration]bool{}
	for i := 0; i < 200; i++ {
		d := initialLifetime()
		if d < lifetime || d > lifetime+lifetime/5 {
			t.Fatalf("lifetime %v outside [%v, %v]", d, lifetime, lifetime+lifetime/5)
		}
		seen[d] = true
	}
	if len(seen) < 2 {
		t.Fatal("every subscription got the same lifetime")
	}
}
