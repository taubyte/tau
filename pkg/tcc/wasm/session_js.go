//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"math"
	"syscall/js"

	"github.com/spf13/afero"
	compiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1/schema"
	tccConvert "github.com/taubyte/tau/utils/tcc/convert"
)

// The editable-config session lives in pkg/tcc/session (exported as
// schema.Session), so the same code serves Go callers (tau-cli) and this wasm.
// Everything here is just JS<->Go marshaling over a handle table of sessions.

var (
	sessions   = map[int]*compiler.Session{}
	nextHandle = 1
)

func register(s *compiler.Session) int {
	h := nextHandle
	nextHandle++
	sessions[h] = s
	return h
}

func lookup(args []js.Value) (*compiler.Session, any) {
	if len(args) < 1 {
		return nil, errResult("missing session handle")
	}
	s, ok := sessions[args[0].Int()]
	if !ok {
		return nil, errResult("invalid or closed session handle")
	}
	return s, nil
}

// open(fsPrimitives) -> handle : stage a project's YAML into a private session.
func openSessionFn(_ js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("open: expected (fsPrimitives)")
	}
	s, err := compiler.NewSession(&jsFs{p: args[0]}, "/")
	if err != nil {
		return errResult("open: " + err.Error())
	}
	return register(s)
}

// decompileSession(compiledObject) -> handle : decompile into a private fs and
// open a session over it.
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
	s, err := compiler.AdoptSession(mem)
	if err != nil {
		return errResult(err.Error())
	}
	return register(s)
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
	v, err := s.Get(res, jsToPath(args[2]))
	if err != nil {
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
	if err := s.Set(res, jsToPath(args[2]), jsToGo(args[3])); err != nil {
		return errResult(err.Error())
	}
	return js.Null()
}

// delete(handle, resourcePath[], fieldPath[]?) : remove a resource, or a single
// field when fieldPath is given.
func sessionDeleteFn(_ js.Value, args []js.Value) any {
	s, e := lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 2 {
		return errResult("delete: expected (handle, resourcePath, fieldPath?)")
	}
	res := jsToPath(args[1])
	if len(res) == 0 {
		return errResult("delete: empty resource path")
	}
	var field []string
	if len(args) > 2 && args[2].Truthy() {
		field = jsToPath(args[2])
	}
	if err := s.Delete(res, field); err != nil {
		return errResult(err.Error())
	}
	return js.Null()
}

// list(handle, path[]) -> string[]
func sessionListFn(_ js.Value, args []js.Value) any {
	s, e := lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 2 {
		return errResult("list: expected (handle, path)")
	}
	p := jsToPath(args[1])
	if len(p) == 0 {
		return errResult("list: empty path")
	}
	names, err := s.List(p)
	if err != nil {
		return toJS([]any{}) // missing/empty folder -> []
	}
	out := make([]any, len(names))
	for i, n := range names {
		out[i] = n
	}
	return toJS(out)
}

func compileOpts(args []js.Value, optIdx int) compiler.CompileOptions {
	var o compiler.CompileOptions
	if len(args) > optIdx && args[optIdx].Truthy() {
		if v := args[optIdx].Get("branch"); v.Truthy() {
			o.Branch = v.String()
		}
		if v := args[optIdx].Get("cloud"); v.Truthy() {
			o.Cloud = v.String()
		}
	}
	return o
}

// compile(handle, opts?) -> { object, indexes, validations }
func sessionCompileFn(_ js.Value, args []js.Value) any {
	s, e := lookup(args)
	if e != nil {
		return e
	}
	obj, validations, err := s.Compile(context.Background(), compileOpts(args, 1))
	if err != nil {
		return errResult(err.Error())
	}
	out := obj.Flat()
	out["validations"] = validations
	return toJS(out)
}

// validate(handle, opts?) -> { validations } : whole-config diagnostics only.
func sessionValidateFn(_ js.Value, args []js.Value) any {
	s, e := lookup(args)
	if e != nil {
		return e
	}
	validations, err := s.Validate(context.Background(), compileOpts(args, 1))
	if err != nil {
		return errResult(err.Error())
	}
	return toJS(map[string]any{"validations": validations})
}

// validateField(handle, resourcePath[], fieldPath[], value) : run one field's
// single-value validator (enum/shape/cid/...) with no compile. null | { error }.
func sessionValidateFieldFn(_ js.Value, args []js.Value) any {
	s, e := lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 4 {
		return errResult("validateField: expected (handle, resourcePath, fieldPath, value)")
	}
	res := jsToPath(args[1])
	if len(res) == 0 {
		return errResult("validateField: empty resource path")
	}
	if err := s.ValidateField(res, jsToPath(args[2]), jsToGo(args[3])); err != nil {
		return errResult(err.Error())
	}
	return js.Null()
}

// validateResource(handle, resourcePath[]) : run every single-value validator of
// one resource, no compile. -> { errors: string[] } (empty = locally valid).
func sessionValidateResourceFn(_ js.Value, args []js.Value) any {
	s, e := lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 2 {
		return errResult("validateResource: expected (handle, resourcePath)")
	}
	res := jsToPath(args[1])
	if len(res) == 0 {
		return errResult("validateResource: empty resource path")
	}
	errs := s.ValidateResource(res)
	msgs := make([]any, len(errs))
	for i, er := range errs {
		msgs[i] = er.Error()
	}
	return toJS(map[string]any{"errors": msgs})
}

// complete(handle, resourcePath[], fieldPath[], partial?) : completion candidates
// for a field's value, filtered by the partial the user typed. -> string[].
func sessionCompleteFn(_ js.Value, args []js.Value) any {
	s, e := lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 3 {
		return errResult("complete: expected (handle, resourcePath, fieldPath, partial?)")
	}
	res := jsToPath(args[1])
	if len(res) == 0 {
		return errResult("complete: empty resource path")
	}
	partial := ""
	if len(args) > 3 && args[3].Truthy() {
		partial = args[3].String()
	}
	names, err := s.Complete(res, jsToPath(args[2]), partial)
	if err != nil {
		return errResult(err.Error())
	}
	out := make([]any, len(names))
	for i, n := range names {
		out[i] = n
	}
	return toJS(out)
}

// save(handle, fsPrimitives) : flush the session's YAML out to the fs.
func sessionSaveFn(_ js.Value, args []js.Value) any {
	s, e := lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 2 {
		return errResult("save: expected (handle, fsPrimitives)")
	}
	if err := s.Save(&jsFs{p: args[1]}, "/"); err != nil {
		return errResult("save: " + err.Error())
	}
	return js.Null()
}

// fork(handle) -> forkHandle : a copy-on-write child; edit + validate in
// isolation, then merge or close.
func sessionForkFn(_ js.Value, args []js.Value) any {
	s, e := lookup(args)
	if e != nil {
		return e
	}
	f, err := s.Fork()
	if err != nil {
		return errResult(err.Error())
	}
	return register(f)
}

// merge(forkHandle) : collapse a fork's validated changes onto its parent.
func sessionMergeFn(_ js.Value, args []js.Value) any {
	s, e := lookup(args)
	if e != nil {
		return e
	}
	if err := s.Merge(); err != nil {
		return errResult(err.Error())
	}
	return js.Null()
}

func sessionCloseFn(_ js.Value, args []js.Value) any {
	if len(args) >= 1 {
		if s, ok := sessions[args[0].Int()]; ok {
			s.Close()
		}
		delete(sessions, args[0].Int())
	}
	return js.Null()
}

// --- JS <-> Go marshaling helpers ---

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
