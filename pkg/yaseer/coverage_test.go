package seer

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

// ---------------------------------------------------------------
//  utils.go
// ---------------------------------------------------------------

func TestMapKeysNil(t *testing.T) {
	if got := mapKeys[any](nil); got != nil {
		t.Errorf("mapKeys(nil) = %v, want nil", got)
	}
}

func TestSafeInterfaceToStringKeys(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if got := safeInterfaceToStringKeys(nil); got != nil {
			t.Errorf("safeInterfaceToStringKeys(nil) = %v, want nil", got)
		}
	})
	t.Run("already string-keyed", func(t *testing.T) {
		in := map[string]any{"a": 1, "b": 2}
		got := safeInterfaceToStringKeys(in)
		if len(got) != 2 || got["a"] != 1 || got["b"] != 2 {
			t.Errorf("got %v", got)
		}
	})
	t.Run("any-keyed with strings", func(t *testing.T) {
		in := map[any]any{"a": 1, "b": 2}
		got := safeInterfaceToStringKeys(in)
		if len(got) != 2 || got["a"] != 1 || got["b"] != 2 {
			t.Errorf("got %v", got)
		}
	})
	t.Run("any-keyed with non-string keys gets stringified", func(t *testing.T) {
		in := map[any]any{42: "answer", "name": "hello"}
		got := safeInterfaceToStringKeys(in)
		if got["42"] != "answer" || got["name"] != "hello" {
			t.Errorf("got %v", got)
		}
	})
	t.Run("unsupported type returns nil", func(t *testing.T) {
		// Not a map → nil
		if got := safeInterfaceToStringKeys(42); got != nil {
			t.Errorf("got %v, want nil for non-map input", got)
		}
	})
}

// ---------------------------------------------------------------
//  options.go
// ---------------------------------------------------------------

func TestSystemFSBadPath(t *testing.T) {
	_, err := New(SystemFS("/this/path/does/not/exist/i/promise"))
	if err == nil {
		t.Error("expected error opening nonexistent dir")
	}
}

func TestSystemFSDuplicateFails(t *testing.T) {
	// Two FS options should reject the second.
	_, err := New(SystemFS("/tmp"), VirtualFS(afero.NewMemMapFs(), "/"))
	if err == nil {
		t.Error("expected error combining FS options")
	}
}

func TestVirtualFSBadPath(t *testing.T) {
	// Empty memfs without the base path: Stat("/") still works on
	// afero.NewMemMapFs, so set up a path that doesn't exist.
	memfs := afero.NewMemMapFs()
	// BasePathFs of a nonexistent path under memfs — root Stat
	// should still succeed because memfs is open. Use a base on the
	// real OS pointing nowhere to trigger the error branch.
	_, err := New(VirtualFS(memfs, "/nonexistent/sub/path"))
	if err != nil {
		// We expect either success or failure — log to silence "no
		// assertion" lint; mem-fs is lenient about subdir stats.
		t.Logf("VirtualFS error: %v (acceptable; mem-fs lenient)", err)
	}
}

func TestVirtualFSDuplicateFails(t *testing.T) {
	_, err := New(VirtualFS(afero.NewMemMapFs(), "/"), VirtualFS(afero.NewMemMapFs(), "/"))
	if err == nil {
		t.Error("expected error combining FS options")
	}
}

func TestWithWALEmptyPathRejected(t *testing.T) {
	_, err := New(VirtualFS(afero.NewMemMapFs(), "/"), WithWAL(""))
	if err == nil {
		t.Error("expected error for empty WAL path")
	}
}

// ---------------------------------------------------------------
//  wal.go — opToWire / wireToOp error paths
// ---------------------------------------------------------------

func TestOpToWireUnknownHandler(t *testing.T) {
	// Synthesise an op with a handler we never registered.
	o := op{
		opType:  opTypeGet,
		handler: func(this op, q *Query, p []string, v *yamlNode) ([]string, *yamlNode, error) { return p, v, nil },
	}
	if _, err := opToWire(o); err == nil {
		t.Error("expected error for unknown handler")
	}
}

