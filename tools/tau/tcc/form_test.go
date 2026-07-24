package tcc

import (
	"testing"

	"gotest.tools/v3/assert"
)

// The DSL is the source of truth, so these assert the shape the driver reads
// out of the live schema rather than any hand-maintained list.

func TestGroups(t *testing.T) {
	groups, err := Groups()
	assert.NilError(t, err)

	byDir := map[string]Group{}
	for _, g := range groups {
		byDir[g.Dir] = g
	}
	// a leaf kind, a repo-less one, and the container
	assert.Equal(t, byDir["functions"].Name, "function")
	assert.Equal(t, byDir["functions"].Def, "Function")
	assert.Equal(t, byDir["functions"].Container, false)
	assert.Equal(t, byDir["applications"].Container, true)
	// clouds is a map but its entries aren't a $def resource -> not a group
	_, hasClouds := byDir["clouds"]
	assert.Assert(t, !hasClouds)
}

func TestGroupForUnknown(t *testing.T) {
	_, err := GroupFor("nonesuch")
	assert.ErrorContains(t, err, "no resource kind")
}

func TestFormForFunction(t *testing.T) {
	f, err := FormFor("Function")
	assert.NilError(t, err)

	byPath := map[string]Field{}
	for _, fd := range f.Fields {
		byPath[join(fd.Path)] = fd
	}

	// id is read-only, name is not an edited field
	assert.Equal(t, byPath["id"].Widget, WidgetCID)
	_, hasName := byPath["name"]
	assert.Assert(t, !hasName)

	// enum -> select, its members come from the DSL
	typ := byPath["trigger/type"]
	assert.Equal(t, typ.Widget, WidgetSelect)
	assert.DeepEqual(t, typ.Enum, []string{"http", "https", "pubsub", "p2p"})

	// a reference list, a scalar, and a bool switch
	assert.Equal(t, byPath["trigger/domains"].Widget, WidgetRefList)
	assert.Equal(t, byPath["trigger/domains"].Ref.Group, "domains")
	assert.Equal(t, byPath["execution/timeout"].Widget, WidgetScalar)
	assert.Equal(t, byPath["execution/timeout"].Scalar, "duration")
	assert.Equal(t, byPath["trigger/local"].Widget, WidgetSwitch)

	// the DSL's section order is preserved and http section is conditional
	assert.Assert(t, len(f.Sections) > 0)
	var http Section
	for _, s := range f.Sections {
		if s.ID == "http" {
			http = s
		}
	}
	assert.Assert(t, http.ShowWhen != nil)
	assert.DeepEqual(t, http.ShowWhen.In, []string{"http", "https"})
}

func TestFormForUnknown(t *testing.T) {
	_, err := FormFor("Nope")
	assert.ErrorContains(t, err, "no schema definition")
}

// A storage's type is a dynamic map key ({object|streaming}) and size lives
// under the chosen branch — the selector and the branch-suffix field.
func TestFormDynamicBranch(t *testing.T) {
	f, err := FormFor("Storage")
	assert.NilError(t, err)

	var typ, size Field
	for _, fd := range f.Fields {
		switch join(fd.Path) {
		case "type":
			typ = fd
		case "size":
			size = fd
		}
	}
	assert.Equal(t, typ.Widget, WidgetBranchSelect)
	assert.Equal(t, typ.IsSelector, true)
	assert.DeepEqual(t, typ.Alternatives, []string{"object", "streaming"})

	assert.DeepEqual(t, size.BranchPrefix, []string{})
	assert.DeepEqual(t, size.Alternatives, []string{"object", "streaming"})
	assert.DeepEqual(t, size.BranchSuffix, []string{"size"})
	assert.Equal(t, size.IsSelector, false)

	assert.DeepEqual(t, f.Selectors(), []Field{typ})
}

// Two fields with the same leaf key get their parent segment as a qualifier so
// their flags don't collide (bridges/mqtt/enable vs bridges/websocket/enable).
func TestFlagCollision(t *testing.T) {
	f, err := FormFor("Messaging")
	assert.NilError(t, err)
	flags := map[string]int{}
	for _, fd := range f.Fields {
		flags[fd.Flag]++
	}
	for flag, n := range flags {
		assert.Equal(t, n, 1, "flag %q assigned %d times", flag, n)
	}
}

func TestParseDynamic(t *testing.T) {
	prefix, alts, suffix, sel := parseDynamic("{object|streaming}/size")
	assert.DeepEqual(t, prefix, []string{})
	assert.DeepEqual(t, alts, []string{"object", "streaming"})
	assert.DeepEqual(t, suffix, []string{"size"})
	assert.Equal(t, sel, false)

	prefix, alts, _, sel = parseDynamic("source/{github}")
	assert.DeepEqual(t, prefix, []string{"source"})
	assert.DeepEqual(t, alts, []string{"github"})
	assert.Equal(t, sel, true)

	_, _, _, sel = parseDynamic("no/branch/here")
	assert.Equal(t, sel, false)
}

func join(p []string) string {
	out := ""
	for i, s := range p {
		if i > 0 {
			out += "/"
		}
		out += s
	}
	return out
}
