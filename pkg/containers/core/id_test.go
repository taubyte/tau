package core

import (
	"sync"
	"testing"
)

// Containers started together must never get the same name: docker refuses the second.
func TestNewContainerIDDoesNotCollide(t *testing.T) {
	const workers, each = 16, 20000
	ids := make([][]ContainerID, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range each {
				ids[w] = append(ids[w], NewContainerID())
			}
		}()
	}
	close(start)
	wg.Wait()
	seen := make(map[ContainerID]bool, workers*each)
	for _, l := range ids {
		for _, id := range l {
			if seen[id] {
				t.Fatalf("%s was handed out twice", id)
			}
			seen[id] = true
		}
	}
}
