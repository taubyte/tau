package storage

import (
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/taubyte/go-sdk/errno"
	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
	"github.com/taubyte/tau/core/vm"
)

type growingStorage struct {
	storageIface.Storage
	n int
}

func (s *growingStorage) list() []string                                 { return strings.Split(strings.Repeat("f,", s.n), ",") }
func (s *growingStorage) List(context.Context, string) ([]string, error) { return s.list(), nil }
func (s *growingStorage) ListVersions(context.Context, string) ([]string, error) {
	return s.list(), nil
}
func (s *growingStorage) Used(context.Context) (int, error) { return s.n, nil }

// A value that grows between the size step and the data step must not be
// written past the buffer the guest sized from the first step.
func TestDataStepRefusesValueThatGrew(t *testing.T) {
	const sizePtr, namePtr, dataPtr = 0, 16, 64
	ctx := context.Background()
	for _, tc := range []struct {
		name       string
		size, data func(*Factory, vm.Module, uint32) uint32
	}{
		{"storageListFiles", func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.storageListFilesSize(ctx, m, id, sizePtr)
		}, func(f *Factory, m vm.Module, id uint32) uint32 { return f.storageListFiles(ctx, m, id, dataPtr) }},
		{"storageListVersions", func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.storageListVersionsSize(ctx, m, id, namePtr, 1, sizePtr)
		}, func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.storageListVersions(ctx, m, id, namePtr, 1, dataPtr)
		}},
		{"storageUsed", func(f *Factory, m vm.Module, id uint32) uint32 {
			return f.storageUsedSize(ctx, m, id, sizePtr)
		}, func(f *Factory, m vm.Module, id uint32) uint32 { return f.storageUsed(ctx, m, id, dataPtr) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &growingStorage{n: 9}
			f := newTestFactory("P")
			id := f.createStoragePointer(s, "/s").id
			mod := newModule()
			mod.mem.buf[namePtr] = 0x66

			if rc := tc.size(f, mod, id); rc != 0 {
				t.Fatalf("size step: errno %d", rc)
			}
			n := binary.LittleEndian.Uint32(mod.mem.buf[sizePtr:])
			for i := dataPtr + n; i < uint32(len(mod.mem.buf)); i++ {
				mod.mem.buf[i] = 0xAA
			}

			s.n = 1000 // another request grows the value

			rc := tc.data(f, mod, id)
			for i := dataPtr + n; i < uint32(len(mod.mem.buf)); i++ {
				if mod.mem.buf[i] != 0xAA {
					t.Fatalf("byte %d past the %d-byte buffer was overwritten", i-dataPtr-n, n)
				}
			}
			if errno.Error(rc) != errno.ErrorSizeMismatch {
				t.Fatalf("want ErrorSizeMismatch, got errno %d", rc)
			}

			if rc := tc.size(f, mod, id); rc != 0 {
				t.Fatalf("size step: errno %d", rc)
			}
			if rc := tc.data(f, mod, id); rc != 0 {
				t.Fatalf("data step after asking again: errno %d", rc)
			}
		})
	}
}