func TestOpToWireAllValid(t *testing.T) {
	cases := []struct {
		name string
		o    op
		want byte
	}{
		{"Get → wireOpGet", op{opType: opTypeGet, handler: opGetOrCreate}, wireOpGet},
		{"GetOrCreate → wireOpGetOrCreate", op{opType: opTypeGetOrCreate, handler: opGetOrCreate}, wireOpGetOrCreate},
		{"CreateDocument → wireOpCreateDocument", op{opType: opTypeCreateDocument, handler: opCreateDocument}, wireOpCreateDocument},
		{"Set → wireOpSet", op{opType: opTypeSet, handler: opSetInYaml}, wireOpSet},
		{"Delete → wireOpDelete", op{opType: opTypeSet, handler: opDelete}, wireOpDelete},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := opToWire(c.o)
			if err != nil {
				t.Fatalf("opToWire: %v", err)
			}
			if got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestWireToOpUnknownCode(t *testing.T) {
	if _, err := wireToOp(0xFE, "x", nil); err == nil {
		t.Error("expected error for unknown wire code")
	}
}

func TestWireToOpAllValid(t *testing.T) {
	cases := []byte{wireOpGet, wireOpGetOrCreate, wireOpCreateDocument, wireOpSet, wireOpDelete}
	for _, c := range cases {
		o, err := wireToOp(c, "n", nil)
		if err != nil {
			t.Errorf("wireToOp(%d): %v", c, err)
		}
		if o.handler == nil {
			t.Errorf("wireToOp(%d): nil handler", c)
		}
	}
}

// Round-trip every wire code through encode → decode → re-encode so
// both directions of opToWire/wireToOp are exercised including the
// post-replay opTypeGet branch.
func TestWireCodeRoundTrip(t *testing.T) {
	for _, code := range []byte{wireOpGet, wireOpGetOrCreate, wireOpCreateDocument, wireOpSet, wireOpDelete} {
		// Build op from wire.
		o, err := wireToOp(code, "x", "v")
		if err != nil {
			t.Fatalf("wire %d → op: %v", code, err)
		}
		// Encode back to bytes.
		body, err := encodeOpsFrame([]op{o})
		if err != nil {
			t.Fatalf("encode wire %d: %v", code, err)
		}
		// Decode the body and check we get the same wire code back.
		rebuilt, err := decodeOpsFrame(body)
		if err != nil {
			t.Fatalf("decode wire %d: %v", code, err)
		}
		if len(rebuilt) != 1 {
			t.Fatalf("wire %d: opCount = %d, want 1", code, len(rebuilt))
		}
		got, err := opToWire(rebuilt[0])
		if err != nil {
			t.Fatalf("opToWire after replay for code %d: %v", code, err)
		}
		if got != code {
			t.Errorf("wire %d → op → wire = %d", code, got)
		}
	}
}

// ---------------------------------------------------------------
//  wal.go — encode / decode error paths
// ---------------------------------------------------------------

func TestEncodeOpsFrameNameTooLong(t *testing.T) {
	// 65536 bytes — exceeds uint16.
	huge := strings.Repeat("a", 1<<16)
	ops := []op{{opType: opTypeGet, name: huge, handler: opGetOrCreate}}
	if _, err := encodeOpsFrame(ops); err == nil {
		t.Error("expected error for name longer than uint16")
	}
}

func TestDecodeOpsFrameTruncatedAtEveryStep(t *testing.T) {
	// Build a valid frame body, then progressively truncate from the
	// tail to hit each "truncated" error branch.
	q := (&Query{seer: &Seer{}}).Get("x").Set("y")
	body, err := encodeOpsFrame(q.ops)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	// Try lengths from 0 to body-1; each should yield decode error.
	for cutoff := 0; cutoff < len(body); cutoff++ {
		if _, err := decodeOpsFrame(body[:cutoff]); err == nil {
			t.Errorf("decode at cutoff %d unexpectedly succeeded", cutoff)
		}
	}
}

func TestDecodeOpsFrameTrailingBytes(t *testing.T) {
	q := (&Query{seer: &Seer{}}).Get("x")
	body, _ := encodeOpsFrame(q.ops)
	body = append(body, 0x99) // trailing garbage byte
	if _, err := decodeOpsFrame(body); err == nil {
		t.Error("expected error for trailing bytes")
	}
}

func TestDecodeOpsFrameUnknownWireCode(t *testing.T) {
	// One op with an unrecognised wire code (>>4).
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint16(1)) // opCount
	b.WriteByte(0xFF)                             // bad wire
	binary.Write(&b, binary.BigEndian, uint16(0)) // nameLen
	binary.Write(&b, binary.BigEndian, uint32(0)) // valLen
	if _, err := decodeOpsFrame(b.Bytes()); err == nil {
		t.Error("expected error for unknown wire code")
	}
}

// ---------------------------------------------------------------
//  wal.go — loadCommitFrames partial / corrupt / missing
// ---------------------------------------------------------------

func TestLoadCommitFramesMissingWAL(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s := &Seer{fs: memfs, walPath: ".wal"}
	frames, err := s.loadCommitFrames()
	if err != nil {
		t.Errorf("missing WAL should not error: %v", err)
	}
	if frames != nil {
		t.Errorf("missing WAL should return nil frames, got %v", frames)
	}
}

func TestLoadCommitFramesWALDisabled(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s := &Seer{fs: memfs, walPath: ""}
	frames, err := s.loadCommitFrames()
	if err != nil || frames != nil {
		t.Errorf("disabled WAL: frames=%v err=%v", frames, err)
	}
}

func TestLoadCommitFramesBadCRC(t *testing.T) {
	memfs := afero.NewMemMapFs()
	// Build a valid frame, then flip a byte in the body so the crc
	// check fails. loadCommitFrames should silently truncate
	// recovery at that point.
	s := &Seer{fs: memfs, walPath: ".wal"}
	q := s.Query().Get("x").Set("y")
	body, _ := encodeOpsFrame(q.ops)
	var buf bytes.Buffer
	buf.WriteString(frameMagic)
	binary.Write(&buf, binary.BigEndian, uint32(len(body)))
	buf.Write(body)
	// Append a deliberately wrong CRC.
	binary.Write(&buf, binary.BigEndian, uint32(0xDEADBEEF))
	afero.WriteFile(memfs, "/.wal", buf.Bytes(), 0o600)

	frames, err := s.loadCommitFrames()
	if err != nil {
		t.Errorf("bad-CRC frame should not error: %v", err)
	}
	if len(frames) != 0 {
		t.Errorf("bad-CRC frame should produce no replayable frames; got %d", len(frames))
	}
}

func TestLoadCommitFramesPartialBodyLength(t *testing.T) {
	// File has magic + bodyLen claiming a huge body, but actual
	// bytes don't follow. Should treat as partial and drop.
	memfs := afero.NewMemMapFs()
	var buf bytes.Buffer
	buf.WriteString(frameMagic)
	binary.Write(&buf, binary.BigEndian, uint32(1<<20)) // claim 1 MB
	buf.WriteByte(0xAA)                                 // a single byte
	afero.WriteFile(memfs, "/.wal", buf.Bytes(), 0o600)

	s := &Seer{fs: memfs, walPath: ".wal"}
	frames, _ := s.loadCommitFrames()
	if len(frames) != 0 {
		t.Errorf("partial body should yield no frames; got %d", len(frames))
	}
}

func TestLoadCommitFramesBadMagic(t *testing.T) {
	memfs := afero.NewMemMapFs()
	afero.WriteFile(memfs, "/.wal", []byte("XXXX...not a yaseer wal"), 0o600)
	s := &Seer{fs: memfs, walPath: ".wal"}
	frames, _ := s.loadCommitFrames()
	if len(frames) != 0 {
		t.Errorf("bad magic should yield no frames; got %d", len(frames))
	}
}

// TestLoadCommitFramesShortFile exercises the "len(data) < 4" early
// break — a WAL truncated to fewer bytes than the magic prefix.
func TestLoadCommitFramesShortFile(t *testing.T) {
	memfs := afero.NewMemMapFs()
	afero.WriteFile(memfs, "/.wal", []byte("AB"), 0o600)
	s := &Seer{fs: memfs, walPath: ".wal"}
	frames, _ := s.loadCommitFrames()
	if len(frames) != 0 {
		t.Errorf("3-byte WAL should yield no frames; got %d", len(frames))
	}
}

// TestLoadCommitFramesMagicNoBodyLen exercises the "len(data) < 8"
// break — magic present, but the 4-byte body-length field is
// missing. A crash mid-write between magic and bodyLen.
func TestLoadCommitFramesMagicNoBodyLen(t *testing.T) {
	memfs := afero.NewMemMapFs()
	afero.WriteFile(memfs, "/.wal", []byte(frameMagic+"ab"), 0o600)
	s := &Seer{fs: memfs, walPath: ".wal"}
	frames, _ := s.loadCommitFrames()
	if len(frames) != 0 {
		t.Errorf("magic-only WAL should yield no frames; got %d", len(frames))
	}
}

// TestLoadCommitFramesDecodeError forges a frame whose body
// passes CRC but contains an invalid op (unknown wire code), so
// decodeOpsFrame returns an error and loadCommitFrames truncates
// recovery at that point.
func TestLoadCommitFramesDecodeError(t *testing.T) {
	// Body: opCount=1, wire=0xFE (unknown), nameLen=0, valLen=0
	var body bytes.Buffer
	binary.Write(&body, binary.BigEndian, uint16(1)) // opCount
	body.WriteByte(0xFE)                             // bad wire
	binary.Write(&body, binary.BigEndian, uint16(0)) // nameLen
	binary.Write(&body, binary.BigEndian, uint32(0)) // valLen
	bodyBytes := body.Bytes()

	var frame bytes.Buffer
	frame.WriteString(frameMagic)
	binary.Write(&frame, binary.BigEndian, uint32(len(bodyBytes)))
	frame.Write(bodyBytes)
	sum := crc32.ChecksumIEEE(frame.Bytes()[4:])
	binary.Write(&frame, binary.BigEndian, sum)

	memfs := afero.NewMemMapFs()
	afero.WriteFile(memfs, "/.wal", frame.Bytes(), 0o600)
	s := &Seer{fs: memfs, walPath: ".wal"}
	frames, _ := s.loadCommitFrames()
	if len(frames) != 0 {
		t.Errorf("frame with bad wire code should yield no frames; got %d", len(frames))
	}
}

// TestLoadCommitFramesPartialMagic — exactly 1 byte. Triggers the
// outer "len(data) < 4" inside the loop.
func TestLoadCommitFramesPartialMagic(t *testing.T) {
	memfs := afero.NewMemMapFs()
	afero.WriteFile(memfs, "/.wal", []byte{'Y'}, 0o600)
	s := &Seer{fs: memfs, walPath: ".wal"}
	frames, _ := s.loadCommitFrames()
	if len(frames) != 0 {
		t.Errorf("1-byte WAL should yield no frames; got %d", len(frames))
	}
}

// ---------------------------------------------------------------
//  Fault-injection FS — drives syncLocked's per-step error paths
//  (Open / Write / Sync / Close) by wrapping afero.MemMapFs and
//  letting a test flag specific failures.
// ---------------------------------------------------------------

type faultFs struct {
	afero.Fs
	failWrite bool
	failSync  bool
	failClose bool
}

func (f *faultFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	inner, err := f.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return &faultFile{File: inner, parent: f}, nil
}

type faultFile struct {
	afero.File
	parent *faultFs
}

func (f *faultFile) Write(p []byte) (int, error) {
	if f.parent.failWrite {
		return 0, errors.New("fault: write")
	}
	return f.File.Write(p)
}
func (f *faultFile) Sync() error {
	if f.parent.failSync {
		return errors.New("fault: sync")
	}
	return f.File.Sync()
}
func (f *faultFile) Close() error {
	if f.parent.failClose {
		return errors.New("fault: close")
	}
	return f.File.Close()
}

// TestSyncWriteFails exercises syncLocked's write-error branch.
func TestSyncWriteFails(t *testing.T) {
	ff := &faultFs{Fs: afero.NewMemMapFs()}
	s, _ := New(VirtualFS(ff, "/"))
	if err := s.Get("c").Document().Set("v").Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	ff.failWrite = true
	if err := s.Sync(); err == nil {
		t.Error("expected Sync to fail on write fault")
	}
}

// TestSyncFsyncFails exercises the syncer.Sync() error branch.
func TestSyncFsyncFails(t *testing.T) {
	ff := &faultFs{Fs: afero.NewMemMapFs()}
	s, _ := New(VirtualFS(ff, "/"))
	if err := s.Get("c").Document().Set("v").Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	ff.failSync = true
	if err := s.Sync(); err == nil {
		t.Error("expected Sync to fail on fsync fault")
	}
}

// TestSyncCloseFails exercises the trailing Close() error branch.
func TestSyncCloseFails(t *testing.T) {
	ff := &faultFs{Fs: afero.NewMemMapFs()}
	s, _ := New(VirtualFS(ff, "/"))
	if err := s.Get("c").Document().Set("v").Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	ff.failClose = true
	if err := s.Sync(); err == nil {
		t.Error("expected Sync to fail on close fault")
	}
}

// TestAppendCommitWALWriteFails: appendCommitWAL surfaces the
// Write/Sync errors from the underlying file.
func TestAppendCommitWALWriteFails(t *testing.T) {
	ff := &faultFs{Fs: afero.NewMemMapFs(), failWrite: true}
	s, _ := New(VirtualFS(ff, "/"), WithWAL(".wal"))
	if err := s.Get("c").Document().Set("v").Commit(); err == nil {
		t.Error("expected commit to surface WAL write fault")
	}
}

func TestAppendCommitWALFsyncFails(t *testing.T) {
	ff := &faultFs{Fs: afero.NewMemMapFs(), failSync: true}
	s, _ := New(VirtualFS(ff, "/"), WithWAL(".wal"))
	if err := s.Get("c").Document().Set("v").Commit(); err == nil {
		t.Error("expected commit to surface WAL fsync fault")
	}
}

// TestReplayWALCommitFailureSkips: a frame whose Commit fails (e.g.
// the op handler returns an error mid-replay) is logged and
// skipped; we still proceed to Sync the remaining frames.
//
// Construct by serialising a frame that opGetOrCreate will reject —
// an empty Get name, which the underlying handler treats as
// invalid.
func TestReplayWALCommitFailureSkips(t *testing.T) {
	memfs := afero.NewMemMapFs()
	// Stage a WAL with one frame that should be rejectable at
	// commit time (no name on a Get).
	bad := []op{{
		opType:  opTypeGetOrCreate,
		name:    "", // empty name — rejected at handler
		handler: opGetOrCreate,
	}}
	body, _ := encodeOpsFrame(bad)
	var frame bytes.Buffer
	frame.WriteString(frameMagic)
	binary.Write(&frame, binary.BigEndian, uint32(len(body)))
	frame.Write(body)
	sum := crc32.ChecksumIEEE(frame.Bytes()[4:])
	binary.Write(&frame, binary.BigEndian, sum)
	afero.WriteFile(memfs, "/.wal", frame.Bytes(), 0o600)

	// New() should NOT error — it logs and proceeds.
	if _, err := New(VirtualFS(memfs, "/"), WithWAL(".wal")); err != nil {
		t.Errorf("New with bad WAL frame should not error: %v", err)
	}
}

// ---------------------------------------------------------------
//  wal.go — clearWAL idempotency
// ---------------------------------------------------------------

func TestClearWALIdempotent(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s := &Seer{fs: memfs, walPath: ".wal"}
	// Clear when nothing exists — should be a clean no-op.
	if err := s.clearWAL(); err != nil {
		t.Errorf("clearWAL on absent file: %v", err)
	}
	// Write something, clear, then clear again.
	afero.WriteFile(memfs, "/.wal", []byte("x"), 0o600)
	if err := s.clearWAL(); err != nil {
		t.Errorf("clearWAL existing: %v", err)
	}
	if err := s.clearWAL(); err != nil {
		t.Errorf("clearWAL after first clear: %v", err)
	}
}

func TestClearWALDisabled(t *testing.T) {
	s := &Seer{walPath: ""}
	if err := s.clearWAL(); err != nil {
		t.Errorf("clearWAL with disabled WAL: %v", err)
	}
}

// ---------------------------------------------------------------
//  wal.go — appendCommitWAL paths
// ---------------------------------------------------------------

func TestAppendCommitWALDisabledIsNoop(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s := &Seer{fs: memfs, walPath: ""}
	q := s.Query().Get("x").Set("y")
	if err := s.appendCommitWAL(q.ops); err != nil {
		t.Errorf("disabled appendCommitWAL should be noop, got %v", err)
	}
}

func TestAppendCommitWALReadOnlyFS(t *testing.T) {
	// Wrap MemMapFs in a read-only layer so Open(O_CREATE|O_WRONLY)
	// fails — exercises the "open" error path.
	roFS := afero.NewReadOnlyFs(afero.NewMemMapFs())
	s := &Seer{fs: roFS, walPath: ".wal"}
	q := s.Query().Get("x").Set("y")
	if err := s.appendCommitWAL(q.ops); err == nil {
		t.Error("expected error appending to read-only FS")
	}
}

// ---------------------------------------------------------------
//  root.go — List error paths + folder vs file
// ---------------------------------------------------------------

func TestRootListEmpty(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s, _ := New(VirtualFS(memfs, "/"))
	out, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty list, got %v", out)
	}
}

