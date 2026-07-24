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

func TestBranchHelpers(t *testing.T) {
	f, err := FormFor("Storage")
	assert.NilError(t, err)
	var sel Field
	for _, fd := range f.Fields {
		if fd.IsSelector {
			sel = fd
		}
	}

	d := Doc{}
	// nothing chosen yet -> WritePath defaults to the first alternative
	assert.Equal(t, ActiveBranch(d, sel), "")
	size := field(f, "size")
	assert.DeepEqual(t, WritePath(d, size), []string{"object", "size"})

	// choose streaming: the write path follows, object branch is gone
	SwitchBranch(d, sel, "streaming")
	assert.Equal(t, ActiveBranch(d, sel), "streaming")
	assert.DeepEqual(t, WritePath(d, size), []string{"streaming", "size"})
	assert.Assert(t, Get(d, []string{"object"}) == nil)

	// a non-dynamic field's write path is just its path
	assert.DeepEqual(t, WritePath(d, field(f, "match")), []string{"match"})
}

func TestVisibility(t *testing.T) {
	f, err := FormFor("Function")
	assert.NilError(t, err)

	method := field(f, "trigger/method") // section "http", show-when type in {http,https}
	channel := field(f, "trigger/channel")

	d := Doc{"trigger": map[string]any{"type": "http"}}
	assert.Assert(t, f.Visible(method, d))
	assert.Assert(t, !f.Visible(channel, d)) // pubsub section hidden

	d = Doc{"trigger": map[string]any{"type": "pubsub"}}
	assert.Assert(t, !f.Visible(method, d))
	assert.Assert(t, f.Visible(channel, d))
}

// A plain field under a dynamic branch is visible only while its branch is
// active, even though it carries no explicit show-when.
func TestBranchLeafVisibility(t *testing.T) {
	f, err := FormFor("Storage")
	assert.NilError(t, err)
	versioning := field(f, "object/versioning")
	ttl := field(f, "streaming/ttl")

	d := Doc{"object": map[string]any{}}
	assert.Assert(t, f.Visible(versioning, d))
	assert.Assert(t, !f.Visible(ttl, d))

	d = Doc{"streaming": map[string]any{}}
	assert.Assert(t, !f.Visible(versioning, d))
	assert.Assert(t, f.Visible(ttl, d))
}

func TestShapes(t *testing.T) {
	// repo-backed by shape
	web, _ := FormFor("Website")
	repo := web.Repo()
	assert.Assert(t, repo != nil)
	assert.Equal(t, repo.Fullname, "fullname")
	doc := Doc{"source": map[string]any{"github": map[string]any{"fullname": "u/r"}}}
	assert.DeepEqual(t, repo.Under(doc, repo.Fullname), []string{"source", "github", "fullname"})
	assert.Assert(t, !web.CodeBacked())

	// code-backed by shape
	fn, _ := FormFor("Function")
	assert.Assert(t, fn.Repo() == nil)
	assert.Assert(t, fn.CodeBacked())

	// neither
	db, _ := FormFor("Database")
	assert.Assert(t, db.Repo() == nil)
	assert.Assert(t, !db.CodeBacked())
}

func field(f *Form, path string) Field {
	for _, fd := range f.Fields {
		if join(fd.Path) == path {
			return fd
		}
	}
	panic("no field " + path)
}
