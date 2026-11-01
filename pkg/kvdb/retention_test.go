package kvdb

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/pkg/fleet"
)

type fakeFleet struct {
	mu        sync.Mutex
	doc       []byte
	published bool
	err       error
}

func (f *fakeFleet) read(context.Context) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.doc, f.published, f.err
}

func (f *fakeFleet) publish(t *testing.T, d fleet.Fleet) {
	t.Helper()
	doc, err := fleet.Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.doc, f.published, f.err = doc, true, nil
}

func ttlOf(t *testing.T, db any) time.Duration {
	t.Helper()
	return time.Duration(db.(*kvDatabase).datastore.retention()) * time.Millisecond
}

func TestTheFleetSetsRetention(t *testing.T) {
	logger := logging.Logger("test")
	src := &fakeFleet{}
	src.publish(t, fleet.Fleet{Retention: fleet.Duration(48 * time.Hour)})
	f := New(mockPeer(t), WithFleet(src.read, time.Hour)).(*factory)

	db, err := f.New(logger, "retention/a", 5)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := ttlOf(t, db); got != 48*time.Hour {
		t.Fatalf("a database opened under a published T of 48h writes under %s", got)
	}

	src.publish(t, fleet.Fleet{Retention: fleet.Duration(72 * time.Hour)})
	f.applyFleet(t.Context(), src.read)
	if got := ttlOf(t, db); got != 72*time.Hour {
		t.Fatalf("an open database did not follow the document: %s", got)
	}

	src.mu.Lock()
	src.err = errors.New("unreachable")
	src.mu.Unlock()
	f.applyFleet(t.Context(), src.read)
	if got := ttlOf(t, db); got != 72*time.Hour {
		t.Fatalf("an unreachable register changed T to %s", got)
	}

	src.mu.Lock()
	src.doc, src.err = []byte("{not json"), nil
	src.mu.Unlock()
	f.applyFleet(t.Context(), src.read)
	if got := ttlOf(t, db); got != 72*time.Hour {
		t.Fatalf("a corrupt document changed T to %s", got)
	}

	src.publish(t, fleet.Fleet{Retention: fleet.Duration(time.Minute)})
	f.applyFleet(t.Context(), src.read)
	if got := ttlOf(t, db); got != 72*time.Hour {
		t.Fatalf("a T no database can hold was applied: %s", got)
	}

	src.mu.Lock()
	src.doc, src.published = nil, false
	src.mu.Unlock()
	f.applyFleet(t.Context(), src.read)
	if got := ttlOf(t, db); got != DefaultOptions().Retention {
		t.Fatalf("a cloud that says nothing should mean the default, not %s", got)
	}

	later, err := f.New(logger, "retention/b", 5)
	if err != nil {
		t.Fatal(err)
	}
	defer later.Close()
	if got := ttlOf(t, later); got != DefaultOptions().Retention {
		t.Fatalf("a database opened after the document went quiet writes under %s", got)
	}
}

func TestAFixedRetentionIsKeptAndChecked(t *testing.T) {
	logger := logging.Logger("test")
	f := New(mockPeer(t), WithRetention(96*time.Hour)).(*factory)
	db, err := f.New(logger, "fixed/a", 5)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := ttlOf(t, db); got != 96*time.Hour {
		t.Fatalf("a fixed T of 96h became %s", got)
	}
	if err := f.setRetention(time.Hour); err != nil || ttlOf(t, db) != 96*time.Hour {
		t.Fatal("a fixed T was moved")
	}

	short := New(mockPeer(t), WithRetention(time.Minute))
	if _, err := short.New(logger, "fixed/b", 5); err == nil {
		t.Fatal("a database opened with a T under ten margins")
	}
}

func TestAFleetRetentionADatabaseCannotHoldFallsBack(t *testing.T) {
	logger := logging.Logger("test")
	src := &fakeFleet{}
	src.publish(t, fleet.Fleet{Retention: fleet.Duration(8 * time.Hour)})
	f := New(mockPeer(t), WithFleet(src.read, time.Hour)).(*factory)
	// One hour between rebroadcasts makes the margin far above what 8h holds.
	db, err := f.New(logger, "fallback/a", 3600)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := ttlOf(t, db); got != DefaultOptions().Retention {
		t.Fatalf("a database that cannot hold the fleet's T writes under %s", got)
	}
}
