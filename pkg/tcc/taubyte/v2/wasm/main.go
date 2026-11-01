//go:build js && wasm

// Command v2-wasm exposes the v2 DSL to the browser: compile, decompile, sessions,
// the JSON Schema and the verb table. Built with GOOS=js GOARCH=wasm.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall/js"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	tccConvert "github.com/taubyte/tau/utils/tcc/convert"
)

// decompile(compiledObject, fsPrimitives) : render a compiled object back to config,
// written through the caller's own filesystem. The compiler renders no YAML, so this
// and `generated` are the only places config is written at all.
func decompileFn(_ js.Value, args []js.Value) any {
	if len(args) < 2 {
		return errResult("decompile: expected (compiledObject, fsPrimitives)")
	}
	d, err := schema.NewDecompiler(schema.DecompileToVirtual(&jsFs{p: args[1]}, "/"))
	if err != nil {
		return errResult(err.Error())
	}
	if err := d.Decompile(tccConvert.MapToTCCObject(jsToMap(args[0]))); err != nil {
		return errResult(err.Error())
	}
	return js.Null()
}

func main() {
	api := js.Global().Get("Object").New()
	api.Set("compile", js.FuncOf(compileFn))
	api.Set("schema", js.FuncOf(schemaFn))
	api.Set("verbs", js.FuncOf(verbsFn))
	api.Set("kinds", js.FuncOf(kindsFn))
	// Computing a derived value is the engine's, so a consumer asks rather than
	// reimplementing the derivation it just read about.
	api.Set("derive", js.FuncOf(deriveFn))
	// Where a document lives and which fields a kind derives, read off the schema.
	api.Set("shapes", js.FuncOf(shapesFn))
	// What a resource nobody wrote would be, as config: a full decompile.
	api.Set("generated", js.FuncOf(generatedFn))
	// A compiled object back to config, written through the caller's own filesystem.
	api.Set("decompile", js.FuncOf(decompileFn))

	// A session keeps the config resident here behind a handle, so the page holds no
	// stale parsed copy and comments survive edits. See session_js.go.
	api.Set("openSession", js.FuncOf(openSessionFn))
	api.Set("decompileSession", js.FuncOf(decompileSessionFn))
	api.Set("sessionGet", js.FuncOf(sessionGetFn))
	api.Set("sessionSet", js.FuncOf(sessionSetFn))
	api.Set("sessionDelete", js.FuncOf(sessionDeleteFn))
	api.Set("sessionList", js.FuncOf(sessionListFn))
	api.Set("sessionCompile", js.FuncOf(sessionCompileFn))
	api.Set("sessionViews", js.FuncOf(sessionViewsFn))
	api.Set("sessionValidate", js.FuncOf(sessionValidateFn))
	api.Set("sessionValidateField", js.FuncOf(sessionValidateFieldFn))
	api.Set("sessionValidateResource", js.FuncOf(sessionValidateResourceFn))
	api.Set("sessionSerialize", js.FuncOf(sessionSerializeFn))
	api.Set("sessionLocation", js.FuncOf(sessionLocationFn))
	api.Set("sessionSetResource", js.FuncOf(sessionSetResourceFn))
	api.Set("sessionResourceAt", js.FuncOf(sessionResourceAtFn))
	api.Set("sessionKinds", js.FuncOf(sessionKindsFn))
	api.Set("sessionAddress", js.FuncOf(sessionAddressFn))
	api.Set("sessionNames", js.FuncOf(sessionNamesFn))
	api.Set("sessionExists", js.FuncOf(sessionExistsFn))
	api.Set("sessionGenerate", js.FuncOf(sessionGenerateFn))
	api.Set("sessionComplete", js.FuncOf(sessionCompleteFn))
	api.Set("sessionSave", js.FuncOf(sessionSaveFn))
	api.Set("sessionFork", js.FuncOf(sessionForkFn))
	api.Set("sessionMerge", js.FuncOf(sessionMergeFn))
	api.Set("sessionClose", js.FuncOf(sessionCloseFn))
	// The one global name both the npm client and the explorer read.
	js.Global().Set("tcc", api)
	select {}
}

func errResult(msg string) any {
	o := js.Global().Get("Object").New()
	o.Set("error", msg)
	return o
}

// toJS round-trips through JSON so Go maps, slices and typed scalars land as plain
// JS values. int64 nanoseconds become numbers, which is what the UI wants to show.
func toJS(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return errResult(err.Error())
	}
	return js.Global().Get("JSON").Call("parse", string(b))
}

