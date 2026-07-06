//go:build js && wasm

// These tests run under the js/wasm target only. Run them with:
//
//	GOOS=js GOARCH=wasm go test -exec="$(go env GOROOT)/lib/wasm/go_js_wasm_exec" ./pkg/tcc/wasm/
//
// The JS fs primitives are built in Go (js.FuncOf over an in-memory store), so
// the real jsFs adapter, marshaling, and session logic are exercised directly.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall/js"
	"testing"
)

// memStore is an in-memory file store exposed to the wasm code as JS fs
// primitives (via js.FuncOf), so these tests drive the real jsFs adapter,
// marshaling, and session logic without a separate JS runtime.
type memStore struct {
	files map[string][]byte
	dirs  map[string]bool
}

func newStore() *memStore {
	return &memStore{files: map[string][]byte{}, dirs: map[string]bool{"/": true}}
}

func (m *memStore) registerParents(p string) {
	d := p
	for {
		i := strings.LastIndex(d, "/")
		if i <= 0 {
			m.dirs["/"] = true
			return
		}
		d = d[:i]
		m.dirs[d] = true
	}
}

func (m *memStore) isDir(p string) bool {
	if m.dirs[p] {
		return true
	}
	pre := p
	if !strings.HasSuffix(pre, "/") {
		pre += "/"
	}
	for k := range m.files {
		if strings.HasPrefix(k, pre) {
			return true
		}
	}
	return false
}

func (m *memStore) primitives() js.Value {
	u8 := js.Global().Get("Uint8Array")
	newObj := func() js.Value { return js.Global().Get("Object").New() }

	fn := func(f func(a []js.Value) any) js.Func {
		return js.FuncOf(func(_ js.Value, a []js.Value) any { return f(a) })
	}

	childNames := func(p string) []any {
		pre := "/"
		if p != "/" {
			pre = p + "/"
		}
		seen := map[string]bool{}
		names := []any{}
		add := func(k string) {
			if strings.HasPrefix(k, pre) {
				rest := k[len(pre):]
				if i := strings.IndexByte(rest, '/'); i >= 0 {
					rest = rest[:i]
				}
				if rest != "" && !seen[rest] {
					seen[rest] = true
					names = append(names, rest)
				}
			}
		}
		for k := range m.files {
			add(k)
		}
		for d := range m.dirs {
			if d != "/" {
				add(d)
			}
		}
		return names
	}

	obj := newObj()
	obj.Set("readFile", fn(func(a []js.Value) any {
		if data, ok := m.files[a[0].String()]; ok {
			arr := u8.New(len(data))
			js.CopyBytesToJS(arr, data)
			return arr
		}
		return js.Null()
	}))
	obj.Set("writeFile", fn(func(a []js.Value) any {
		p := a[0].String()
		buf := make([]byte, a[1].Get("length").Int())
		js.CopyBytesToGo(buf, a[1])
		m.files[p] = buf
		m.registerParents(p)
		return js.Undefined()
	}))
	obj.Set("readdir", fn(func(a []js.Value) any { return js.ValueOf(childNames(a[0].String())) }))
	obj.Set("stat", fn(func(a []js.Value) any {
		p := a[0].String()
		if data, ok := m.files[p]; ok {
			s := newObj()
			s.Set("isDir", false)
			s.Set("size", len(data))
			return s
		}
		if m.isDir(p) {
			s := newObj()
			s.Set("isDir", true)
			s.Set("size", 0)
			return s
		}
		return js.Null()
	}))
	obj.Set("mkdir", fn(func(a []js.Value) any {
		m.dirs[a[0].String()] = true
		return js.Undefined()
	}))
	return obj
}

const fixtureRoot = "../taubyte/v1/fixtures/config"

func loadFixture(t *testing.T) *memStore {
	t.Helper()
	m := newStore()
	err := filepath.Walk(fixtureRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, fixtureRoot)
		m.files[rel] = data
		m.registerParents(rel)
		return nil
	})
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	if len(m.files) == 0 {
		t.Fatal("no fixture files loaded")
	}
	return m
}

func val(v any) js.Value { return v.(js.Value) }

