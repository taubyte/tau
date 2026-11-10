package auth

import (
	"testing"
	"time"
)

func TestRepoListIsKeptPerTokenAndDroppedOnChange(t *testing.T) {
	client, calls := listServer(t, 3, 0)
	client.Token = "token-a"
	t.Cleanup(func() { forgetRepoList(client) })

	first, err := client.ListMyRepos()
	if err != nil || len(first) != 6 || calls.Load() != 3 {
		t.Fatalf("first listing: %d repos, %d calls, %v", len(first), calls.Load(), err)
	}

	// select and push right after an import ask again: GitHub is not
	if again, err := client.ListMyRepos(); err != nil || len(again) != 6 || calls.Load() != 3 {
		t.Fatalf("second listing asked GitHub: %d calls", calls.Load())
	}
	var seen []string
	if err := client.EachRepoPage(func(ids []string) bool { seen = append(seen, ids...); return true }); err != nil || calls.Load() != 3 {
		t.Fatalf("recent projects asked GitHub: %d calls, %v", calls.Load(), err)
	}
	if len(seen) != 6 || seen[0] != "10" || seen[5] != "31" {
		t.Fatalf("cached order: %v", seen)
	}

	// a caller's change drops its copy
	forgetRepoList(client)
	if _, err := client.ListMyRepos(); err != nil || calls.Load() != 6 {
		t.Fatalf("not listed again after a change: %d calls", calls.Load())
	}

	// another caller has its own
	other, otherCalls := listServer(t, 1, 0)
	other.Token = "token-b"
	t.Cleanup(func() { forgetRepoList(other) })
	if _, err := other.ListMyRepos(); err != nil || otherCalls.Load() != 1 {
		t.Fatalf("other caller: %d calls, %v", otherCalls.Load(), err)
	}
}

func TestRepoListExpires(t *testing.T) {
	old := repoListTTL
	repoListTTL = time.Millisecond
	t.Cleanup(func() { repoListTTL = old })

	client, calls := listServer(t, 1, 0)
	client.Token = "token-expiring"
	t.Cleanup(func() { forgetRepoList(client) })
	client.ListMyRepos()
	time.Sleep(5 * time.Millisecond)
	client.ListMyRepos()
	if calls.Load() != 2 {
		t.Fatalf("%d calls", calls.Load())
	}
}

func TestFailedListingIsNotKept(t *testing.T) {
	client, calls := listServer(t, 3, 2)
	client.Token = "token-failing"
	t.Cleanup(func() { forgetRepoList(client) })
	if _, err := client.ListMyRepos(); err == nil {
		t.Fatal("no error")
	}
	before := calls.Load()
	client.ListMyRepos()
	if calls.Load() == before {
		t.Fatal("a failed listing was cached")
	}
}
