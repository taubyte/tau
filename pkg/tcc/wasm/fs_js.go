//go:build js && wasm

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"syscall/js"
	"time"

	"github.com/spf13/afero"
)

// jsFs adapts a set of JS filesystem primitives into an afero.Fs so the tcc
// compiler/decompiler (which read/write YAML through yaseer -> afero) can run
// against a browser-provided filesystem. The compile/decompile core is
// unchanged; only this adapter is new.
//
// The JS side must provide an object with synchronous methods:
//
//	readFile(path)        -> Uint8Array | string | null   (null if missing)
//	writeFile(path, data) -> void                          (data is a Uint8Array)
//	readdir(path)         -> string[]                      (child base names)
//	stat(path)            -> { isDir: bool, size: number } | null
//	mkdir(path)           -> void                          (idempotent; may no-op)
//
// yaseer only ever calls Open (read-all), OpenFile (create+truncate+write),
// Stat and afero.ReadDir, so the surface implemented here is deliberately
// minimal. ponytail: mutating ops we never hit (Remove/Rename/Chmod/...) are
// best-effort no-ops — add real impls only if a caller needs them.
type jsFs struct {
	p js.Value // the primitives object
}

var _ afero.Fs = (*jsFs)(nil)

func normalize(name string) string {
	if name == "" {
		return "/"
	}
	if name[0] != '/' {
		name = "/" + name
	}
	return path.Clean(name)
}

// safeCall invokes a JS primitive, converting a thrown JS exception (which
// surfaces as a Go panic) into an error.
func (f *jsFs) safeCall(method string, args ...any) (res js.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("js %s failed: %v", method, r)
		}
	}()
	return f.p.Call(method, args...), nil
}

func jsToBytes(v js.Value) []byte {
	if v.Type() == js.TypeString {
		return []byte(v.String())
	}
	n := v.Get("length").Int()
	buf := make([]byte, n)
	js.CopyBytesToGo(buf, v)
	return buf
}

func bytesToJS(b []byte) js.Value {
	u8 := js.Global().Get("Uint8Array").New(len(b))
	js.CopyBytesToJS(u8, b)
	return u8
}

func (f *jsFs) Name() string { return "jsFs" }

func (f *jsFs) Stat(name string) (os.FileInfo, error) {
	name = normalize(name)
	res, err := f.safeCall("stat", name)
	if err != nil {
		return nil, err
	}
	if res.IsNull() || res.IsUndefined() {
		return nil, &os.PathError{Op: "stat", Path: name, Err: os.ErrNotExist}
	}
	return &jsFileInfo{
		name: path.Base(name),
		size: int64(res.Get("size").Int()),
		dir:  res.Get("isDir").Bool(),
	}, nil
}

func (f *jsFs) Open(name string) (afero.File, error) {
	name = normalize(name)
	fi, err := f.Stat(name)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return &jsFile{fs: f, name: name, dir: true}, nil
	}
	res, err := f.safeCall("readFile", name)
	if err != nil {
		return nil, err
	}
	if res.IsNull() || res.IsUndefined() {
		return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrNotExist}
	}
	return &jsFile{fs: f, name: name, reader: bytes.NewReader(jsToBytes(res))}, nil
}

func (f *jsFs) OpenFile(name string, flag int, _ os.FileMode) (afero.File, error) {
	name = normalize(name)
	// Any write intent -> return a buffered writer that flushes on Close.
	// yaseer always opens with O_CREATE|O_RDWR|O_TRUNC, so starting empty is
	// the correct truncate semantics.
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0 {
		// Persist the (empty, truncated) file immediately so it is visible to a
		// subsequent Stat/Open before this handle is Closed. afero.MemMapFs —
		// which yaseer is written against — creates the entry on OpenFile, and
		// the seer reads the doc back within the same operation.
		// ponytail: unconditional truncate is correct because yaseer only ever
		// opens with O_TRUNC; a non-truncating append path would need to preserve
		// existing bytes here.
		if _, err := f.safeCall("writeFile", name, bytesToJS(nil)); err != nil {
			return nil, err
		}
		return &jsFile{fs: f, name: name, writeBuf: &bytes.Buffer{}, writable: true}, nil
	}
	return f.Open(name)
}

func (f *jsFs) Create(name string) (afero.File, error) {
	return f.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
}

// Directories are implicit (derived from file paths on the JS side), so mkdir
// is best-effort and never fatal.
func (f *jsFs) Mkdir(name string, _ os.FileMode) error {
	_, _ = f.safeCall("mkdir", normalize(name))
	return nil
}

