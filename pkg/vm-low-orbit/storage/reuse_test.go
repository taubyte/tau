package storage

import (
	"testing"

	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
)

// Storage handles have no close. A pooled instance opening its storage on
// every call reuses the one handle instead of adding one per call.
func TestStorageHandleIsReused(t *testing.T) {
	f := &Factory{storages: make(map[uint32]*Storage)}

	var backing storageIface.Storage
	first := f.createStoragePointer(backing, "/st")
	for i := 0; i < 1000; i++ {
		if again := f.openedStorage("/st"); again != first {
			t.Fatalf("open %d made a new handle", i)
		}
	}
	if len(f.storages) != 1 {
		t.Fatalf("%d handles held for one storage", len(f.storages))
	}
	if f.openedStorage("/other") != nil {
		t.Fatal("another storage must not reuse this handle")
	}
}
