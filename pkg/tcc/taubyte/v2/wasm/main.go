//go:build js && wasm

// Command v2-wasm exposes the v2 DSL to the browser: compile a set of YAML files,
// read the JSON Schema, read the verb table. Built with GOOS=js GOARCH=wasm.
//
// It takes the config as a plain {path: contents} object rather than a JS
// filesystem, because the explorer edits the tree in memory — no lightning-fs, no
// hydration step, and every compile is over exactly what is on screen.
package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"syscall/js"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/jsonschema"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

func main() {
	api := js.Global().Get("Object").New()
	api.Set("compile", js.FuncOf(compileFn))
	api.Set("schema", js.FuncOf(schemaFn))
	api.Set("verbs", js.FuncOf(verbsFn))
	api.Set("kinds", js.FuncOf(kindsFn))
	// Computing a derived value is the engine's, so a consumer asks rather than
	// reimplementing the derivation it just read about.
	api.Set("derive", js.FuncOf(deriveFn))
	api.Set("omissions", js.FuncOf(omissionsFn))
	// The shapes a consumer cannot infer: where a document lives, and which fields a kind
	// derives. Read off the schema, so a graph, a CLI or a console handles a vocabulary
	// it has never seen.
	api.Set("shapes", js.FuncOf(shapesFn))
	// What a resource nobody wrote WOULD be, as config: decompile, all the way. The
	// compiler renders no yaml — writing config is the decompiler's job, and this is the
	// only place that does it.
	api.Set("generated", js.FuncOf(generatedFn))
	js.Global().Set("tccV2", api)
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

// compile({ "function/checkout.yaml": "...", ... }, { branch }) ->
//
//	{ object, validations } | { error }
func compileFn(_ js.Value, args []js.Value) any {
	if len(args) < 1 || !args[0].Truthy() {
		return errResult("compile: expected (files, options?)")
	}

	fs := afero.NewMemMapFs()
	files := args[0]
	keys := js.Global().Get("Object").Call("keys", files)
	for i := 0; i < keys.Length(); i++ {
		path := keys.Index(i).String()
		body := files.Get(path).String()
		if err := afero.WriteFile(fs, "/"+path, []byte(body), 0o644); err != nil {
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
	// AS WRITTEN, first: that is what the page shows and what the author is editing.
	obj, validations, err := c.Authored(context.Background())
	if err != nil {
		return errResult(err.Error())
	}
	// ...and as it RUNS. Compiling lowers, so this is where the queues a workflow becomes
	// exist. Two compiles rather than one because the page needs both: an editor that
	// only had the second would show its user resources they cannot edit, mixed in with
	// the ones they wrote.
	loweredObj, _, err := c.Compile(context.Background())
	if err != nil {
		return errResult(err.Error())
	}
	lowered := loweredObj.Flat()

	// THE LOWERED FILESYSTEM ITSELF — every document the second pass actually wrote, not
	// the compiled object's shape of it: a relocated source's own leaf document, and each
	// produced child, exactly as read back. Nil for a project that lowers nothing, which
	// LoweredFS says by returning nil rather than an empty filesystem.
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

	// The project's label in a cloud's generated zone is a pure function of its id, so
	// it can be shown rather than guessed at. Only the cloud's own fqdn is unknown
	// here, and that stays unknown.
	flat := obj.Flat()
	out := map[string]any{"object": flat}
	if root, ok := flat["object"].(map[string]any); ok {
		flat = root
	}
	if id, ok := flat["id"].(string); ok && id != "" {
		out["projectLabel"] = schema.ProjectLabel(id)
	}
	// The audit runs HERE, not in the page. Every one of its checks needs a second
	// resource to answer, and the compiled object is what has them — so a second
	// implementation in TypeScript would be the same reasoning written twice, drifting
	// the first time a kind changed. The page renders findings; it does not decide
	// them.
	//
	// The stub resolver: what tcc cannot see — is this zone delegated, is this image
	// pullable — nothing in a browser can see either.
	flatObj := obj.Flat()
	// The audit runs over what RUNS: a lowering that produced something broken is a
	// config that cannot be deployed, and an author told their own files are fine would
	// find that out from a failed deploy.
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

	// WHAT NOBODY WROTE, and where it came from. The page draws the project as it was
	// WRITTEN — that is what an author is editing — and asks for the lowered one only
	// when a box is opened. Both are the same shapes, because a produced resource is an
	// ordinary resource; the difference is only which of them exist.
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

	// WHAT EACH RESOURCE IS REACHED AT. A name is the label it chose, the zone that
	// answers for it and the project's own label — three places, so whoever assembles
	// them decides what the name is, and a view assembling its own is a second answer.
	// The cloud's fqdn stays absent, because it is not in the config.
	names := map[string]any{}
	for id, r := range schema.ReachableNames(flatObj) {
		names[id] = map[string]any{
			"name": r.Name, "zone": r.Zone, "fqdn": r.Fqdn, "generated": r.Generated,
		}
	}
	out["names"] = names

	// WHAT RESOURCES THERE ARE, and the namespace each lives in. Finding them means
	// telling a nested group from an access block from a list, which is schema knowledge:
	// the walk lives with the schema and every consumer gets the same answer.
	res := []map[string]any{}
	for _, r := range schema.Resources(flatObj) {
		res = append(res, map[string]any{
			"id": r.ID, "kind": r.Kind, "name": r.Name, "scope": r.Scope, "fields": r.Fields,
		})
	}
	out["resources"] = res

	// AND WHAT THE SCOPES ARE, which is a different question from what resources there
	// are: Resources() lists what is INSIDE a scope, on purpose. A scope path is a path
	// of NAMES and a document address is a path of DIRECTORIES named after the kind —
	// `shop/checkout` is `namespace/shop/workflow/checkout` — so a consumer filling in
	// LayoutOf().Scope's `{kind}` asks here rather than assuming every level is the one
	// container it happens to know about.
	scopes := []map[string]any{}
	for _, s := range schema.Scopes(flatObj) {
		scopes = append(scopes, map[string]any{
			"id": s.ID, "kind": s.Kind, "name": s.Name, "scope": s.Scope, "fields": s.Fields,
		})
	}
	out["scopes"] = scopes

	// EVERY RELATIONSHIP BETWEEN TWO RESOURCES, already resolved: a name is resolved
	// against a scope, and a consumer that does its own resolving is one that will
	// disagree with the compiler about what a name means.
	rels := []map[string]any{}
	for _, r := range schema.Relations(flatObj) {
		rels = append(rels, map[string]any{
			"from": r.From, "to": r.To, "kind": r.Kind,
			"verb": r.Verb, "effect": r.Effect, "class": string(r.Class),
		})
	}
	out["relations"] = rels

	if len(validations) > 0 {
		vs := make([]map[string]any, 0, len(validations))
		for _, v := range validations {
			vs = append(vs, map[string]any{"key": v.Key, "value": v.Value, "validator": v.Validator})
		}
		out["validations"] = vs
	}
	return toJS(out)
}

// schema() -> the Draft 2020-12 JSON Schema for the whole v2 DSL.
func schemaFn(js.Value, []js.Value) any {
	b, err := jsonschema.GenerateJSONSchema(schema.GenerationRoot(), jsonschema.JSONSchemaOptions{
		Title:       "tau v2 configuration",
		Description: "The v2 DSL and its constraints.",
	})
	if err != nil {
		return errResult(err.Error())
	}
	return js.Global().Get("JSON").Call("parse", string(b))
}

// verbs() -> [{kind, verbs: [{name, effect, delivered}]}] — read back off each kind's own Ops()
// declaration, so the UI cannot show a verb the compiler would reject.
func verbsFn(js.Value, []js.Value) any {
	kinds := schema.Kinds()
	out := make([]map[string]any, 0, len(kinds))
	for _, kind := range kinds {
		ops := schema.OpsOf(kind)
		vs := make([]map[string]any, 0, len(ops))
		for _, v := range ops {
			vs = append(vs, map[string]any{"name": v.Name, "effect": v.Effect, "delivered": v.Delivered})
		}
		out = append(out, map[string]any{"kind": kind, "verbs": vs})
	}
	return toJS(out)
}

// omissions() -> what the vocabulary deliberately does NOT have, and why. Served
// beside kinds() because a closed enum is only honest if its closure is legible.
func omissionsFn(js.Value, []js.Value) any {
	out := make([]map[string]any, 0, len(schema.Omissions))
	for _, o := range schema.Omissions {
		out = append(out, map[string]any{
			"name": o.Name, "state": string(o.State),
			"why": o.Why, "instead": o.Instead, "unblocks": o.Unblocks,
		})
	}
	return toJS(out)
}

// kinds() -> [{group, singular, container, category, doc, icon, fields}] read off the schema.
func shapesFn(js.Value, []js.Value) any {
	// WHAT A CONSUMER CANNOT INFER, and nothing else. Where a document lives, and which
	// fields a kind derives — the second because deriving one is the engine's, so a
	// consumer asks rather than reimplementing a derivation it just read about.
	//
	// Four more facts were here: every reference a kind declares, the blocks that hand
	// traffic on, what an access entry is called, and where each kind states the name it
	// answers on. Nothing reads them now — tcc answers those questions directly, as
	// resources, relations and names — and a shape nobody reads is a second definition
	// waiting to disagree with the first.
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
		"kinds":  kinds,
		"layout": map[string]any{"resource": layout.Resource, "scope": layout.Scope, "leaf": layout.Leaf},
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
		out = append(out, map[string]any{
			"group": name, "singular": singular, "container": iter.Group,
			"category": category, "doc": doc, "icon": icon, "fields": fields,
			"reach": reach,
		})
	}
	return toJS(out)
}

// generated(files, {branch}) -> { "<path>": "<yaml>", ... } | { error }
//
// Decompiling ALL THE WAY: the authored documents plus the ones a lowering produced. The
// authored half is thrown away here — the page already has those, they are what the user
// is editing — so what comes back is exactly the config nobody wrote.
//
// It is a separate call, made when a box is opened rather than on every keystroke,
// because it is a whole decompile and nothing needs it until somebody looks.
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