// sourceFs accepts either shape: the explorer passes { path: contents }, the npm
// client passes sync filesystem primitives. Object.keys over a filesystem would
// yield its method names as files.
func sourceFs(v js.Value) (afero.Fs, error) {
	if isSyncFs(v) {
		return &jsFs{p: v}, nil
	}
	fs := afero.NewMemMapFs()
	keys := js.Global().Get("Object").Call("keys", v)
	for i := 0; i < keys.Length(); i++ {
		path := keys.Index(i).String()
		if err := afero.WriteFile(fs, "/"+path, []byte(v.Get(path).String()), 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return fs, nil
}

// compile(files | fsPrimitives, { branch }) ->
//
//	{ object, validations } | { error }
func compileFn(_ js.Value, args []js.Value) any {
	if len(args) < 1 || !args[0].Truthy() {
		return errResult("compile: expected (files, options?)")
	}

	fs, err := sourceFs(args[0])
	if err != nil {
		return errResult(err.Error())
	}

	branch := schema.DefaultBranch
	if len(args) > 1 && args[1].Truthy() {
		if v := args[1].Get("branch"); v.Truthy() {
			branch = v.String()
		}
	}

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch(branch))
	if err != nil {
		return errResult(err.Error())
	}
	// AS WRITTEN, first: that is what the page shows and what the author is editing.
	obj, validations, err := c.Authored(context.Background())
	if err != nil {
		return errResult(err.Error())
	}
	// And as it runs, where lowered resources exist; the page needs both, so its
	// editor never mixes uneditable resources into what was written.
	loweredObj, _, err := c.Compile(context.Background())
	if err != nil {
		return errResult(err.Error())
	}
	lowered := loweredObj.Flat()

	// The documents the second pass wrote, as read back; nil when nothing lowers.
	loweredFiles := map[string]any{}
	if fs := c.LoweredFS(); fs != nil {
		afero.Walk(fs, "/", func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return err
			}
			body, err := afero.ReadFile(fs, path)
			if err != nil {
				return err
			}
			loweredFiles[strings.TrimPrefix(path, "/")] = string(body)
			return nil
		})
	}

	// Flat() is { object, indexes }, siblings of validations, as the client's
	// CompileResult declares.
	flat := obj.Flat()
	out := map[string]any{}
	for k, v := range flat {
		out[k] = v
	}
	root, _ := flat["object"].(map[string]any)
	if root == nil {
		root = flat
	}
	if id, ok := root["id"].(string); ok && id != "" {
		out["projectLabel"] = schema.ProjectLabel(id)
	}
	// The audit runs here, not in the page, so its reasoning is not duplicated in
	// TypeScript. The stub resolver: what tcc cannot see, a browser cannot either.
	flatObj := obj.Flat()
	// Over what runs: a broken lowering is undeployable config.
	findings := schema.Audit(lowered, schema.StubResolver())
	found := make([]map[string]any, 0, len(findings))
	for _, f := range findings {
		found = append(found, map[string]any{
			"severity": string(f.Severity), "kind": f.Kind, "name": f.Name,
			"scope": f.Scope, "title": f.Title, "detail": f.Detail, "id": f.ID,
		})
	}
	out["findings"] = found
	out["loweredFiles"] = loweredFiles

	// What nobody wrote and where it came from, for when a box is opened.
	rec := interp.LoweredOf(lowered)
	made := map[string]any{}
	for id, p := range rec.Resources {
		made[id] = map[string]any{
			"from": p.From, "at": p.At, "kind": p.Kind, "name": p.Name, "scope": p.Scope,
		}
	}
	granted := []map[string]any{}
	for _, g := range rec.Entries {
		path := make([]any, 0, len(g.Path))
		for _, seg := range g.Path {
			path = append(path, seg)
		}
		granted = append(granted, map[string]any{
			"on": g.On, "path": path, "value": g.Value, "from": g.From,
		})
	}
	loweredResources := []map[string]any{}
	for _, r := range schema.Resources(lowered) {
		loweredResources = append(loweredResources, map[string]any{
			"id": r.ID, "kind": r.Kind, "name": r.Name, "scope": r.Scope, "fields": r.Fields,
		})
	}
	loweredRelations := []map[string]any{}
	for _, r := range schema.Relations(lowered) {
		loweredRelations = append(loweredRelations, map[string]any{
			"from": r.From, "to": r.To, "kind": r.Kind,
			"verb": r.Verb, "effect": r.Effect, "class": string(r.Class),
		})
	}
	out["lowered"] = map[string]any{
		"produced": made, "granted": granted,
		"resources": loweredResources, "relations": loweredRelations,
	}

	// What each resource is reached at, assembled by the schema; the cloud's fqdn
	// stays absent.
	names := map[string]any{}
	for id, r := range schema.ReachableNames(flatObj) {
		names[id] = map[string]any{
			"name": r.Name, "zone": r.Zone, "fqdn": r.Fqdn, "generated": r.Generated,
		}
	}
	out["names"] = names

	// The resources and their namespaces, from the schema's walk.
	res := []map[string]any{}
	for _, r := range schema.Resources(flatObj) {
		res = append(res, map[string]any{
			"id": r.ID, "kind": r.Kind, "name": r.Name, "scope": r.Scope, "fields": r.Fields,
		})
	}
	out["resources"] = res

	// The scopes, which Resources() does not list, with their kinds for filling
	// LayoutOf().Scope.
	scopes := []map[string]any{}
	for _, s := range schema.Scopes(flatObj) {
		scopes = append(scopes, map[string]any{
			"id": s.ID, "kind": s.Kind, "name": s.Name, "scope": s.Scope, "fields": s.Fields,
		})
	}
	out["scopes"] = scopes

	// Every relationship, already resolved.
	rels := []map[string]any{}
	for _, r := range schema.Relations(flatObj) {
		rels = append(rels, map[string]any{
			"from": r.From, "to": r.To, "kind": r.Kind,
			"verb": r.Verb, "effect": r.Effect, "class": string(r.Class),
		})
	}
	out["relations"] = rels

	// One view per region, cut as a deploy publishes.
	var opts js.Value
	if len(args) > 1 {
		opts = args[1]
	}
	out["views"] = viewsResult(lowered, "", regionsFrom(opts, lowered))

	// Always present, empty when there are none: the client types it as a list.
	vs := make([]map[string]any, 0, len(validations))
	for _, v := range validations {
		vs = append(vs, map[string]any{"key": v.Key, "value": v.Value, "validator": v.Validator})
	}
	out["validations"] = vs
	return toJS(out)
}

