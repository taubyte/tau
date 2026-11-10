package client

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

// A value that shrinks between the size step and the data step is refused too:
// written short, the guest would read the rest of its buffer as zero bytes.
func TestDataStepRefusesValueThatShrank(t *testing.T) {
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			kv := &memKV{m: map[string][]byte{"k": []byte("hello world"), "other": nil}}
			f := &Factory{Methods: helpers.New(context.Background()), database: map[uint32]*Database{}}
			id := f.createDatabasePointer(memDB{kv: kv}, "/kv").Id
			mod := &memModule{mem: &memMemory{buf: make([]byte, 4096)}}
			mod.mem.buf[keyPtr] = 0x6b // "k"

			if rc := tc.size(f, mod, id); rc != 0 {
				t.Fatalf("size step: errno %d", rc)
			}

			kv.m["k"] = []byte("hi") // another request shrinks the value
			delete(kv.m, "other")    // and the key list

			if rc := tc.data(f, mod, id); errno.Error(rc) != errno.ErrorSizeMismatch {
				t.Fatalf("want ErrorSizeMismatch, got errno %d", rc)
			}

			// Asking again works.
			if rc := tc.size(f, mod, id); rc != 0 {
				t.Fatalf("size step: errno %d", rc)
			}
			n := binary.LittleEndian.Uint32(mod.mem.buf[sizePtr:])
			if rc := tc.data(f, mod, id); rc != 0 {
				t.Fatalf("data step after asking again: errno %d", rc)
			}
			if tc.name == "databaseGet" && string(mod.mem.buf[dataPtr:dataPtr+n]) != "hi" {
				t.Fatalf("got %q", mod.mem.buf[dataPtr:dataPtr+n])
			}
		})
	}
}
