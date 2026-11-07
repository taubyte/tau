package client

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/taubyte/go-sdk/errno"
	dbIface "github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

type memKV struct {
	dbIface.KV
	m map[string][]byte
}

func (k *memKV) Get(_ context.Context, key string) ([]byte, error) { return k.m[key], nil }
func (k *memKV) List(_ context.Context, prefix string) ([]string, error) {
	keys := []string{}
	for key := range k.m {
		keys = append(keys, key)
	}
	return keys, nil
}

func (k *memKV) ListRegEx(ctx context.Context, prefix string, _ ...string) ([]string, error) {
	return k.List(ctx, prefix)
}

type memDB struct {
	dbIface.Database
	kv *memKV
}

func (d memDB) KV() dbIface.KV { return d.kv }

type memMemory struct {
	vm.Memory
	buf []byte
}

func (m *memMemory) Read(o, n uint32) ([]byte, bool) {
	if uint64(o)+uint64(n) > uint64(len(m.buf)) {
		return nil, false
	}
	return m.buf[o : o+n], true
}

func (m *memMemory) Write(o uint32, v []byte) bool {
	if uint64(o)+uint64(len(v)) > uint64(len(m.buf)) {
		return false
	}
	copy(m.buf[o:], v)
	return true
}

func (m *memMemory) WriteUint32Le(o, v uint32) bool {
	binary.LittleEndian.PutUint32(m.buf[o:], v)
	return true
}

type memModule struct {
	vm.Module
	mem *memMemory
}

func (m *memModule) Memory() vm.Memory { return m.mem }

const (
	sizePtr = 0
	keyPtr  = 16
	dataPtr = 64
)

// A value that grows between the size step and the data step must not be
// written past the buffer the guest sized from the first step.
func TestDataStepRefusesValueThatGrew(t *testing.T) {
	for _, tc := range []struct {
		name       string
		size, data func(*Factory, vm.Module, uint32) uint32
	}{
		{"databaseGet", func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseGetSize(context.Background(), m, id, keyPtr, 1, sizePtr)
		}, func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseGet(context.Background(), m, id, keyPtr, 1, dataPtr)
		}},
		{"databaseList", func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseListSize(context.Background(), m, id, keyPtr, 1, sizePtr)
		}, func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseList(context.Background(), m, id, keyPtr, 1, dataPtr)
		}},
		{"databaseListEntries", func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseListEntriesSize(context.Background(), m, id, keyPtr, 1, sizePtr)
		}, func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseListEntries(context.Background(), m, id, keyPtr, 1, dataPtr)
		}},
		{"databaseListMatch", func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseListMatchSize(context.Background(), m, id, keyPtr, 1, keyPtr, 0, sizePtr)
		}, func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseListMatch(context.Background(), m, id, keyPtr, 1, keyPtr, 0, dataPtr)
		}},
		{"databaseListPage", func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseListPageSize(context.Background(), m, id, keyPtr, 1, keyPtr, 0, 100, sizePtr)
		}, func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.databaseListPage(context.Background(), m, id, keyPtr, 1, keyPtr, 0, 100, dataPtr)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kv := &memKV{m: map[string][]byte{"k": []byte("small")}}
			f := &Factory{Methods: helpers.New(context.Background()), database: map[uint32]*Database{}}
			id := f.createDatabasePointer(memDB{kv: kv}, "/kv").Id
			mod := &memModule{mem: &memMemory{buf: make([]byte, 4096)}}
			mod.mem.buf[keyPtr] = 0x6b // "k"

			if rc := tc.size(f, mod, id); rc != 0 {
				t.Fatalf("size step: errno %d", rc)
			}
			n := binary.LittleEndian.Uint32(mod.mem.buf[sizePtr:])
			for i := dataPtr + n; i < uint32(len(mod.mem.buf)); i++ {
				mod.mem.buf[i] = 0xAA
			}

			kv.m["k"] = make([]byte, 512)                 // another request grows the value
			kv.m["another-key-that-grows-the-list"] = nil // and the key list

			rc := tc.data(f, mod, id)
			for i := dataPtr + n; i < uint32(len(mod.mem.buf)); i++ {
				if mod.mem.buf[i] != 0xAA {
					t.Fatalf("byte %d past the %d-byte buffer was overwritten", i-dataPtr-n, n)
				}
			}
			if errno.Error(rc) != errno.ErrorSizeMismatch {
				t.Fatalf("want ErrorSizeMismatch, got errno %d", rc)
			}

			// Asking again works.
			if rc := tc.size(f, mod, id); rc != 0 {
				t.Fatalf("size step: errno %d", rc)
			}
			if rc := tc.data(f, mod, id); rc != 0 {
				t.Fatalf("data step after asking again: errno %d", rc)
			}
		})
	}
}
