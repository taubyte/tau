package storage

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

// --- minimal mocks: only what the content host functions actually touch ---

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

func (m *mockMemory) Size() uint32 { return uint32(len(m.buf)) }

type mockModule struct {
	vm.Module
	mem *mockMemory
}

func (m *mockModule) Memory() vm.Memory { return m.mem }

type mockContext struct {
	project string
}

func (c *mockContext) Context() context.Context         { return context.Background() }
func (c *mockContext) Project() string                  { return c.project }
func (c *mockContext) Application() string              { return "" }
func (c *mockContext) Resource() string                 { return "" }
func (c *mockContext) Branches() []string               { return nil }
func (c *mockContext) Commit() string                   { return "" }
func (c *mockContext) Clone(context.Context) vm.Context { return c }

type mockInstance struct {
	vm.Instance
	ctx vm.Context
}

func (i *mockInstance) Context() vm.Context { return i.ctx }

func newTestFactory(project string) *Factory {
	inst := &mockInstance{ctx: &mockContext{project: project}}
	return New(inst, nil, helpers.New(context.Background()))
}

func newModule() *mockModule { return &mockModule{mem: &mockMemory{buf: make([]byte, 4096)}} }

// newContent drives storageNewContent and returns the guest-visible id and the
// on-disk path the factory chose for it.
func newContent(t *testing.T, f *Factory) (uint32, string) {
	t.Helper()
	mod := newModule()
	if rc := f.storageNewContent(context.Background(), mod, 0); rc != 0 {
		t.Fatalf("storageNewContent failed: errno %d", rc)
	}
	id := binary.LittleEndian.Uint32(mod.mem.buf[0:4])
	c, errno := f.getContent(id)
	if errno != 0 {
		t.Fatalf("getContent(%d) failed: errno %d", id, errno)
	}
	return id, c.path
}

// Regression for the shared-CWD content-file collision: two concurrent
// instances of the *same* project each reset their content counter to 0, so the
// old `os.Create("tempFile" + counter)` scheme handed both the identical
// relative path "tempFile0" — a cross-tenant/cross-call alias where one guest's
// write clobbered or leaked another's staged content. Project id in the name
// alone would not fix this (same project), so the fix uses os.CreateTemp.
func TestContentTempFilesDoNotCollideAcrossInstances(t *testing.T) {
	const project = "PROJECTX"
	a := newTestFactory(project)
	b := newTestFactory(project)
	t.Cleanup(func() { a.Close(); b.Close() })

	idA, pathA := newContent(t, a)
	idB, pathB := newContent(t, b)

	// Both counters start at 0 — this is precisely the case the old scheme collided on.
	if idA != 0 || idB != 0 {
		t.Fatalf("expected both content ids 0 (fresh per-instance counters), got %d and %d", idA, idB)
	}

	if pathA == pathB {
		t.Fatalf("content files collide across instances: both %q", pathA)
	}

	for _, p := range []string{pathA, pathB} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("temp file %q not created: %v", p, err)
		}
		if !strings.HasPrefix(filepath.Base(p), "tau-content-"+project+"-") {
			t.Errorf("temp file %q is not namespaced by project", p)
		}
		if filepath.Dir(p) == "." {
			t.Errorf("temp file %q is a relative path in the process CWD", p)
		}
	}
}

// A guest-supplied read length larger than the guest's own memory must be
// rejected before the host allocation — not make([]byte, hugeLen) first. This
// exercises the shared helpers.Read path that backs contentReadFile,
// storageReadFile and readHttpResponseBody. The regression signal is bytes
// allocated: the old code allocated the full ~4 GiB before failing the
// write-back, so a return-code check alone would not catch it.
func TestContentReadRejectsOversizedLength(t *testing.T) {
	f := newTestFactory("P")
	t.Cleanup(func() { f.Close() })

	id, _ := newContent(t, f)
	mod := newModule() // 4096-byte guest memory
	const hugeLen = uint32(0xFFFFFFFF)

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	rc := f.contentReadFile(context.Background(), mod, id, 0, hugeLen, 0)
	runtime.ReadMemStats(&after)

	if rc != uint32(errno.ErrorAddressOutOfMemory) {
		t.Fatalf("contentReadFile(bufLen=%#x) = errno %d, want ErrorAddressOutOfMemory (%d)",
			hugeLen, rc, uint32(errno.ErrorAddressOutOfMemory))
	}
	if delta := after.TotalAlloc - before.TotalAlloc; delta > 64<<20 {
		t.Fatalf("contentReadFile allocated %d bytes for an oversized read; the cap did not run before make()", delta)
	}
}

// Closing a content handle must remove its temp file and drop the map entry, and
// Factory.Close must sweep any content the guest never closed — otherwise the
// files (and their fds) leak for the process lifetime.
func TestContentCloseRemovesTempFile(t *testing.T) {
	f := newTestFactory("P")

	// Explicit close removes the file and forgets the handle.
	id, path := newContent(t, f)
	if rc := f.contentCloseFile(context.Background(), newModule(), id); rc != 0 {
		t.Fatalf("contentCloseFile failed: errno %d", rc)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temp file %q still present after close: %v", path, err)
	}
	if _, errno := f.getContent(id); errno == 0 {
		t.Fatalf("content %d still registered after close", id)
	}

	// A handle the guest never closes is swept by Factory.Close.
	_, leaked := newContent(t, f)
	if err := f.Close(); err != nil {
		t.Fatalf("Factory.Close failed: %v", err)
	}
	if _, err := os.Stat(leaked); !os.IsNotExist(err) {
		t.Fatalf("temp file %q leaked past Factory.Close: %v", leaked, err)
	}
}
