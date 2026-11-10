package client

import (
	"testing"

	dbIface "github.com/taubyte/tau/core/services/substrate/components/database"
)

type closingDB struct {
	dbIface.Database
	closed *int
}

func (d closingDB) Close() { *d.closed++ }

// A pooled instance opening its database on every call reuses one handle
// instead of adding one per call, and the handle is closed only when every
// open of it has been closed.
func TestDatabaseHandleIsReusedAndRefCounted(t *testing.T) {
	f := &Factory{database: make(map[uint32]*Database)}
	closed := 0

	first := f.createDatabasePointer(closingDB{closed: &closed}, "/kv")
	for i := 0; i < 1000; i++ {
		if again := f.openedDatabase("/kv"); again != first {
			t.Fatalf("open %d made a new handle", i)
		}
	}
	if len(f.database) != 1 {
		t.Fatalf("%d handles held for one database", len(f.database))
	}

	if f.openedDatabase("/other") != nil {
		t.Fatal("another database must not reuse this handle")
	}

	for i := 0; i < 1000; i++ {
		if f.releaseDatabase(first) {
			t.Fatalf("closed at release %d of 1001", i+1)
		}
	}
	if !f.releaseDatabase(first) || closed != 1 {
		t.Fatalf("last close must close the handle once (closed=%d)", closed)
	}
	if len(f.database) != 0 || f.openedDatabase("/kv") != nil {
		t.Fatal("a closed handle must be forgotten")
	}
}
