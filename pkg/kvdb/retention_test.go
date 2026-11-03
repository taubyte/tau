package kvdb

import (
	"testing"
	"time"

	logging "github.com/ipfs/go-log/v2"

	"github.com/taubyte/tau/pkg/fleet"
)

func ttlOf(t *testing.T, db *kvDatabase) time.Duration {
	t.Helper()
	return time.Duration(db.datastore.retention()) * time.Millisecond
}

func TestTheFleetDocumentSaysT(t *testing.T) {
	got, err := RetentionOf(nil, false)
	if err != nil || got != 0 {
		t.Fatalf("an unpublished document is the default: %s %v", got, err)
	}
	doc, err := fleet.Encode(fleet.Fleet{Retention: fleet.Duration(48 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if got, err = RetentionOf(doc, true); err != nil || got != 48*time.Hour {
		t.Fatalf("a published 48h read as %s %v", got, err)
	}
	if _, err := RetentionOf([]byte("{not json"), true); err == nil {
		t.Fatal("a corrupt document read as a T")
	}
	if DefaultRetention() != DefaultOptions().Retention {
		t.Fatal("the default is not the options' default")
	}
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

func TestAFleetRetentionADatabaseCannotHoldFallsBack(t *testing.T) {
	// One hour between rebroadcasts makes the margin far above what 8h holds.
	db, err := open(mockPeer(t), "fallback/a", WithLogger(logging.Logger("test")),
		WithRebroadcast(time.Hour), WithFleetRetention(8*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := ttlOf(t, db); got != DefaultOptions().Retention {
		t.Fatalf("a database that cannot hold the fleet's T writes under %s", got)
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
	if got := ttlOf(t, db); got != DefaultOptions().Retention {
		t.Fatalf("asking changed T to %s", got)
	}
	if err := db.SetRetention(72 * time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := ttlOf(t, db); got != 72*time.Hour {
		t.Fatalf("T is %s, not 72h", got)
	}
}