func (f *jsFs) MkdirAll(p string, _ os.FileMode) error {
	_, _ = f.safeCall("mkdir", normalize(p))
	return nil
}

// Unused by the compile/decompile path — best-effort no-ops.
func (f *jsFs) Remove(name string) error                   { _, _ = f.safeCall("remove", normalize(name)); return nil }
func (f *jsFs) RemoveAll(p string) error                   { _, _ = f.safeCall("remove", normalize(p)); return nil }
func (f *jsFs) Rename(o, n string) error                   { return nil }
func (f *jsFs) Chmod(string, os.FileMode) error            { return nil }
func (f *jsFs) Chown(string, int, int) error               { return nil }
func (f *jsFs) Chtimes(string, time.Time, time.Time) error { return nil }

// jsFile is a minimal afero.File. Reads are served from an in-memory
// bytes.Reader loaded up front; writes accumulate in a buffer flushed to the
// JS writeFile primitive on Close.
type jsFile struct {
	fs       *jsFs
	name     string
	reader   *bytes.Reader
	writeBuf *bytes.Buffer
	writable bool
	dir      bool
}

var _ afero.File = (*jsFile)(nil)

var errWrongMode = errors.New("jsFile: operation not supported in this mode")

func (fl *jsFile) Name() string { return fl.name }

func (fl *jsFile) Read(p []byte) (int, error) {
	if fl.reader == nil {
		return 0, errWrongMode
	}
	return fl.reader.Read(p)
}

func (fl *jsFile) ReadAt(p []byte, off int64) (int, error) {
	if fl.reader == nil {
		return 0, errWrongMode
	}
	return fl.reader.ReadAt(p, off)
}

func (fl *jsFile) Seek(offset int64, whence int) (int64, error) {
	if fl.reader == nil {
		return 0, errWrongMode
	}
	return fl.reader.Seek(offset, whence)
}

func (fl *jsFile) Write(p []byte) (int, error) {
	if fl.writeBuf == nil {
		return 0, errWrongMode
	}
	return fl.writeBuf.Write(p)
}

func (fl *jsFile) WriteString(s string) (int, error) {
	if fl.writeBuf == nil {
		return 0, errWrongMode
	}
	return fl.writeBuf.WriteString(s)
}

func (fl *jsFile) WriteAt([]byte, int64) (int, error) { return 0, errWrongMode }

func (fl *jsFile) Close() error {
	if fl.writable && fl.writeBuf != nil {
		_, err := fl.fs.safeCall("writeFile", fl.name, bytesToJS(fl.writeBuf.Bytes()))
		return err
	}
	return nil
}

func (fl *jsFile) Sync() error { return nil }

func (fl *jsFile) Truncate(size int64) error {
	if fl.writeBuf == nil {
		return errWrongMode
	}
	fl.writeBuf.Truncate(int(size))
	return nil
}

func (fl *jsFile) Readdir(count int) ([]os.FileInfo, error) {
	if !fl.dir {
		return nil, errors.New("jsFile: not a directory")
	}
	res, err := fl.fs.safeCall("readdir", fl.name)
	if err != nil {
		return nil, err
	}
	n := res.Length()
	out := make([]os.FileInfo, 0, n)
	for i := 0; i < n; i++ {
		child := res.Index(i).String()
		fi, err := fl.fs.Stat(path.Join(fl.name, child))
		if err != nil {
			continue
		}
		out = append(out, fi)
		if count > 0 && len(out) >= count {
			break
		}
	}
	return out, nil
}

func (fl *jsFile) Readdirnames(n int) ([]string, error) {
	infos, err := fl.Readdir(n)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(infos))
	for i, fi := range infos {
		names[i] = fi.Name()
	}
	return names, nil
}

func (fl *jsFile) Stat() (os.FileInfo, error) { return fl.fs.Stat(fl.name) }

// jsFileInfo is a static os.FileInfo (times are not tracked in the browser fs).
type jsFileInfo struct {
	name string
	size int64
	dir  bool
}

var _ os.FileInfo = (*jsFileInfo)(nil)

func (i *jsFileInfo) Name() string { return i.name }
func (i *jsFileInfo) Size() int64  { return i.size }
func (i *jsFileInfo) Mode() os.FileMode {
	if i.dir {
		return os.ModeDir | 0o755
	}
	return 0o644
}
func (i *jsFileInfo) ModTime() time.Time { return time.Time{} }
func (i *jsFileInfo) IsDir() bool        { return i.dir }
func (i *jsFileInfo) Sys() any           { return nil }
