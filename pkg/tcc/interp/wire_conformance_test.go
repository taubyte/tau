package interp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/schema"
	"gotest.tools/v3/assert"
)

// TestWireKeyConformance locks the Phase-0 invariant that every later pass-collapse
// phase depends on: the compiled wire key of a resource attribute is Tag() ?? Name.
// The engine stores each attribute under its bare Name; pass1 then Move()s the
// tagged ones to their Tag() value (pubsub-channel -> channel, git-provider ->
// provider, certificate-data -> cert-file, ...). A future generic driver will read
// Tag() to perform that rename mechanically, so this test freezes the mapping now.
//
// It compiles the frozen fixtures and, for every compiled resource instance,
// asserts:
//
//  1. no wire key exists that isn't Tag()??Name of some attribute (or a known
//     synthesized key: the EnumBool projection local/public, the DerivedBool
//     secure, the common name/description/tags, or the attaches-to-all smartops
//     list). id never survives — it is promoted to the object key.
//  2. no attribute's raw Name leaks where a Tag() renamed it (e.g. a function must
//     never carry "pubsub-channel", only "channel").
//  3. every declared Tag() value actually surfaces as a compiled wire key in the
//     fixtures.
//
// Coverage gaps (documented, not silently skipped):
//   - http-methods (Tag "methods") is NoStructField / unimplemented and absent from
//     every fixture, so its Tag value is excluded from assertion (3). Its raw-name
//     no-leak (2) and allowed-key (1) checks still apply.
//   - EnumBool granularity (network-access -> local/public) and the conditional
//     drop of p2p-protocol are exercised elsewhere; here they are only
//     required not to leak a raw name and not to introduce an unexpected key.
type groupRule struct {
	allowed map[string]bool   // every legal wire key for the group
	renamed map[string]string // raw Name -> Tag value, for attrs a Tag renames
}

func attrTag(a *engine.Attribute) (string, bool) {
	t, ok := a.Meta["tag"].(string)
	return t, ok && t != ""
}

func TestWireKeyConformance(t *testing.T) {
	compiler, err := schema.New(schema.WithLocal("../taubyte/v1/fixtures/config"), schema.WithBranch("master"))
	assert.NilError(t, err)

	obj, _, err := compiler.Compile(context.Background())
	assert.NilError(t, err)

	// common keys every resource carries; id is consumed (promoted to the map key).
	common := map[string]bool{"name": true, "description": true, "tags": true}

	// Build per-group expectations straight from the DSL, so the test cannot drift
	// from the schema it guards.
	rules := map[string]groupRule{}
	wantTags := map[string]string{} // Tag value -> "group.attr" (for failure messages)
	attachKey := ""                 // AttachesToAll list key (smartops)

	for _, g := range schema.GenerationRoot() {
		name, _ := g.Match.(string)
		if len(g.Children) == 0 {
			continue
		}
		iter := g.Children[0]
		if _, isResource := iter.Meta["resource"].([4]string); !isResource {
			continue // applications (container) + clouds have no per-attr wire shape
		}

		r := groupRule{allowed: map[string]bool{}, renamed: map[string]string{}}
		for k := range common {
			r.allowed[k] = true
		}
		for _, a := range iter.Attributes {
			if common[a.Name] || a.Name == "id" {
				continue
			}
			// An EnumBool attr projects to a bool under lower(goName) (network-access
			// -> local/public), replacing its own key. That is a separate mechanism
			// from Tag; here we only allow its projected key.
			if eb, ok := a.Meta["enumBool"].(engine.EnumBoolSpec); ok && eb.GoName != "" {
				r.allowed[strings.ToLower(eb.GoName)] = true
				continue
			}
			// A DerivedBool attr synthesizes a bool wire key (function secure) in
			// addition to keeping its own key.
			if d, ok := a.Meta["derivedBool"].(engine.DerivedBoolSpec); ok && d.GoName != "" {
				r.allowed[strings.ToLower(d.GoName)] = true
			}
			key := a.Name
			if tag, ok := attrTag(a); ok {
				key = tag
				if tag != a.Name {
					r.renamed[a.Name] = tag
				}
				if b, _ := a.Meta["noStructField"].(bool); !b {
					wantTags[tag] = name + "." + a.Name
				}
			}
			r.allowed[key] = true
		}
		if b, _ := iter.Meta["attachesToAll"].(bool); b {
			attachKey = name
		}
		rules[name] = r
	}
	// The attaches-to-all list (smartops) can ride on any resource.
	if attachKey != "" {
		for _, r := range rules {
			r.allowed[attachKey] = true
		}
	}

	seen := map[string]bool{} // every wire key observed, for the coverage check

	checkInstance := func(group string, inst map[string]any) {
		r, ok := rules[group]
		if !ok {
			return
		}
		for k := range inst {
			seen[k] = true
			assert.Assert(t, r.allowed[k],
				"group %q instance carries wire key %q, which is not Tag()??Name of any attribute nor a known synthesized key",
				group, k)
		}
		for raw, tag := range r.renamed {
			_, leaked := inst[raw]
			assert.Assert(t, !leaked,
				"group %q leaked raw attribute name %q as a wire key; it must be renamed to its Tag() %q",
				group, raw, tag)
		}
	}

	walkGroup := func(group string, groupMap map[string]any) {
		for _, instV := range groupMap {
			if inst, ok := instV.(map[string]any); ok {
				checkInstance(group, inst)
			}
		}
	}

	root := obj.Flat()["object"].(map[string]any)
	for gname, gv := range root {
		gm, ok := gv.(map[string]any)
		if !ok {
			continue // project-root scalar (id/name/description/email)
		}
		if gname == "applications" {
			// each application is a container of nested resource groups plus the
			// common scalar fields.
			for _, appV := range gm {
				app, ok := appV.(map[string]any)
				if !ok {
					continue
				}
				for k, v := range app {
					if sub, ok := v.(map[string]any); ok {
						walkGroup(k, sub) // k is a nested resource-group name
						continue
					}
					assert.Assert(t, common[k],
						"application container carries unexpected scalar key %q", k)
				}
			}
			continue
		}
		walkGroup(gname, gm)
	}

	// Every declared Tag() value (minus the documented NoStructField exclusion) must
	// have actually surfaced as a compiled wire key — proving the tags are real.
	assert.Assert(t, len(wantTags) > 0, "expected some tagged attributes in the schema")
	for tag, where := range wantTags {
		assert.Assert(t, seen[tag],
			"declared Tag(%q) on %s never appeared as a compiled wire key in the fixtures", tag, where)
	}
}
