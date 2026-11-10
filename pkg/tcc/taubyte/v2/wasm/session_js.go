//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"syscall/js"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	tccConvert "github.com/taubyte/tau/utils/tcc/convert"
)

// An editable session (pkg/tcc/session via schema.Session) behind a handle table;
// this file is only JS<->Go marshaling. The config stays in wasm.

// bridge is the sessions JS has open, by handle. JS calls its methods, bound when
// main registers them.
type bridge struct {
	sessions map[int]*schema.Session
	next     int
}

func newBridge() *bridge { return &bridge{sessions: map[int]*schema.Session{}, next: 1} }

func (b *bridge) register(s *schema.Session) int {
	h := b.next
	b.next++
	b.sessions[h] = s
	return h
}

func (b *bridge) lookup(args []js.Value) (*schema.Session, any) {
	if len(args) < 1 {
		return nil, errResult("missing session handle")
	}
	s, ok := b.sessions[args[0].Int()]
	if !ok {
		return nil, errResult("invalid or closed session handle")
	}
	return s, nil
}

// errFrom is errResult plus the source position, when the error carries one:
// { error, file, line, column }. The message is unchanged, so a consumer that only
// reads it is unaffected — but one that wants to know WHICH file failed no longer
// has to substring-match the message.
func errFrom(err error) any {
	o := js.Global().Get("Object").New()
	o.Set("error", err.Error())
	var le *engine.LocatedError
	if errors.As(err, &le) && le.File != "" {
		o.Set("file", le.File)
		if le.Line > 0 {
			o.Set("line", le.Line)
		}
		if le.Column > 0 {
			o.Set("column", le.Column)
		}
	}
	return o
}

// openSession(fsPrimitives) -> handle : stage a project's YAML into a private
// session. The caller's filesystem is read once and never written until save.
func (b *bridge) openSessionFn(_ js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("openSession: expected (fsPrimitives)")
	}
	s, err := schema.NewSession(&jsFs{p: args[0]}, "/")
	if err != nil {
		return errResult("openSession: " + err.Error())
	}
	return b.register(s)
}

// decompileSession(compiledObject) -> handle : render a compiled object back to
// config on a private filesystem and open a session over it, so an object that
// arrived from anywhere becomes editable without a repo behind it.
func (b *bridge) decompileSessionFn(_ js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("decompileSession: expected (compiledObject)")
	}
	obj := tccConvert.MapToTCCObject(jsToMap(args[0]))
	mem := afero.NewMemMapFs()
	d, err := schema.NewDecompiler(schema.DecompileToVirtual(mem, "/"))
	if err != nil {
		return errResult(err.Error())
	}
	if err := d.Decompile(obj); err != nil {
		return errResult(err.Error())
	}
	s, err := schema.AdoptSession(mem)
	if err != nil {
		return errResult(err.Error())
	}
	return b.register(s)
}

// sessionGet(handle, resourcePath[], fieldPath[]) -> value | null(absent)
func (b *bridge) sessionGetFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
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
		// ABSENT IS NOT AN ERROR to whoever asked: an editor reading a field
		// nobody has filled in yet is the normal case, not a fault.
		return js.Null()
	}
	return toJS(v)
}

// sessionSet(handle, resourcePath[], fieldPath[], value)
func (b *bridge) sessionSetFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
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

// sessionDelete(handle, resourcePath[], fieldPath[]?) : the whole resource when no
// field is named, one field when there is.
func (b *bridge) sessionDeleteFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
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

// sessionList(handle, path[]) -> string[]
func (b *bridge) sessionListFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
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
		// A folder nobody has created yet holds nothing, which is what an
		// editor listing an empty kind should see.
		return toJS([]any{})
	}
	out := make([]any, len(names))
	for i, n := range names {
		out[i] = n
	}
	return toJS(out)
}

func compileOpts(args []js.Value, optIdx int) schema.CompileOptions {
	var o schema.CompileOptions
	if len(args) > optIdx && args[optIdx].Truthy() {
		if v := args[optIdx].Get("branch"); v.Truthy() {
			o.Branch = v.String()
		}
		if v := args[optIdx].Get("cloud"); v.Truthy() {
			o.Cloud = v.String()
		}
		if v := args[optIdx].Get("environment"); v.Truthy() {
			o.Environment = v.String()
		}
	}
	return o
}