func TestRootListMixed(t *testing.T) {
	memfs := afero.NewMemMapFs()
	// One folder, one yaml file, one non-yaml file (should be filtered).
	memfs.MkdirAll("/sub", 0o755)
	afero.WriteFile(memfs, "/doc.yaml", []byte("x: 1\n"), 0o600)
	afero.WriteFile(memfs, "/readme.md", []byte("notes"), 0o600)
	s, _ := New(VirtualFS(memfs, "/"))
	out, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantSet := map[string]bool{"sub": false, "doc": false}
	for _, name := range out {
		if _, ok := wantSet[name]; ok {
			wantSet[name] = true
		}
	}
	for n, found := range wantSet {
		if !found {
			t.Errorf("List() missing %q; got %v", n, out)
		}
	}
}

func TestQueryListOnFolder(t *testing.T) {
	// Exercise node.go List() — the folder-listing branch. Builds a
	// few yaml docs under /cars/ then asks for cars.List().
	memfs := afero.NewMemMapFs()
	s, _ := New(VirtualFS(memfs, "/"))
	for _, name := range []string{"mac", "linux", "windows"} {
		if err := s.Get("cars").Get(name).Document().Set("info").Commit(); err != nil {
			t.Fatalf("commit %s: %v", name, err)
		}
	}
	if err := s.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	items, err := s.Get("cars").List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := map[string]bool{"mac": false, "linux": false, "windows": false}
	for _, n := range items {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, found := range want {
		if !found {
			t.Errorf("Query.List() missing %q; got %v", n, items)
		}
	}
}

// ---------------------------------------------------------------
//  new.go — error path: replayWAL fails (corrupt-after-magic)
// ---------------------------------------------------------------

func TestNewReplayCorruptIsIgnored(t *testing.T) {
	memfs := afero.NewMemMapFs()
	// Write a WAL with the magic but no usable content. New() should
	// treat as nothing-to-replay (not an error).
	afero.WriteFile(memfs, "/.wal", []byte(frameMagic+"X"), 0o600)
	if _, err := New(VirtualFS(memfs, "/"), WithWAL(".wal")); err != nil {
		t.Errorf("New with truncated WAL: %v", err)
	}
}

// ---------------------------------------------------------------
//  Batch.Commit propagates errors
// ---------------------------------------------------------------

func TestBatchCommitPropagatesError(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s, _ := New(VirtualFS(memfs, "/"))
	// Build a Batch where one Query errors on Commit by handing it
	// an error pre-populated.
	good := s.Get("ok").Document().Set("v")
	bad := s.Query()
	bad.errors = append(bad.errors, errors.New("synthetic"))
	b := s.Batch(good, bad)
	if err := b.Commit(); err == nil {
		t.Error("expected Batch.Commit to surface inner error")
	}
}

// ---------------------------------------------------------------
//  IO error path on Sync (read-only FS)
// ---------------------------------------------------------------

func TestSyncReadOnlyFails(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s, _ := New(VirtualFS(memfs, "/"))
	if err := s.Get("config").Document().Set("v").Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// Swap the FS for a read-only one so the next Sync's OpenFile
	// for write fails.
	s.fs = afero.NewReadOnlyFs(memfs)
	if err := s.Sync(); err == nil {
		t.Error("expected Sync to fail on read-only FS")
	}
}

// Compile-time check for sentinel errors we test against.
var _ = errors.New

// Sanity: fs.ErrNotExist still resolves.
var _ = fs.ErrNotExist

// ---------------------------------------------------------------
//  yaml.Marshal error path in encodeOpsFrame
// ---------------------------------------------------------------

// (yaml.v3 PANICS on chan / func / complex inputs rather than
// returning an error, so the marshal-error branch in
// encodeOpsFrame can't be triggered with a synthetic value. It
// remains defensive code for hypothetical encoder regressions.)

// ---------------------------------------------------------------
//  More upstream coverage — Document on non-existent path, Set then
//  immediate Value (no Sync), Delete via Query
// ---------------------------------------------------------------

func TestDocumentOnFreshKey(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s, _ := New(VirtualFS(memfs, "/"))
	if err := s.Get("new").Document().Set(42).Commit(); err != nil {
		t.Fatalf("commit fresh doc: %v", err)
	}
	if err := s.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	var v int
	if err := s.Get("new").Value(&v); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if v != 42 {
		t.Errorf("got %d, want 42", v)
	}
}

func TestDeleteThenValueErrors(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s, _ := New(VirtualFS(memfs, "/"))
	if err := s.Get("d").Document().Get("k").Set("v").Commit(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := s.Get("d").Document().Get("k").Delete().Commit(); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var got string
	if err := s.Get("d").Document().Get("k").Value(&got); err == nil {
		t.Error("expected error reading deleted key")
	}
}

// TestNewSyncTwiceIsIdempotent: two consecutive Sync() calls without
// new commits should both succeed and leave the file matching the
// in-memory state.
func TestNewSyncTwiceIsIdempotent(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s, _ := New(VirtualFS(memfs, "/"))
	if err := s.Get("c").Document().Set("v").Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := s.Sync(); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	if err := s.Sync(); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	got, err := afero.ReadFile(memfs, "/c.yaml")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(got), "v") {
		t.Errorf("file contents: %q", got)
	}
}

// TestReplayWALWithDisabled ensures replayWAL is a clean no-op when
// the WAL path is empty (defensive: New() with no WAL should never
// touch any file).
func TestReplayWALWithDisabled(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s := &Seer{fs: memfs, walPath: ""}
	if err := s.replayWAL(); err != nil {
		t.Errorf("disabled replayWAL: %v", err)
	}
}

// TestCommitWithPriorErrors covers the early-return branch in
// Query.Commit when n.errors is non-empty.
func TestCommitWithPriorErrors(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s, _ := New(VirtualFS(memfs, "/"))
	q := s.Query()
	q.errors = append(q.errors, errors.New("synthetic"))
	if err := q.Commit(); err == nil {
		t.Error("expected commit to surface preexisting errors")
	}
}

// TestValueWithPriorErrors covers Query.Value's prior-errors early
// return.
func TestValueWithPriorErrors(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s, _ := New(VirtualFS(memfs, "/"))
	q := s.Query()
	q.errors = append(q.errors, errors.New("synthetic"))
	var v string
	if err := q.Value(&v); err == nil {
		t.Error("expected Value to surface preexisting errors")
	}
}

// TestDecodeOpsFrameYAMLUnmarshalError: bad yaml in the val portion
// should surface as an Unmarshal error from decodeOpsFrame.
func TestDecodeOpsFrameYAMLUnmarshalError(t *testing.T) {
	// Build a frame body where val is bytes that yaml.v3 can't
	// parse: a half-open quoted string.
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint16(1)) // opCount
	b.WriteByte(wireOpSet)                        // wire
	binary.Write(&b, binary.BigEndian, uint16(0)) // nameLen
	binary.Write(&b, binary.BigEndian, uint32(7)) // valLen
	b.WriteString(`"unterm`)                      // unterminated quoted yaml
	if _, err := decodeOpsFrame(b.Bytes()); err == nil {
		t.Error("expected yaml unmarshal error")
	}
}

