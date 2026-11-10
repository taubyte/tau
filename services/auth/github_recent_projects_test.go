package auth

import (
	"fmt"
	"testing"
)

// EachRepoPage reads pages newest first and stops when told to.
func TestEachRepoPageStops(t *testing.T) {
	client, calls := listServer(t, 7, 0)
	var seen []string
	err := client.EachRepoPage(func(ids []string) bool {
		seen = append(seen, ids...)
		return len(seen) < 2
	})
	if err != nil {
		t.Fatal(err)
	}
	// page 1 alone answers it; nothing is fetched ahead of a stop
	if calls.Load() != 1 || len(seen) != 2 {
		t.Fatalf("%d calls, %d ids", calls.Load(), len(seen))
	}

	if err := func() error {
		failing, _ := listServer(t, 7, 2)
		return failing.EachRepoPage(func([]string) bool { return true })
	}(); err == nil {
		t.Fatal("a failed page was not an error")
	}
}

func TestEachRepoPageReadsAllInOrder(t *testing.T) {
	client, calls := listServer(t, 7, 0)
	var seen []string
	if err := client.EachRepoPage(func(ids []string) bool { seen = append(seen, ids...); return true }); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 14 || calls.Load() != 7 {
		t.Fatalf("%d ids in %d calls", len(seen), calls.Load())
	}
	for i := 1; i < 7; i++ {
		if seen[2*i] != fmt.Sprint((i+1)*10) {
			t.Fatalf("pages out of order: %v", seen)
		}
	}
}