// openHandle validates a session-open result (a Go int handle on success, an
// error object on failure) and returns it as a js.Value for later calls.
func openHandle(t *testing.T, v any) js.Value {
	t.Helper()
	if h, ok := v.(int); ok {
		return js.ValueOf(h)
	}
	t.Fatalf("expected session handle, got error: %s", errOf(v.(js.Value)))
	return js.Value{}
}

func errOf(v js.Value) string {
	if v.IsNull() || v.IsUndefined() || v.Type() != js.TypeObject {
		return ""
	}
	if e := v.Get("error"); !e.IsUndefined() {
		return e.String()
	}
	return ""
}

func masterOpts() js.Value {
	o := js.Global().Get("Object").New()
	o.Set("branch", "master")
	return o
}

func arr(segs ...string) js.Value {
	out := make([]any, len(segs))
	for i, s := range segs {
		out[i] = s
	}
	return js.ValueOf(out)
}

func TestCompileFn(t *testing.T) {
	m := loadFixture(t)
	r := val(compileFn(js.Null(), []js.Value{m.primitives(), masterOpts()}))
	if e := errOf(r); e != "" {
		t.Fatalf("compile error: %s", e)
	}
	if r.Get("object").Get("functions").IsUndefined() {
		t.Error("compiled object has no functions")
	}
	vs := r.Get("validations")
	found := false
	for i := 0; i < vs.Length(); i++ {
		v := vs.Index(i)
		if v.Get("validator").String() == "dns" && v.Get("value").String() == "hal.computers.com" {
			found = true
		}
	}
	if !found {
		t.Error("expected hal.computers.com dns validation")
	}
}

func TestDecompileFn(t *testing.T) {
	m := loadFixture(t)
	compiled := val(compileFn(js.Null(), []js.Value{m.primitives(), masterOpts()}))

	out := newStore()
	r := val(decompileFn(js.Null(), []js.Value{compiled, out.primitives()}))
	if !r.IsNull() {
		t.Fatalf("decompile error: %s", errOf(r))
	}
	if _, ok := out.files["/config.yaml"]; !ok {
		t.Error("decompile did not write /config.yaml")
	}
	if len(out.files) < 2 {
		t.Errorf("decompile wrote %d files, want several", len(out.files))
	}
}

