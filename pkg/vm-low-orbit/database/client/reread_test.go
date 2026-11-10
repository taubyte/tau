package client

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taubyte/go-sdk/errno"
	dbIface "github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

// countKV counts the reads that reach the store.
type countKV struct {
	*memKV
	gets, lists atomic.Int64
}

func (k *countKV) Get(ctx context.Context, key string) ([]byte, error) {
	k.gets.Add(1)
	return k.memKV.Get(ctx, key)
}

func (k *countKV) List(ctx context.Context, prefix string) ([]string, error) {
	k.lists.Add(1)
	return k.memKV.List(ctx, prefix)
}

type countingDB struct {
	dbIface.Database
	kv *countKV
}

func (d countingDB) KV() dbIface.KV { return d.kv }

type slowDB struct {
	dbIface.Database
	kv *slowKV
}

func (d slowDB) KV() dbIface.KV { return d.kv }

// A database call is a size step and a data step: each is a network call, so
// the data step must use what the size step read, not fetch it again.
func TestDataStepDoesNotReadAgain(t *testing.T) {
	bg := context.Background()
	for _, tc := range []struct {
		name       string
		gets       int64
		size, data func(*Factory, vm.Module, uint32) uint32
	}{
		{"databaseGet", 1, func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseGetSize(bg, m, id, keyPtr, 1, sizePtr)
		}, func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseGet(bg, m, id, keyPtr, 1, dataPtr)
		}},
		{"databaseList", 0, func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseListSize(bg, m, id, keyPtr, 1, sizePtr)
		}, func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseList(bg, m, id, keyPtr, 1, dataPtr)
		}},
		{"databaseListEntries", 2, func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseListEntriesSize(bg, m, id, keyPtr, 1, sizePtr)
		}, func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseListEntries(bg, m, id, keyPtr, 1, dataPtr)
		}},
		{"databaseListMatch", 0, func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseListMatchSize(bg, m, id, keyPtr, 1, keyPtr, 0, sizePtr)
		}, func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseListMatch(bg, m, id, keyPtr, 1, keyPtr, 0, dataPtr)
		}},
		{"databaseListPage", 0, func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseListPageSize(bg, m, id, keyPtr, 1, keyPtr, 0, 100, sizePtr)
		}, func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseListPage(bg, m, id, keyPtr, 1, keyPtr, 0, 100, dataPtr)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kv := &countKV{memKV: &memKV{m: map[string][]byte{"k": []byte("value"), "other": []byte("x")}}}
			f := &Factory{Methods: helpers.New(bg), database: map[uint32]*Database{}}
			id := f.createDatabasePointer(countingDB{kv: kv}, "/kv").Id
			mod := &memModule{mem: &memMemory{buf: make([]byte, 4096)}}
			mod.mem.buf[keyPtr] = 0x6b // "k"

			if rc := tc.size(f, mod, id); rc != 0 {
				t.Fatalf("size step: errno %d", rc)
			}
			before := kv.gets.Load() + kv.lists.Load()
			if rc := tc.data(f, mod, id); rc != 0 {
				t.Fatalf("data step: errno %d", rc)
			}
			if after := kv.gets.Load() + kv.lists.Load(); after != before {
				t.Fatalf("data step read the store %d more times", after-before)
			}
			if got := kv.gets.Load(); got != tc.gets {
				t.Fatalf("%d gets, want %d", got, tc.gets)
			}
		})
	}
}

// A size step that was never fetched must not pin memory on a pooled instance,
// and a value too large to hold is read again, under the size check.
func TestOversizedValueIsReadAgainUnderTheSizeCheck(t *testing.T) {
	bg := context.Background()
	kv := &memKV{m: map[string][]byte{"k": make([]byte, 9<<20)}}
	f := &Factory{Methods: helpers.New(bg), database: map[uint32]*Database{}}
	id := f.createDatabasePointer(memDB{kv: kv}, "/kv").Id
	mod := &memModule{mem: &memMemory{buf: make([]byte, 4096)}}
	mod.mem.buf[keyPtr] = 0x6b // "k"

	if rc := f.databaseGetSize(bg, mod, id, keyPtr, 1, sizePtr); rc != 0 {
		t.Fatalf("size step: errno %d", rc)
	}
	kv.m["k"] = []byte("hi")
	if rc := f.databaseGet(bg, mod, id, keyPtr, 1, dataPtr); errno.Error(rc) != errno.ErrorSizeMismatch {
		t.Fatalf("want ErrorSizeMismatch, got errno %d", rc)
	}
}

// slowKV holds every Get until two are in flight.
type slowKV struct {
	*memKV
	inFlight atomic.Int64
	both     chan struct{}
	once     sync.Once
}

func (k *slowKV) Get(ctx context.Context, key string) ([]byte, error) {
	if k.inFlight.Add(1) >= 2 {
		k.once.Do(func() { close(k.both) })
	}
	defer k.inFlight.Add(-1)
	select {
	case <-k.both:
	case <-time.After(10 * time.Second):
		return nil, context.DeadlineExceeded
	}
	return k.memKV.Get(ctx, key)
}

func TestListEntriesReadsKeysConcurrently(t *testing.T) {
	bg := context.Background()
	kv := &slowKV{memKV: &memKV{m: map[string][]byte{"a": []byte("1"), "b": []byte("2"), "c": []byte("3")}}, both: make(chan struct{})}
	f := &Factory{Methods: helpers.New(bg), database: map[uint32]*Database{}}
	e := f.createDatabasePointer(slowDB{kv: kv}, "/kv")

	encoded, err := f.listEntries(bg, e, "")
	if err != 0 {
		t.Fatalf("errno %s", err)
	}
	if len(encoded) == 0 {
		t.Fatal("no entries")
	}
}