// sessionCompile(handle, opts?) -> { object, indexes, validations }
func (b *bridge) sessionCompileFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
	if e != nil {
		return e
	}
	obj, validations, err := s.Compile(context.Background(), compileOpts(args, 1))
	if err != nil {
		return errFrom(err)
	}
	out := obj.Flat()
	out["validations"] = validations
	return toJS(out)
}

// sessionValidate(handle, opts?) -> { validations } : whole-config diagnostics only.
func (b *bridge) sessionValidateFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
	if e != nil {
		return e
	}
	validations, err := s.Validate(context.Background(), compileOpts(args, 1))
	if err != nil {
		return errFrom(err)
	}
	return toJS(map[string]any{"validations": validations})
}

// sessionValidateField(handle, resourcePath[], fieldPath[], value) : one field's
// single-value validator, no compile. null | { error }.
func (b *bridge) sessionValidateFieldFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
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

// sessionValidateResource(handle, resourcePath[]) -> { issues: [{field, message}] } :
// every single-value validator of one resource, no compile, each issue attributed to
// the field that failed. Empty means locally valid.
func (b *bridge) sessionValidateResourceFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
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
	issues := s.ValidateResource(res)
	if issues == nil {
		issues = []schema.FieldIssue{}
	}
	return toJS(map[string]any{"issues": issues})
}

// sessionSerialize(handle, resourcePath[]) -> { path, yaml } : one resource's
// document, comments preserved. The caller never names the file — where a document
// lives is the DSL's, and a caller that named it would read a path tcc never wrote.
func (b *bridge) sessionSerializeFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 2 {
		return errResult("serialize: expected (handle, resourcePath)")
	}
	res := jsToPath(args[1])
	if len(res) == 0 {
		return errResult("serialize: empty resource path")
	}
	path, data, err := s.Serialize(res)
	if err != nil {
		return errFrom(err)
	}
	return toJS(map[string]any{"path": path, "yaml": string(data)})
}

// sessionLocation(handle, resourcePath[]) -> string : where the document lives,
// without reading it. serialize() when the YAML is wanted; this when the path is.
func (b *bridge) sessionLocationFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 2 {
		return errResult("location: expected (handle, resourcePath)")
	}
	res := jsToPath(args[1])
	if len(res) == 0 {
		return errResult("location: empty resource path")
	}
	p, err := s.Location(res)
	if err != nil {
		return errFrom(err)
	}
	return js.ValueOf(p)
}

// sessionSetResource(handle, resourcePath[], doc) : make the document equal doc, as
// the minimal set/delete ops, so untouched YAML — comments included — survives.
func (b *bridge) sessionSetResourceFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 3 {
		return errResult("setResource: expected (handle, resourcePath, doc)")
	}
	res := jsToPath(args[1])
	if len(res) == 0 {
		return errResult("setResource: empty resource path")
	}
	doc, ok := jsToGo(args[2]).(map[string]any)
	if !ok {
		return errResult("setResource: doc must be an object")
	}
	if err := s.SetResource(res, doc); err != nil {
		return errFrom(err)
	}
	return js.Null()
}

// sessionResourceAt(handle, repoPath) -> string[] | null : the canonical address of
// the resource a repo-relative YAML path holds, null when it holds none.
func (b *bridge) sessionResourceAtFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 2 {
		return errResult("resourceAt: expected (handle, path)")
	}
	res, ok := s.ResourceAt(args[1].String())
	if !ok {
		return js.Null()
	}
	return toJS(strsToAny(res))
}

// sessionKinds(handle) -> [{name, group, container}] : the DSL's resource kinds, for
// a consumer that routes on a kind string rather than a static accessor.
func (b *bridge) sessionKindsFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
	if e != nil {
		return e
	}
	kinds := s.Kinds()
	if kinds == nil {
		kinds = []schema.Kind{}
	}
	return toJS(kinds)
}

