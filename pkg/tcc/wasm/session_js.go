//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"syscall/js"

	"github.com/spf13/afero"
	compiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1/schema"
	seer "github.com/taubyte/tau/pkg/yaseer"
	tccConvert "github.com/taubyte/tau/utils/tcc/convert"
)

// A session is a wasm-resident, editable representation of a project's SOURCE
// config: a yaseer document tree over a private in-memory filesystem. The TS
// getters/setters read/write fields against it by path; YAML is only ever
// crossed here (on open and save). This is the "decompile into an editable
// in-memory representation, edit via getters/setters" model.
type session struct {
	fs   afero.Fs // the memfs the seer owns
	seer *seer.Seer
}

var (
	sessions   = map[int]*session{}
	nextHandle = 1
)

func newSession(fs afero.Fs) (int, error) {
	s, err := seer.New(seer.VirtualFS(fs, "/"))
	if err != nil {
		return 0, err
	}
	h := nextHandle
	nextHandle++
	sessions[h] = &session{fs: fs, seer: s}
	return h, nil
}

// open(fsPrimitives) -> handle : stage a project's YAML into a private memfs and
// open an editable session over it.
func openSessionFn(_ js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("open: expected (fsPrimitives)")
	}
	mem := afero.NewMemMapFs()
	if err := copyTree(&jsFs{p: args[0]}, mem); err != nil {
		return errResult("open: " + err.Error())
	}
	h, err := newSession(mem)
	if err != nil {
		return errResult(err.Error())
	}
	return h
}

// decompileSession(compiledObject) -> handle : decompile a compiled object into
// a private memfs and open an editable session over it.
func decompileSessionFn(_ js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("decompile: expected (compiledObject)")
	}
	obj := tccConvert.MapToTCCObject(jsToMap(args[0]))
	mem := afero.NewMemMapFs()
	d, err := compiler.NewDecompiler(compiler.DecompilerWithVirtual(mem, "/"))
	if err != nil {
		return errResult(err.Error())
	}
	if err := d.Decompile(obj); err != nil {
		return errResult(err.Error())
	}
	h, err := newSession(mem)
	if err != nil {
		return errResult(err.Error())
	}
	return h
}

func lookup(args []js.Value) (*session, any) {
	if len(args) < 1 {
		return nil, errResult("missing session handle")
	}
	s, ok := sessions[args[0].Int()]
	if !ok {
		return nil, errResult("invalid or closed session handle")
	}
	return s, nil
}

// queryField navigates seer to a field: down the resource path ([dir, id]) to the
// file, into it with Document(), then down the in-doc field path — mirroring how
// pkg/schema/basic scopes (root().Document().Get(...)).
func queryField(s *session, res, field []string) *seer.Query {
	q := s.seer.Get(res[0])
	for _, seg := range res[1:] {
		q = q.Get(seg)
	}
	q = q.Document()
	for _, seg := range field {
		q = q.Get(seg)
	}
	return q
}

// get(handle, resourcePath[], fieldPath[]) -> value | null(absent)
func sessionGetFn(_ js.Value, args []js.Value) any {
	s, e := lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 3 {
		return errResult("get: expected (handle, resourcePath, fieldPath)")
	}
	res := jsToPath(args[1])
	if len(res) == 0 {
		return errResult("get: empty resource path")
	}
	var v any
	if err := queryField(s, res, jsToPath(args[2])).Value(&v); err != nil {
		return js.Null() // absent -> undefined
	}
	return toJS(v)
}

// set(handle, resourcePath[], fieldPath[], value)
func sessionSetFn(_ js.Value, args []js.Value) any {
	s, e := lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 4 {
		return errResult("set: expected (handle, resourcePath, fieldPath, value)")
	}
	res := jsToPath(args[1])
	if len(res) == 0 {
		return errResult("set: empty resource path")
	}
	if err := queryField(s, res, jsToPath(args[2])).Set(jsToGo(args[3])).Commit(); err != nil {
		return errResult(err.Error())
	}
	return js.Null()
}

