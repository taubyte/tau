package globals

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
func (k *memKV) List(context.Context, string) ([]string, error) {
	keys := []string{}
	for key := range k.m {
		keys = append(keys, key)
	}
	return keys, nil
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

func (m *memMemory) Read(o, n uint32) ([]byte, bool) { return m.buf[o : o+n], true }
func (m *memMemory) Write(o uint32, v []byte) bool {
	if int(o)+len(v) > len(m.buf) {
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

// A global that grows between the size step and the data step must not be
// written past the buffer the guest sized from the first step.
func TestDataStepRefusesValueThatGrew(t *testing.T) {
	const sizePtr, namePtr, dataPtr = 0, 16, 64
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		size func(*Factory, vm.Module) uint32
	}{
		{"getGlobalValueSize", func(f *Factory, m vm.Module) uint32 {
			return f.getGlobalValueSize(ctx, m, namePtr, 1, 0, 0, sizePtr)
		}},
		{"getOrCreateGlobalValueSize", func(f *Factory, m vm.Module) uint32 {
			return f.getOrCreateGlobalValueSize(ctx, m, namePtr, 1, 0, 0, sizePtr)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kv := &memKV{m: map[string][]byte{"/g": []byte("small")}}
			f := &Factory{Methods: helpers.New(ctx), databaseInstance: memDB{kv: kv}}
			mod := &memModule{mem: &memMemory{buf: make([]byte, 4096)}}
			mod.mem.buf[namePtr] = 0x67 // "g"
			data := func() uint32 { return f.getGlobalValue(ctx, mod, namePtr, 1, 0, 0, dataPtr) }

			if rc := tc.size(f, mod); rc != 0 {
				t.Fatalf("size step: errno %d", rc)
			}
			n := binary.LittleEndian.Uint32(mod.mem.buf[sizePtr:])
			for i := dataPtr + n; i < uint32(len(mod.mem.buf)); i++ {
				mod.mem.buf[i] = 0xAA
			}

			kv.m["/g"] = make([]byte, 512) // another request grows the value

			rc := data()
			for i := dataPtr + n; i < uint32(len(mod.mem.buf)); i++ {
				if mod.mem.buf[i] != 0xAA {
					t.Fatalf("byte %d past the %d-byte buffer was overwritten", i-dataPtr-n, n)
				}
			}
			if errno.Error(rc) != errno.ErrorSizeMismatch {
				t.Fatalf("want ErrorSizeMismatch, got errno %d", rc)
			}

			if rc := tc.size(f, mod); rc != 0 {
				t.Fatalf("size step: errno %d", rc)
			}
			if rc := data(); rc != 0 {
				t.Fatalf("data step after asking again: errno %d", rc)
			}
		})
	}
}
