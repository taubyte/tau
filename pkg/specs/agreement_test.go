package specs_test

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// Two readers of a compiled object, held to each other. schema.Resources/Scopes
// walk the object tree, for the wasm client reading back what it just compiled;
// pkg/specs descends the index tcc wrote, for per-request resolution. They encode
// "resource" and "scope" independently and have disagreed before, so this asserts
// both report the same set for the vocabulary's fixture.

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
func index(t *testing.T, p *specs.Project) []string {
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

func TestTheTreeWalkAndTheIndexWalkAgree(t *testing.T) {
	raw := compiled(t)

	var flat map[string]any
	require.NoError(t, json.Unmarshal(raw, &flat))

	p, err := specs.Load(raw)
	require.NoError(t, err)

	require.Equal(t, tree(t, flat), index(t, p),
		"the object walk and the index walk disagree about what this project contains - "+
			"one of them is what the explorer draws and the other is what the cloud serves")
}

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

		// The fixture declares `file:`, and the compile reads the file into the object, so
		// these are its lines in order, comments and blanks included. Not Records[0]: a real
		// zone file opens with a comment.
		joined := strings.Join(z.Records, "\n")
		require.Regexp(t, `IN\s+MX`, joined, "the records are the zone-file lines the author wrote")
		require.Contains(t, joined, ";", "and nothing stripped the author's comments")
		require.Contains(t, joined, "include:_spf.google.com",
			"including a record written across lines, which a list of strings in yaml could not carry")
	}
	require.True(t, found, "the fixture's zone was not among %v", zones)
}