// compileSession(handle, opts?) -> { object, indexes, validations }
func sessionCompileFn(_ js.Value, args []js.Value) any {
	s, e := lookup(args)
	if e != nil {
		return e
	}
	if err := s.seer.Sync(); err != nil {
		return errResult(err.Error())
	}
	branch, cloud := compiler.DefaultBranch, ""
	if len(args) > 1 && args[1].Truthy() {
		if v := args[1].Get("branch"); v.Truthy() {
			branch = v.String()
		}
		if v := args[1].Get("cloud"); v.Truthy() {
			cloud = v.String()
		}
	}
	c, err := compiler.New(compiler.WithVirtual(s.fs, "/"), compiler.WithBranch(branch), compiler.WithCloud(cloud))
	if err != nil {
		return errResult(err.Error())
	}
	obj, validations, err := c.Compile(context.Background())
	if err != nil {
		return errResult(err.Error())
	}
	out := obj.Flat()
	out["validations"] = validations
	return toJS(out)
}

// saveSession(handle, fsPrimitives) : flush the in-memory YAML out to the fs.
func sessionSaveFn(_ js.Value, args []js.Value) any {
	s, e := lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 2 {
		return errResult("save: expected (handle, fsPrimitives)")
	}
	if err := s.seer.Sync(); err != nil {
		return errResult(err.Error())
	}
	if err := copyTree(s.fs, &jsFs{p: args[1]}); err != nil {
		return errResult("save: " + err.Error())
	}
	return js.Null()
}

// delete(handle, resourcePath[]) : remove a whole resource (mirrors Resource.Delete()).
func sessionDeleteFn(_ js.Value, args []js.Value) any {
	s, e := lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 2 {
		return errResult("delete: expected (handle, resourcePath)")
	}
	res := jsToPath(args[1])
	if len(res) == 0 {
		return errResult("delete: empty resource path")
	}
	q := s.seer.Get(res[0])
	for _, seg := range res[1:] {
		q = q.Get(seg)
	}
	if err := q.Delete().Commit(); err != nil {
		return errResult(err.Error())
	}
	return js.Null()
}

// list(handle, path[]) -> string[] : the names under a folder (resource names, or
// application names for ["applications"]). Mirrors project/list.go's seer.List().
func sessionListFn(_ js.Value, args []js.Value) any {
	s, e := lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 2 {
		return errResult("list: expected (handle, path)")
	}
	path := jsToPath(args[1])
	if len(path) == 0 {
		return errResult("list: empty path")
	}
	q := s.seer.Get(path[0])
	for _, seg := range path[1:] {
		q = q.Get(seg)
	}
	names, err := q.List()
	if err != nil {
		return toJS([]any{}) // missing/empty folder -> []
	}
	out := make([]any, len(names))
	for i, n := range names {
		out[i] = n
	}
	return toJS(out)
}

func sessionCloseFn(_ js.Value, args []js.Value) any {
	if len(args) >= 1 {
		delete(sessions, args[0].Int())
	}
	return js.Null()
}

// --- helpers ---

func copyTree(src, dst afero.Fs) error {
	return afero.Walk(src, "/", func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return dst.MkdirAll(p, 0o755)
		}
		data, err := afero.ReadFile(src, p)
		if err != nil {
			return err
		}
		return afero.WriteFile(dst, p, data, 0o644)
	})
}

func jsToPath(v js.Value) []string {
	n := v.Length()
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = v.Index(i).String()
	}
	return out
}

func jsToGo(v js.Value) any {
	switch v.Type() {
	case js.TypeString:
		return v.String()
	case js.TypeBoolean:
		return v.Bool()
	case js.TypeNumber:
		f := v.Float()
		if f == math.Trunc(f) { // integral -> int/int64 (32-bit-int target safe)
			i := int64(f)
			if int64(int32(i)) == i {
				return int(i)
			}
			return i
		}
		return f
	case js.TypeObject:
		if v.InstanceOf(js.Global().Get("Array")) {
			n := v.Length()
			out := make([]any, n)
			for i := 0; i < n; i++ {
				out[i] = jsToGo(v.Index(i))
			}
			return out
		}
	}
	return nil
}

func jsToMap(v js.Value) map[string]any {
	str := js.Global().Get("JSON").Call("stringify", v).String()
	var m map[string]any
	_ = json.Unmarshal([]byte(str), &m)
	return m
}