// sessionAddress(handle, kind, name, app?) -> string[] : one resource's canonical
// address, by kind — no group-name table or plural rule on the caller's side.
func (b *bridge) sessionAddressFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 3 {
		return errResult("address: expected (handle, kind, name, app?)")
	}
	app := ""
	if len(args) > 3 && args[3].Truthy() {
		app = args[3].String()
	}
	res, err := s.Address(args[1].String(), args[2].String(), app)
	if err != nil {
		return errResult(err.Error())
	}
	return toJS(strsToAny(res))
}

// sessionNames(handle, kind, app?) -> string[] : the instances of a kind in scope.
func (b *bridge) sessionNamesFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 2 {
		return errResult("names: expected (handle, kind, app?)")
	}
	app := ""
	if len(args) > 2 && args[2].Truthy() {
		app = args[2].String()
	}
	names, err := s.Names(args[1].String(), app)
	if err != nil {
		return errResult(err.Error())
	}
	return toJS(strsToAny(names))
}

// sessionExists(handle, resourcePath[]) -> bool : what tells an editor whether it is
// creating a resource or editing one.
func (b *bridge) sessionExistsFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 2 {
		return errResult("exists: expected (handle, resourcePath)")
	}
	return js.ValueOf(s.Exists(jsToPath(args[1])))
}

// sessionGenerate(handle, resourcePath[], fieldPath[]) -> string : mint a value for
// a DSL-generated field, so no consumer carries its own "how do I make an id".
func (b *bridge) sessionGenerateFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
	if e != nil {
		return e
	}
	if len(args) < 3 {
		return errResult("generate: expected (handle, resourcePath, fieldPath)")
	}
	res := jsToPath(args[1])
	if len(res) == 0 {
		return errResult("generate: empty resource path")
	}
	v, err := s.Generate(res, jsToPath(args[2]))
	if err != nil {
		return errResult(err.Error())
	}
	return js.ValueOf(v)
}

// sessionComplete(handle, resourcePath[], fieldPath[], partial?) -> string[] :
// candidates for a field's value, filtered by what the user has typed.
func (b *bridge) sessionCompleteFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
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
	return toJS(strsToAny(names))
}

// sessionSave(handle, fsPrimitives) : flush the session's YAML back out.
func (b *bridge) sessionSaveFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
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

// sessionFork(handle) -> forkHandle : a copy-on-write child, so an edit can be made
// and validated in isolation before the parent ever sees it.
func (b *bridge) sessionForkFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
	if e != nil {
		return e
	}
	f, err := s.Fork()
	if err != nil {
		return errResult(err.Error())
	}
	return b.register(f)
}

// sessionMerge(forkHandle) : collapse a fork's validated changes onto its parent.
func (b *bridge) sessionMergeFn(_ js.Value, args []js.Value) any {
	s, e := b.lookup(args)
	if e != nil {
		return e
	}
	if err := s.Merge(); err != nil {
		return errResult(err.Error())
	}
	return js.Null()
}

func (b *bridge) sessionCloseFn(_ js.Value, args []js.Value) any {
	if len(args) >= 1 {
		if s, ok := b.sessions[args[0].Int()]; ok {
			s.Close()
		}
		delete(b.sessions, args[0].Int())
	}
	return js.Null()
}

func strsToAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
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
		if f == math.Trunc(f) { // integral -> int/int64, safe on a 32-bit int target
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
		// A PLAIN OBJECT NEEDS ITS OWN BRANCH. Without one every object
		// marshaled to nil, which silently blanked object-valued writes.
		keys := js.Global().Get("Object").Call("keys", v)
		out := make(map[string]any, keys.Length())
		for i := 0; i < keys.Length(); i++ {
			k := keys.Index(i).String()
			out[k] = jsToGo(v.Get(k))
		}
		return out
	}
	return nil
}

func jsToMap(v js.Value) map[string]any {
	str := js.Global().Get("JSON").Call("stringify", v).String()
	var m map[string]any
	json.Unmarshal([]byte(str), &m)
	return m
}
