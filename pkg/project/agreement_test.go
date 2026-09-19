package project_test

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/project"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// agreement_test holds the two readers of a compiled object to each other.
//
// # There are two, and there should be
//
// schema.Resources/Scopes walk the object TREE and are part of what tcc-gen
// publishes for the browser: the wasm client compiles in memory and reads back
// what it just made, with no register, no deployment and no index to descend.
//
// pkg/project descends the INDEX tcc wrote at compile time, because the runtime
// resolves a name per request and a walk of every resource in the project is not
// that. Neither is a copy of the other's code, and neither should become one.
//
// # What was missing is that nothing made them agree
//
// They encode "what is a resource" and "what is a scope" independently, and they
// have already disagreed: Scopes() reported a container-kind ACCESS ENTRY as a
// nested scope — an agent a workflow reaches, reported as an agent it contains —
// where the index walk never saw one. The explorer and the runtime were
// answering differently about the same project.
//
// So this compiles the vocabulary's own fixture and asserts the two answers are
// the same set. It is the check that makes two readers safe rather than the
// argument for having one.

// tree is what the schema-side walk reports, as scope/kind/name.
func tree(t *testing.T, flat map[string]any) []string {
	t.Helper()

	var out []string
	for _, r := range schema.Resources(flat) {
		out = append(out, r.Scope+"|"+r.Kind+"|"+r.Name)
	}
	sort.Strings(out)
	return out
}

// index is what the runtime-side reader reports, walking the same project.
func index(t *testing.T, p *project.Project) []string {
	t.Helper()

	var out []string
	var walk func(scope []string)
	walk = func(scope []string) {
		at := strings.Join(scope, "/")
		kinds, err := p.Kinds(scope)
		require.NoError(t, err)
		for _, kind := range kinds {
			names, err := p.Names(scope, kind)
			if err != nil {
				continue
			}
			for _, name := range names {
				out = append(out, at+"|"+kind+"|"+name)
			}
		}
		scopes, err := p.Scopes(scope)
		require.NoError(t, err)
		for _, inner := range scopes {
			walk(append(append([]string{}, scope...), inner))
		}
	}
	walk(nil)
	sort.Strings(out)
	return out
}

// TestBOTHREADERSOFACOMPILEDOBJECTREPORTTHESAMEPROJECT.
func TestTheTreeWalkAndTheIndexWalkAgree(t *testing.T) {
	raw := compiled(t)

	var flat map[string]any
	require.NoError(t, json.Unmarshal(raw, &flat))

	p, err := project.Load(raw)
	require.NoError(t, err)

	require.Equal(t, tree(t, flat), index(t, p),
		"the object walk and the index walk disagree about what this project contains - "+
			"one of them is what the explorer draws and the other is what the cloud serves")
}

// TestAZONEISREADOFFAREALCOMPILE. hosts.go spells `zone`, `fqdn` and `records`
// itself, and until this nothing held them to a compiled object: the dns
// service's tests inject zones through a seam, so they pass with fakes whatever
// this reader does. A renamed attribute would have produced a cloud
// authoritative for nothing, silently — every zone simply absent.
func TestZonesAreReadFromACompiledProject(t *testing.T) {
	zones := load(t).Zones()
	require.NotEmpty(t, zones, "the fixture declares a zone and this reader found none")

	var found bool
	for _, z := range zones {
		if z.Fqdn != "example.com" {
			continue
		}
		found = true
		require.NotEmpty(t, z.Records, "the zone was found and its records were not")
		require.Contains(t, z.Records[0], "IN MX",
			"the records are the zone-file lines the author wrote")
	}
	require.True(t, found, "the fixture's zone was not among %v", zones)
}