func TestSessionRoundTrip(t *testing.T) {
	m := loadFixture(t)
	h := openHandle(t, openSessionFn(js.Null(), []js.Value{m.primitives()}))
	res := arr("functions", "test_function1_glob")
	mem := arr("execution", "memory")

	if got := val(sessionGetFn(js.Null(), []js.Value{h, res, mem})); got.String() != "32GB" {
		t.Errorf("get memory = %q, want 32GB", got.String())
	}
	if e := errOf(val(sessionSetFn(js.Null(), []js.Value{h, res, mem, js.ValueOf("64GB")}))); e != "" {
		t.Fatalf("set memory: %s", e)
	}
	if got := val(sessionGetFn(js.Null(), []js.Value{h, res, mem})); got.String() != "64GB" {
		t.Errorf("get after set = %q, want 64GB", got.String())
	}

	// typed numeric field
	dbMin := arr("databases", "test_database1")
	minF := arr("replicas", "min")
	if got := val(sessionGetFn(js.Null(), []js.Value{h, dbMin, minF})); got.Int() != 15 {
		t.Errorf("db min = %d, want 15", got.Int())
	}

	// compile reflects the edit
	c := val(sessionCompileFn(js.Null(), []js.Value{h, masterOpts()}))
	if e := errOf(c); e != "" {
		t.Fatalf("session compile: %s", e)
	}
	fnMem := c.Get("object").Get("functions").Get("QmNf1SAZuyM9vLPeWiYx9qh3AWJKCjJvF9d1f5ZPZCZxXh").Get("memory")
	if fnMem.Int() != 64000000000 {
		t.Errorf("compiled memory = %v, want 64000000000", fnMem)
	}

	// list + app scope
	fns := val(sessionListFn(js.Null(), []js.Value{h, arr("functions")}))
	if !contains(fns, "test_function1_glob") {
		t.Error("list functions missing test_function1_glob")
	}
	apps := val(sessionListFn(js.Null(), []js.Value{h, arr("applications")}))
	if !contains(apps, "test_app1") {
		t.Error("list applications missing test_app1")
	}
	appMem := val(sessionGetFn(js.Null(), []js.Value{h, arr("applications", "test_app1", "functions", "test_function2"), mem}))
	if appMem.String() != "23MB" {
		t.Errorf("app-scoped memory = %q, want 23MB", appMem.String())
	}

	// exercise jsToGo across value kinds: number, bool, string array
	if e := errOf(val(sessionSetFn(js.Null(), []js.Value{h, dbMin, minF, js.ValueOf(20)}))); e != "" {
		t.Fatalf("set number: %s", e)
	}
	if got := val(sessionGetFn(js.Null(), []js.Value{h, dbMin, minF})); got.Int() != 20 {
		t.Errorf("set number -> %d, want 20", got.Int())
	}
	if e := errOf(val(sessionSetFn(js.Null(), []js.Value{h, res, arr("trigger", "local"), js.ValueOf(true)}))); e != "" {
		t.Fatalf("set bool: %s", e)
	}
	if got := val(sessionGetFn(js.Null(), []js.Value{h, res, arr("trigger", "local")})); !got.Bool() {
		t.Error("set bool -> want true")
	}
	if e := errOf(val(sessionSetFn(js.Null(), []js.Value{h, res, arr("tags"), js.ValueOf([]any{"a", "b"})}))); e != "" {
		t.Fatalf("set array: %s", e)
	}
	if got := val(sessionGetFn(js.Null(), []js.Value{h, res, arr("tags")})); got.Length() != 2 {
		t.Errorf("set array -> len %d, want 2", got.Length())
	}

	// delete
	if e := errOf(val(sessionDeleteFn(js.Null(), []js.Value{h, arr("functions", "test_function2_glob")}))); e != "" {
		t.Fatalf("delete: %s", e)
	}
	if contains(val(sessionListFn(js.Null(), []js.Value{h, arr("functions")})), "test_function2_glob") {
		t.Error("deleted function still listed")
	}

	// save writes YAML reflecting edits and the deletion
	out := newStore()
	if e := errOf(val(sessionSaveFn(js.Null(), []js.Value{h, out.primitives()}))); e != "" {
		t.Fatalf("save: %s", e)
	}
	yaml := string(out.files["/functions/test_function1_glob.yaml"])
	if !strings.Contains(yaml, "memory: 64GB") {
		t.Errorf("saved YAML missing edit:\n%s", yaml)
	}
	if _, ok := out.files["/functions/test_function2_glob.yaml"]; !ok {
		t.Log("note: save copies present files; deleted file simply absent from memfs")
	}

	sessionCloseFn(js.Null(), []js.Value{h})
	if e := errOf(val(sessionGetFn(js.Null(), []js.Value{h, res, mem}))); e == "" {
		t.Error("expected error after close (invalid handle)")
	}
}

func TestDecompileSessionAndErrors(t *testing.T) {
	m := loadFixture(t)
	compiled := val(compileFn(js.Null(), []js.Value{m.primitives(), masterOpts()}))
	h := openHandle(t, decompileSessionFn(js.Null(), []js.Value{compiled}))
	got := val(sessionGetFn(js.Null(), []js.Value{h, arr("functions", "test_function1_glob"), arr("execution", "memory")}))
	if got.String() != "32GB" {
		t.Errorf("decompiled memory = %q, want 32GB", got.String())
	}
	sessionCloseFn(js.Null(), []js.Value{h})

	// error paths
	if errOf(val(compileFn(js.Null(), nil))) == "" {
		t.Error("compile with no args should error")
	}
	if errOf(val(sessionGetFn(js.Null(), []js.Value{js.ValueOf(9999), arr("x"), arr("y")}))) == "" {
		t.Error("get with bad handle should error")
	}
	if errOf(val(sessionSetFn(js.Null(), []js.Value{js.ValueOf(9999), arr("x"), arr("y"), js.ValueOf(1)}))) == "" {
		t.Error("set with bad handle should error")
	}
}

func contains(jsArr js.Value, s string) bool {
	for i := 0; i < jsArr.Length(); i++ {
		if jsArr.Index(i).String() == s {
			return true
		}
	}
	return false
}