// schema() -> the Draft 2020-12 JSON Schema for the whole v2 DSL.
//
// The document's identity and its vendor prefix are the DSL's, so they are asked
// for rather than restated here: a second set of options in a client is a second
// answer to what this schema IS, and this one drifted — it left ExtPrefix empty,
// which the emitter reads as the generic "x-", so the page and every Go reader of
// a tau schema disagreed about the name of the same key.
func schemaFn(js.Value, []js.Value) any {
	b, err := schema.JSONSchema()
	if err != nil {
		return errResult(err.Error())
	}
	return js.Global().Get("JSON").Call("parse", string(b))
}

// verbs() -> [{kind, verbs: [{name, effect, delivered, inbound}]}] — read back off each
// kind's own Ops() declaration, so the UI cannot show a verb the compiler would reject.
// `delivered` and `inbound` are two facts, not one: delivered says the platform PUSHES
// (so a container needs a port), inbound says the edge points at the declarer.
func verbsFn(js.Value, []js.Value) any {
	kinds := schema.Kinds()
	out := make([]map[string]any, 0, len(kinds))
	for _, kind := range kinds {
		ops := schema.OpsOf(kind)
		vs := make([]map[string]any, 0, len(ops))
		for _, v := range ops {
			vs = append(vs, map[string]any{
				"name": v.Name, "effect": v.Effect,
				"delivered": v.Delivered, "inbound": v.Delivered || v.Inbound,
			})
		}
		out = append(out, map[string]any{"kind": kind, "verbs": vs})
	}
	return toJS(out)
}

