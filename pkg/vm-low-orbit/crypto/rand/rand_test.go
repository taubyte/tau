package rand

import (
	"context"
	"encoding/binary"
	"runtime"
	"testing"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

// --- minimal mock: cryptoRead needs Read (an aliasing view, like wazy) and
// WriteUint64Le. ---

type mockMemory struct {
	vm.Memory
	buf []byte
}

// Read returns a slice aliasing the backing buffer, matching wazy's real
// Memory.Read, so a caller that fills it writes into guest memory in place.
func (m *mockMemory) Read(offset, count uint32) ([]byte, bool) {
	if uint64(offset)+uint64(count) > uint64(len(m.buf)) {
		return nil, false
	}
	return m.buf[offset : offset+count : offset+count], true
}

func (m *mockMemory) WriteUint64Le(offset uint32, v uint64) bool {
	if uint64(offset)+8 > uint64(len(m.buf)) {
		return false
	}
	binary.LittleEndian.PutUint64(m.buf[offset:], v)
	return true
}

type mockModule struct {
	vm.Module
	mem *mockMemory
}

func (m *mockModule) Memory() vm.Memory { return m.mem }

// cryptoRead is the worst allocate-on-guest-length case: make([]byte, bufLen)
// followed by rand.Read, which touches every page and forces real RSS. A guest
// with a tiny memory must not be able to drive a multi-GiB host allocation; the
// cap must reject before make(). The regression signal is bytes allocated — the
// old code returned the same errno, just after committing ~4 GiB.
func TestCryptoReadRejectsOversizedLength(t *testing.T) {
	f := &Factory{Methods: helpers.New(context.Background())}
	mod := &mockModule{mem: &mockMemory{buf: make([]byte, 4096)}}
	const hugeLen = uint32(0xFFFFFFFF)

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	rc := f.cryptoRead(context.Background(), mod, 0 /*bufPtr*/, hugeLen, 8 /*readPtr*/)
	runtime.ReadMemStats(&after)

	if rc != uint32(errno.ErrorAddressOutOfMemory) {
		t.Fatalf("cryptoRead(bufLen=%#x) = errno %d, want ErrorAddressOutOfMemory (%d)",
			hugeLen, rc, uint32(errno.ErrorAddressOutOfMemory))
	}
	if delta := after.TotalAlloc - before.TotalAlloc; delta > 64<<20 {
		t.Fatalf("cryptoRead allocated %d bytes for an oversized request; the cap did not run before make()", delta)
	}
}
