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
	"syscall/js"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/jsonschema"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

func main() {
	api := js.Global().Get("Object").New()
	api.Set("compile", js.FuncOf(compileFn))
	api.Set("schema", js.FuncOf(schemaFn))
	api.Set("verbs", js.FuncOf(verbsFn))
	api.Set("kinds", js.FuncOf(kindsFn))
	api.Set("omissions", js.FuncOf(omissionsFn))
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
	obj, validations, err := c.Compile(context.Background())
	if err != nil {
		return errResult(err.Error())
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
	findings := schema.Audit(obj.Flat(), schema.StubResolver())
	found := make([]map[string]any, 0, len(findings))
	for _, f := range findings {
		found = append(found, map[string]any{
			"severity": string(f.Severity), "kind": f.Kind, "name": f.Name,
			"scope": f.Scope, "title": f.Title, "detail": f.Detail,
		})
	}
	out["findings"] = found

	// What the lowered resources become, and how they are wired to each other. Computed
	// here for the same reason the audit is: it is a fact about the vocabulary, and a
	// second implementation in the page would drift the first time a lowering changed.
	// It is also what lets the page show the INSIDE of a workflow without knowing what
	// a workflow is.
	plan := schema.LoweringPlan(obj.Flat())
	lowered := make([]map[string]any, 0, len(plan))
	for _, d := range plan {
		links := make([]map[string]any, 0, len(d.Links))
		for _, l := range d.Links {
			link := map[string]any{
				"kind": l.Kind, "name": l.Name, "verb": l.Verb, "scope": l.Scope,
				"effect": l.Effect, "ref": l.Ref,
			}
			// where the edge STARTS, when that is not the derived resource — a queue
			// does not hand work to another queue, the function that consumed it does
			if l.From.Kind != "" {
				link["from"] = map[string]any{"kind": l.From.Kind, "name": l.From.Name}
			}
			links = append(links, link)
		}
		lowered = append(lowered, map[string]any{
			"kind": d.Kind, "name": d.Name, "of": d.Of, "scope": d.Scope, "why": d.Why, "links": links,
			// deterministic, and the document it would be — a resource nobody wrote is
			// still a resource somebody may need to read
			"id": d.ID, "yaml": d.Yaml(),
			// Where it came from, so the page can show the construct as written as well
			// as what it becomes.
			"source": map[string]any{
				"kind": d.Source.Kind, "name": d.Source.Name, "scope": d.Source.Scope,
				"path": d.Source.Path, "part": d.Source.Part,
			},
		})
	}
	out["lowered"] = lowered

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
