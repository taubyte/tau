package tcc

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestGetSet(t *testing.T) {
	d := Doc{}
	Set(d, []string{"trigger", "type"}, "http")
	assert.Equal(t, Get(d, []string{"trigger", "type"}), "http")
	// missing path -> nil, non-map segment -> nil
	assert.Assert(t, Get(d, []string{"trigger", "nope"}) == nil)
	assert.Assert(t, Get(d, []string{"trigger", "type", "x"}) == nil)

	// empty / nil value deletes the key so blanks stay out of the YAML
	Set(d, []string{"trigger", "type"}, "")
	assert.Assert(t, Get(d, []string{"trigger", "type"}) == nil)
	Set(d, []string{"trigger"}, nil)
	assert.Assert(t, Get(d, []string{"trigger"}) == nil)
	Set(d, nil, "x") // no-op, no panic
}

// selectorSomewhere finds any kind with a branch selector, and the field whose
// write path follows it.
//
// NOTHING HERE NAMES A KIND. The first version picked Storage and asserted
// against `object` and `streaming`; the vocabulary moved and the test broke
// while the code was right. What matters is the behaviour of a selector,
// whichever kind happens to declare one.
func selectorSomewhere(t *testing.T) (*Form, Field, Field) {
	t.Helper()

	groups, err := Groups()
	assert.NilError(t, err)

	for _, g := range groups {
		f, err := FormFor(g.Def)
		assert.NilError(t, err)

		var sel Field
		for _, fd := range f.Fields {
			if fd.IsSelector && len(fd.Alternatives) > 1 {
				sel = fd
			}
		}
		if sel.Key == "" {
			continue
		}

		// A field under the branch: one that is dynamic and is not the
		// selector itself.
		for _, fd := range f.Fields {
			if !fd.IsSelector && len(fd.Alternatives) > 1 && len(fd.BranchSuffix) > 0 {
				return f, sel, fd
			}
		}
	}

	t.Fatal("no kind in this vocabulary declares a branch selector with a field under it")
	return nil, Field{}, Field{}
}

// TestABranchIsChosenAndTheWritePathFollows. Before anything is chosen the
// write path defaults to the first alternative; choosing another moves it and
// takes the abandoned branch's values with it.
func TestBranchHelpers(t *testing.T) {
	_, sel, under := selectorSomewhere(t)

	first := sel.Alternatives[0]
	second := sel.Alternatives[1]

	d := Doc{}
	assert.Equal(t, ActiveBranch(d, sel), "")
	assert.DeepEqual(t, WritePath(d, under), branchPath(under, first))

	SwitchBranch(d, sel, second)
	assert.Equal(t, ActiveBranch(d, sel), second)
	assert.DeepEqual(t, WritePath(d, under), branchPath(under, second))

	// THE ABANDONED BRANCH IS GONE. Leaving it behind would compile a document
	// that opens two branches of a switch, which the loader refuses — and the
	// author would be looking at a form that shows one.
	assert.Assert(t, Get(d, append(append([]string{}, under.BranchPrefix...), first)) == nil)
}

// branchPath is where a dynamic field writes under one alternative.
func branchPath(f Field, alternative string) []string {
	out := append([]string{}, f.BranchPrefix...)
	out = append(out, alternative)
	return append(out, f.BranchSuffix...)
}

// TestANonDynamicFieldWritesWhereItSays.
func TestAPlainFieldsWritePathIsItsPath(t *testing.T) {
	f, _, _ := selectorSomewhere(t)

	for _, fd := range f.Fields {
		if len(fd.Alternatives) > 0 {
			continue
		}
		assert.DeepEqual(t, WritePath(Doc{}, fd), fd.Path)
		return
	}
	t.Fatal("every field of this kind is dynamic, which cannot be right")
}

// TestAConditionalFieldIsHiddenUntilItsConditionHolds, wherever the vocabulary
// declares one.
func TestVisibility(t *testing.T) {
	groups, err := Groups()
	assert.NilError(t, err)

	checked := 0
	for _, g := range groups {
		f, err := FormFor(g.Def)
		assert.NilError(t, err)

		for _, fd := range f.Fields {
			c := fd.ShowWhen
			if c == nil || len(c.In) == 0 {
				continue
			}

			// Held: the deciding field carries a value the condition names.
			//
			// A DYNAMIC FIELD NEEDS ITS BRANCH OPEN AS WELL. Visibility is two
			// questions — is the show-when satisfied, and is this the branch
			// the document actually took — and a readiness probe is both: it
			// says `kind: http` and lives under `readiness/http`.
			held := Doc{}
			Set(held, c.Path, c.In[0])
			openBranch(f, held, fd)
			assert.Assert(t, f.Visible(fd, held),
				"%s.%s is hidden while %s is %q, which its own condition allows",
				g.Def, join(fd.Path), join(c.Path), c.In[0])

			// Not held: a value the condition does not name.
			denied := Doc{}
			Set(denied, c.Path, "a-value-no-condition-names")
			openBranch(f, denied, fd)
			assert.Assert(t, !f.Visible(fd, denied),
				"%s.%s is shown while %s is something its condition does not name",
				g.Def, join(fd.Path), join(c.Path))

			checked++
		}
	}

	assert.Assert(t, checked > 0, "nothing in the vocabulary is conditional, which cannot be right")
}

// openBranch puts the branch a field lives under into the document.
//
// THE BRANCH IS THE SELECTOR'S AND NOT THE FIELD'S. `readiness/http/port` is
// not itself dynamic — it is a plain field sitting under one alternative of the
// `readiness` selector — so what has to be opened is that selector's branch,
// found the same way Visible finds it.
func openBranch(form *Form, d Doc, fd Field) {
	for _, sel := range form.Selectors() {
		if !under(fd, sel) {
			continue
		}
		at := fd.Path[len(sel.BranchPrefix)]
		for _, alt := range sel.Alternatives {
			if at == alt {
				SwitchBranch(d, sel, alt)
			}
		}
	}
}

func field(f *Form, path string) Field {
	for _, fd := range f.Fields {
		if join(fd.Path) == path {
			return fd
		}
	}
	panic("no field " + path)
}
