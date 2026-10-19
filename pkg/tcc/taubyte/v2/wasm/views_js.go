//go:build js && wasm

package main

import (
	"context"
	"syscall/js"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// THE VIEWS, as every consumer of this module gets them: the explorer through compile,
// a console through a session. Both ask schema.Views, which is what a deploy publishes,
// so no page cuts a project its own way.
//
// WHICH REGIONS is the caller's to say, and there are three answers:
//
//	(nothing)        the build with no regions — one view, `default`, the OSS deploy
//	"lenient"        the same, saying (not refusing) a region the project names
//	"named"          a preview of every region the project names
//	["eu", "us"]     a preview of exactly these
//
// A preview refuses no region; the build with none refuses a project it cannot place,
// which is reported as the views' error beside everything else the compile returned.
func regionsFrom(opts js.Value, flat map[string]any) schema.Regions {
	if opts.IsUndefined() || opts.IsNull() || !opts.Truthy() {
		return schema.NoRegion()
	}
	v := opts.Get("regions")
	switch {
	case v.IsUndefined() || v.IsNull():
		return schema.NoRegion()
	case v.Type() == js.TypeString && v.String() == "named":
		return schema.Preview(schema.RegionsNamed(flat)...)
	case v.Type() == js.TypeString && v.String() == "lenient":
		return schema.TryOneRegion()
	case v.Type() == js.TypeString:
		return schema.NoRegion()
	}
	return schema.Preview(jsToPath(v)...)
}

// viewsResult is { destinations, named, views: { <region>: { object, roles, empty } },
// notes, refused? }.
func viewsResult(flat map[string]any, environment string, r schema.Regions) map[string]any {
	opts := []schema.AuditOption{schema.WithRegions(r)}
	if environment != "" {
		opts = append(opts, schema.AuditIn(environment))
	}
	views, findings, err := schema.Views(flat, opts...)
	notes := make([]map[string]any, 0, len(findings))
	for _, f := range findings {
		notes = append(notes, map[string]any{
			"severity": string(f.Severity), "kind": f.Kind, "name": f.Name,
			"scope": f.Scope, "title": f.Title, "detail": f.Detail, "id": f.ID,
		})
	}
	out := map[string]any{
		"destinations": r.Serves(),
		"named":        schema.RegionsNamed(flat),
		"notes":        notes,
	}
	if err != nil {
		out["refused"] = err.Error()
		return out
	}
	byRegion := map[string]any{}
	for region, v := range views {
		roles := map[string]any{}
		for id, role := range v.Roles {
			roles[id] = string(role)
		}
		byRegion[region] = map[string]any{"object": v.Object, "roles": roles, "empty": v.Empty()}
	}
	out["views"] = byRegion
	return out
}

// sessionViews(handle, { branch, environment, regions }) ->
//
//	{ canonical, destinations, named, views, notes, refused? } | { error }
//
// The canonical object travels with the views: an editor still needs all of it.
func sessionViewsFn(_ js.Value, args []js.Value) any {
	s, e := lookup(args)
	if e != nil {
		return e
	}
	opts := compileOpts(args, 1)
	obj, _, err := s.Compile(context.Background(), opts)
	if err != nil {
		return errFrom(err)
	}
	flat := obj.Flat()
	var raw js.Value
	if len(args) > 1 {
		raw = args[1]
	}
	out := viewsResult(flat, opts.Environment, regionsFrom(raw, flat))
	out["canonical"] = flat
	return toJS(out)
}
