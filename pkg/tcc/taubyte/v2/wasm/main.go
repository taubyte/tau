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

// compile({ "functions/checkout.yaml": "...", ... }, { branch }) ->
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

	out := map[string]any{"object": obj.Flat()}
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

// verbs() -> [{kind, verbs: [{name, effect}]}] — read back off each kind's own Ops()
// declaration, so the UI cannot show a verb the compiler would reject.
func verbsFn(js.Value, []js.Value) any {
	kinds := schema.Kinds()
	out := make([]map[string]any, 0, len(kinds))
	for _, kind := range kinds {
		ops := schema.OpsOf(kind)
		vs := make([]map[string]any, 0, len(ops))
		for _, v := range ops {
			vs = append(vs, map[string]any{"name": v.Name, "effect": v.Effect})
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
		out = append(out, map[string]any{
			"group": name, "singular": singular, "container": iter.Group,
			"category": category, "doc": doc, "icon": icon, "fields": fields,
		})
	}
	return toJS(out)
}
