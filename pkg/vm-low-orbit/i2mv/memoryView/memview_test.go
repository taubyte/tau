package memoryView

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

// --- minimal mock: only Read / Write / WriteUint32Le are exercised ---

type mockMemory struct {
	vm.Memory
	buf []byte
}

func (m *mockMemory) Read(offset, count uint32) ([]byte, bool) {
	if uint64(offset)+uint64(count) > uint64(len(m.buf)) {
		return nil, false
	}
	out := make([]byte, count)
	copy(out, m.buf[offset:offset+count])
	return out, true
}

func (m *mockMemory) Write(offset uint32, v []byte) bool {
	if uint64(offset)+uint64(len(v)) > uint64(len(m.buf)) {
		return false
	}
	copy(m.buf[offset:], v)
	return true
}

func (m *mockMemory) WriteUint32Le(offset, v uint32) bool {
	if uint64(offset)+4 > uint64(len(m.buf)) {
		return false
	}
	binary.LittleEndian.PutUint32(m.buf[offset:], v)
	return true
}

type mockModule struct {
	vm.Module
	mem *mockMemory
}

func (m *mockModule) Memory() vm.Memory { return m.mem }

func newFactory() *Factory {
	return &Factory{
		Methods:     helpers.New(context.Background()),
		memoryViews: make(map[uint32]*MemoryView),
	}
}

// A guest-supplied count near UINT32_MAX must not wrap offset+count and slip
// past the clamp into an out-of-range slice. Before the clamp fix,
// memoryViewRead(id, offset=16, count=0xFFFFFFF8, …) panicked on data[16:8].
func TestMemoryViewReadCountOverflowIsClamped(t *testing.T) {
	const viewSize = 1024
	const srcPtr = 0
	const dstPtr = 4096
	const nPtr = 8192

	mod := &mockModule{mem: &mockMemory{buf: make([]byte, 16384)}}
	for i := 0; i < viewSize; i++ { // recognizable source bytes
		mod.mem.buf[srcPtr+i] = byte(i)
	}

	f := newFactory()

	// Create a view over [srcPtr, srcPtr+viewSize).
	idOut := uint32(12000)
	if rc := f.memoryViewNew(context.Background(), mod, srcPtr, viewSize, 0 /*isCloser*/, idOut); rc != 0 {
		t.Fatalf("memoryViewNew failed: errno %d", rc)
	}
	id := binary.LittleEndian.Uint32(mod.mem.buf[idOut : idOut+4])

	const offset = 16
	const count = uint32(0xFFFFFFF8) // == int32(-8); offset+count wraps to 8

	rc := f.memoryViewRead(context.Background(), mod, id, offset, count, dstPtr, nPtr)
	if rc != 0 {
		t.Fatalf("memoryViewRead returned errno %d", rc)
	}

	// It must clamp to the bytes remaining after offset, not wrap.
	got := binary.LittleEndian.Uint32(mod.mem.buf[nPtr : nPtr+4])
	if want := uint32(viewSize - offset); got != want {
		t.Fatalf("clamped count = %d, want %d", got, want)
	}

	// And the copied-out bytes must be the real source region [offset, viewSize).
	for i := 0; i < viewSize-offset; i++ {
		if mod.mem.buf[dstPtr+i] != byte(offset+i) {
			t.Fatalf("byte %d = %d, want %d", i, mod.mem.buf[dstPtr+i], byte(offset+i))
		}
	}
}
