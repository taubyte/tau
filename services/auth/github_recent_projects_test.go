package auth

import (
	"testing"
)

// EachRepoPage reads pages newest first and stops when told to.
func TestEachRepoPageStops(t *testing.T) {
	client, calls := listServer(t, 7, 0)
	var seen []string
	err := client.EachRepoPage(func(ids []string) bool {
		seen = append(seen, ids...)
		return len(seen) < 3
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(seen) != 4 {
		t.Fatalf("%d calls, %d ids", calls.Load(), len(seen))
	}

	if err := func() error {
		failing, _ := listServer(t, 7, 2)
		return failing.EachRepoPage(func([]string) bool { return true })
	}(); err == nil {
		t.Fatal("a failed page was not an error")
	}
}
