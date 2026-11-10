package kvdb

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	logging "github.com/ipfs/go-log/v2"
)

func ttlOf(t *testing.T, db *kvDatabase) time.Duration {
	t.Helper()
	return time.Duration(db.datastore.retention()) * time.Millisecond
}

func TestAFixedRetentionIsKeptAndChecked(t *testing.T) {
	logger := logging.Logger("test")
	db, err := open(mockPeer(t), "fixed/a", WithLogger(logger), WithRetention(96*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := ttlOf(t, db); got != 96*time.Hour {
		t.Fatalf("a fixed T of 96h became %s", got)
	}
	if _, err := Open(mockPeer(t), "fixed/b", WithLogger(logger), WithRetention(time.Minute)); err == nil {
		t.Fatal("a database opened with a T under ten margins")
	}
}

func TestARegionRetentionADatabaseCannotHoldFallsBack(t *testing.T) {
	// One hour between rebroadcasts makes the margin far above what 8h holds.
	db, err := open(mockPeer(t), "fallback/a", WithLogger(logging.Logger("test")),
		WithRebroadcast(time.Hour), WithRegionRetention(8*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := ttlOf(t, db); got != testOptions().Retention {
		t.Fatalf("a database that cannot hold the region's T writes under %s", got)
	}
}

func TestAnOpenDatabaseMovesToANewT(t *testing.T) {
	db, err := open(mockPeer(t), "moves/a", WithLogger(logging.Logger("test")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.CanRetain(time.Minute); err == nil {
		t.Fatal("a T under ten margins can be held")
	}
	if err := db.CanRetain(72 * time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := ttlOf(t, db); got != testOptions().Retention {
		t.Fatalf("asking changed T to %s", got)
	}
	if err := db.SetRetention(72 * time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := ttlOf(t, db); got != 72*time.Hour {
		t.Fatalf("T is %s, not 72h", got)
	}
}

func TestADatabaseFollowsTheRegionsT(t *testing.T) {
	var (
		mu   sync.Mutex
		says = 72 * time.Hour
		err  error
	)
	read := func(context.Context) (time.Duration, error) {
		mu.Lock()
		defer mu.Unlock()
		return says, err
	}
	db, openErr := open(mockPeer(t), "follows/a", WithLogger(logging.Logger("test")), WithRetentionFrom(read, time.Millisecond))
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer db.Close()
	waitFor(t, func() bool { return ttlOf(t, db) == 72*time.Hour })

	// A T IT CANNOT HOLD, or a read that fails, leaves it where it was.
	mu.Lock()
	says = time.Minute
	mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	if got := ttlOf(t, db); got != 72*time.Hour {
		t.Fatalf("moved to %s, which it cannot hold", got)
	}
	mu.Lock()
	says, err = 0, errors.New("no register")
	mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	if got := ttlOf(t, db); got != 72*time.Hour {
		t.Fatalf("a failed read moved it to %s", got)
	}

	// UNPUBLISHED IS THE DEFAULT.
	mu.Lock()
	err = nil
	mu.Unlock()
	waitFor(t, func() bool { return ttlOf(t, db) == DefaultRetention() })
}

func waitFor(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("never reached")
		}
		time.Sleep(time.Millisecond)
	}
}