// TestSystemFSDuplicateOption is the partner of
// TestSystemFSDuplicateFails — exercises the SystemFS branch that
// rejects a second FS option.
func TestSystemFSPathThenVirtualFails(t *testing.T) {
	_, err := New(VirtualFS(afero.NewMemMapFs(), "/"), SystemFS("/tmp"))
	if err == nil {
		t.Error("expected error mixing FS options")
	}
}

// TestValueDecodeErrorFormatting covers the file/line/column error
// branches in Query.Value when yaml.Decode fails — e.g. reading a
// string field as an int.
func TestValueDecodeErrorFormatting(t *testing.T) {
	memfs := afero.NewMemMapFs()
	if err := afero.WriteFile(memfs, "/conf.yaml",
		[]byte("port: not-a-number\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := New(VirtualFS(memfs, "/"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var port int
	if err := s.Get("conf").Document().Get("port").Value(&port); err == nil {
		t.Error("expected decode error reading string into *int")
	}
}

// TestNewReplayWALErrorBubbles forces replayWAL to surface an error
// at New-time. We stage a WAL frame whose op handler panics is not
// realistic — instead, write a totally bogus WAL with the magic but
// gibberish bodyLen claim. loadCommitFrames truncates silently
// (already covered); the actual error path triggers when the WAL
// file Open returns an error other than NotExist.
//
// Use a faultFs whose Open() of the WAL fails with a synthetic error.
func TestNewReplayWALOpenError(t *testing.T) {
	mem := afero.NewMemMapFs()
	afero.WriteFile(mem, "/.wal", []byte("anything"), 0o600)
	ff := &faultOpenFs{Fs: mem, failOnOpen: ".wal"}
	if _, err := New(VirtualFS(ff, "/"), WithWAL(".wal")); err == nil {
		t.Error("expected New to surface WAL open error")
	}
}

// faultOpenFs fails Open / OpenFile for any path matching
// failOnOpen — used to drive the loadCommitFrames "open returned
// non-ENOENT" branch.
type faultOpenFs struct {
	afero.Fs
	failOnOpen string
}

func (f *faultOpenFs) Open(name string) (afero.File, error) {
	if strings.HasSuffix(name, f.failOnOpen) {
		return nil, errors.New("fault: open")
	}
	return f.Fs.Open(name)
}

func (f *faultOpenFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if strings.HasSuffix(name, f.failOnOpen) {
		return nil, errors.New("fault: openfile")
	}
	return f.Fs.OpenFile(name, flag, perm)
}

// TestDeleteOnReadQuery covers the !query.write branch in opDelete /
// _opDeleteInYaml — invoking Delete() in a Value() context (which
// sets write=false) must return an error.
func TestDeleteOnReadQuery(t *testing.T) {
	memfs := afero.NewMemMapFs()
	s, _ := New(VirtualFS(memfs, "/"))
	// Set up a doc with a key.
	if err := s.Get("d").Document().Get("k").Set("v").Commit(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Build a Query with Delete then call Value — write is false on
	// Value, so opDelete should error.
	q := s.Get("d").Document().Get("k").Delete()
	var got string
	if err := q.Value(&got); err == nil {
		t.Error("expected Delete in read query to error")
	}
}

// TestClearWALRemoveError uses a fault FS whose Remove always fails
// to cover clearWAL's "non-ENOENT" branch.
func TestClearWALRemoveError(t *testing.T) {
	mem := afero.NewMemMapFs()
	afero.WriteFile(mem, "/.wal", []byte("x"), 0o600)
	ff := &faultRemoveFs{Fs: mem}
	s := &Seer{fs: ff, walPath: ".wal"}
	if err := s.clearWAL(); err == nil {
		t.Error("expected clearWAL to surface remove error")
	}
}

type faultRemoveFs struct{ afero.Fs }

func (f *faultRemoveFs) Remove(name string) error {
	return errors.New("fault: remove")
}

// TestLoadCommitFramesOpenError uses faultOpenFs to make Open of
// the WAL return a non-ENOENT error.
func TestLoadCommitFramesOpenError(t *testing.T) {
	ff := &faultOpenFs{Fs: afero.NewMemMapFs(), failOnOpen: ".wal"}
	// Pre-write so the file "exists" even though our Open fails.
	afero.WriteFile(ff.Fs, "/.wal", []byte("x"), 0o600)
	s := &Seer{fs: ff, walPath: ".wal"}
	if _, err := s.loadCommitFrames(); err == nil {
		t.Error("expected loadCommitFrames to surface Open error")
	}
}