// kinds() -> [{group, singular, container, category, doc, icon, fields}] read off the schema.
func shapesFn(js.Value, []js.Value) any {
	// Only what a consumer cannot infer: where a document lives, and which fields a
	// kind derives.
	kinds := map[string]any{}
	for _, g := range schema.KindRoot() {
		key, _ := g.Match.(string)
		if key == "" || len(g.Children) == 0 {
			continue
		}
		derived := []map[string]any{}
		for _, a := range g.Children[0].Attributes {
			kindOf, _ := a.Meta["derived"].(string)
			if kindOf == "" {
				continue
			}
			from, _ := a.Meta["derivedFrom"].([]string)
			inputs := []any{}
			for _, f := range from {
				inputs = append(inputs, f)
			}
			label, _ := a.Meta["doc"].([2]string)
			derived = append(derived, map[string]any{
				"field": a.Name, "derivation": kindOf, "from": inputs, "label": label[0],
			})
		}
		if len(derived) > 0 {
			kinds[key] = map[string]any{"derived": derived}
		}
	}

	layout := schema.LayoutOf()
	return toJS(map[string]any{
		"kinds": kinds,
		"layout": map[string]any{"resource": layout.Resource, "scope": layout.Scope,
			"leaf": layout.Leaf, "content": layout.Content},
	})
}
func deriveFn(_ js.Value, args []js.Value) any {
	if len(args) < 1 {
		return errResult("derive: expected (derivation, ...inputs)")
	}
	inputs := make([]string, 0, len(args)-1)
	for _, a := range args[1:] {
		inputs = append(inputs, a.String())
	}
	v, err := engine.Derive(args[0].String(), inputs...)
	if err != nil {
		return ""
	}
	return v
}

func kindsFn(js.Value, []js.Value) any {
	out := []map[string]any{}
	for _, g := range schema.GenerationRoot() {
		name, _ := g.Match.(string)
		if len(g.Children) == 0 {
			continue
		}
		iter := g.Children[0]
		doc, _ := iter.Meta["doc"].(string)
		icon, _ := iter.Meta["icon"].(string)
		category, _ := iter.Meta["category"].(string)
		singular, _ := iter.Meta["singular"].(string)
		if r, ok := iter.Meta["resource"].([4]string); ok {
			singular = r[2]
		}
		fields := []string{}
		for _, a := range iter.Attributes {
			switch a.Name {
			case "id", "name", "description", "tags":
				continue
			}
			if len(a.Name) > 4 && a.Name[:4] == "use-" {
				continue
			}
			fields = append(fields, a.Name)
		}
		// how this kind is reachable, if at all: by an access declaration from
		// another resource, by a gateway route, or neither
		reach := []string{}
		if len(schema.OpsOf(name)) > 0 {
			reach = append(reach, "access")
		}
		if schema.ServedByRoute(name) {
			reach = append(reach, "route")
		}
		// `name` is the declared singular, lowercased, matching the session's kinds() and
		// loader.ts's Kind type.
		out = append(out, map[string]any{
			"group": name, "name": strings.ToLower(singular), "container": iter.Group,
			"category": category, "doc": doc, "icon": icon, "fields": fields,
			"reach": reach,
		})
	}
	return toJS(out)
}

// generated(files, {branch}) -> { "<path>": "<yaml>", ... } | { error }
//
// A full decompile with the authored documents dropped: exactly the config nobody
// wrote. Separate from compile because nothing needs it until a box is opened.
func generatedFn(_ js.Value, args []js.Value) any {
	if len(args) < 1 || !args[0].Truthy() {
		return errResult("generated: expected (files, options?)")
	}

	fs := afero.NewMemMapFs()
	files := args[0]
	authored := map[string]bool{}
	keys := js.Global().Get("Object").Call("keys", files)
	for i := 0; i < keys.Length(); i++ {
		path := keys.Index(i).String()
		authored["/"+path] = true
		if err := afero.WriteFile(fs, "/"+path, []byte(files.Get(path).String()), 0o644); err != nil {
			return errResult("writing " + path + ": " + err.Error())
		}
	}

	branch := schema.DefaultBranch
	if len(args) > 1 && args[1].Truthy() {
		if v := args[1].Get("branch"); v.Truthy() {
			branch = v.String()
		}
	}

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch(branch))
	if err != nil {
		return errResult(err.Error())
	}
	obj, _, err := c.Compile(context.Background())
	if err != nil {
		return errResult(err.Error())
	}

	// A private filesystem: decompiling writes documents, and the page's own files are
	// not the decompiler's to touch.
	into := afero.NewMemMapFs()
	d, err := schema.NewDecompiler(schema.DecompileToVirtual(into, "/"), schema.WithAsLowered())
	if err != nil {
		return errResult(err.Error())
	}
	if err := d.Decompile(obj); err != nil {
		return errResult(err.Error())
	}

	out := map[string]any{}
	err = afero.Walk(into, "/", func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || authored[path] {
			return nil
		}
		body, err := afero.ReadFile(into, path)
		if err != nil {
			return err
		}
		out[strings.TrimPrefix(path, "/")] = string(body)
		return nil
	})
	if err != nil {
		return errResult(err.Error())
	}
	return toJS(out)
}
