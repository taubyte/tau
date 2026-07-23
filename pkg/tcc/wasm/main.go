//go:build js && wasm

// Command tcc-wasm exposes the Taubyte config compiler/decompiler to the
// browser. Built with `GOOS=js GOARCH=wasm`, it registers a `globalThis.tcc`
// object with `compile` and `decompile` functions. The filesystem is provided
// by the JS caller (see fs_js.go for the primitive contract); the
// compile/decompile core itself is unchanged.
package main

import (
	"context"
	"encoding/json"
	"syscall/js"

	compiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1/schema"
	tccConvert "github.com/taubyte/tau/utils/tcc/convert"
)

func main() {
	tcc := js.Global().Get("Object").New()
	// Stateless whole-repo ops.
	tcc.Set("compile", js.FuncOf(compileFn))
	tcc.Set("decompile", js.FuncOf(decompileFn))
	tcc.Set("schema", js.FuncOf(schemaFn))
	// Editable in-wasm sessions (see session_js.go): the config lives here as a
	// yaseer tree; TS getters/setters read/write fields by path.
	tcc.Set("openSession", js.FuncOf(openSessionFn))
	tcc.Set("decompileSession", js.FuncOf(decompileSessionFn))
	tcc.Set("sessionGet", js.FuncOf(sessionGetFn))
	tcc.Set("sessionSet", js.FuncOf(sessionSetFn))
	tcc.Set("sessionCompile", js.FuncOf(sessionCompileFn))
	tcc.Set("sessionValidate", js.FuncOf(sessionValidateFn))
	tcc.Set("sessionValidateField", js.FuncOf(sessionValidateFieldFn))
	tcc.Set("sessionValidateResource", js.FuncOf(sessionValidateResourceFn))
	tcc.Set("sessionSave", js.FuncOf(sessionSaveFn))
	tcc.Set("sessionDelete", js.FuncOf(sessionDeleteFn))
	tcc.Set("sessionList", js.FuncOf(sessionListFn))
	tcc.Set("sessionFork", js.FuncOf(sessionForkFn))
	tcc.Set("sessionMerge", js.FuncOf(sessionMergeFn))
	tcc.Set("sessionClose", js.FuncOf(sessionCloseFn))
	js.Global().Set("tcc", tcc)

	// Keep the Go runtime alive so the exported functions stay callable.
	select {}
}

// compileFn: compile(fsPrimitives, { branch?, cloud? }) ->
//
//	{ object, indexes, validations } on success, or { error } on failure.
func compileFn(_ js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("compile: expected (fsPrimitives, options?)")
	}
	fs := &jsFs{p: args[0]}

	branch := compiler.DefaultBranch
	cloud := ""
	if len(args) > 1 && args[1].Truthy() {
		if v := args[1].Get("branch"); v.Truthy() {
			branch = v.String()
		}
		if v := args[1].Get("cloud"); v.Truthy() {
			cloud = v.String()
		}
	}

	c, err := compiler.New(
		compiler.WithVirtual(fs, "/"),
		compiler.WithBranch(branch),
		compiler.WithCloud(cloud),
	)
	if err != nil {
		return errResult(err.Error())
	}

	obj, validations, err := c.Compile(context.Background())
	if err != nil {
		return errResult(err.Error())
	}

	// Flat() yields { object, indexes }; attach the external validations.
	out := obj.Flat()
	out["validations"] = validations
	return toJS(out)
}

// schemaFn: schema() -> the config JSON Schema (Draft 2020-12) as a JS object, or
// { error } on failure. Generated from the same DSL definition the compiler uses,
// so it always matches this wasm build — no separately-shipped schema to drift.
func schemaFn(_ js.Value, _ []js.Value) any {
	b, err := compiler.JSONSchema()
	if err != nil {
		return errResult(err.Error())
	}
	return js.Global().Get("JSON").Call("parse", string(b))
}

// decompileFn: decompile(compiledObject, fsPrimitives) -> null on success, or
// { error } on failure. `compiledObject` is the object returned by compile
// (the { object, indexes } shape); the rendered YAML is written back through
// the fs primitives.
func decompileFn(_ js.Value, args []js.Value) any {
	if len(args) < 2 {
		return errResult("decompile: expected (compiledObject, fsPrimitives)")
	}

	jsonStr := js.Global().Get("JSON").Call("stringify", args[0]).String()
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		return errResult("decompile: invalid compiled object: " + err.Error())
	}

	fs := &jsFs{p: args[1]}
	obj := tccConvert.MapToTCCObject(m)

	d, err := compiler.NewDecompiler(compiler.DecompilerWithVirtual(fs, "/"))
	if err != nil {
		return errResult(err.Error())
	}
	if err := d.Decompile(obj); err != nil {
		return errResult(err.Error())
	}
	return js.Null()
}

// toJS marshals a Go value to a real JS value via JSON (handles the typed
// slices/maps in the compiled object that js.ValueOf cannot).
func toJS(v any) js.Value {
	data, err := json.Marshal(v)
	if err != nil {
		return errResult("marshal: " + err.Error())
	}
	return js.Global().Get("JSON").Call("parse", string(data))
}

func errResult(msg string) js.Value {
	o := js.Global().Get("Object").New()
	o.Set("error", msg)
	return o
}
