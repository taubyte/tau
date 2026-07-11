package hoarder

import (
	"sync"
	"testing"

	cr "github.com/taubyte/tau/p2p/streams/command/response"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
)

// A remoteKV handle is shared across goroutines in production (substrate caches
// database/storage handles and concurrent function invocations reuse them). The
// attempt() retry bound reads the replica count while reset/adoptRedirect/
// pinServer replace the slice under r.mu, so that read must be synchronized too.
// This hammers all of them on one handle; under -race it flags any unsynchronized
// access — it fails against a raw len(r.replicas) bound and passes with the
// locked replicaCount() accessor.
func TestRemoteKV_ConcurrentReplicaMutation_Race(t *testing.T) {
	const (
		valid1 = "12D3KooWHS36LKeJVFCPb6g3i8VkZsMcMkwrV7Sg8Hh2pcP2LfHP"
		valid2 = "12D3KooWGoShXGTy7asGYauUA1kLuLmHDovX5omu2Fjsy2NPFSq7"
	)
	redirect := cr.Response{hoarderSpecs.BodyPeers: []string{valid1, valid2}}

	r := &remoteKV{}

	const (
		workers = 8
		iters   = 2000
	)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				switch (w + i) % 5 {
				case 0:
					// The bound read the retry loop performs each iteration.
					_ = r.replicaCount()
				case 1:
					r.adoptRedirect(redirect)
				case 2:
					r.pinServer(valid1)
				case 3:
					r.failover()
				case 4:
					r.reset()
				}
			}
		}(w)
	}
	wg.Wait()
}
