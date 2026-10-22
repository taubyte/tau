package interp

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

func TestInlineReadsThePointedAtFileAsLines(t *testing.T) {
	flat, _, err := compileFiles(t, map[string]string{
		"/project.yaml":   "id: root\n",
		"/site/w.yaml":    "id: QmW\nfile: zones/w.zone\n",
		"/site/gone.yaml": "id: QmGone\nfile: zones/missing.zone\n",
		"/site/none.yaml": "id: QmNone\n",
		"/zones/w.zone":   "; comment\r\n@ IN A 1.2.3.4\r\n\r\nwww IN CNAME @\n",
	})
	assert.NilError(t, err)
	sites := flat["site"].(map[string]any)
	assert.DeepEqual(t, sites["QmW"].(map[string]any)["records"], []any{"; comment", "@ IN A 1.2.3.4", "", "www IN CNAME @"})
	_, read := sites["QmGone"].(map[string]any)["records"]
	assert.Assert(t, !read, "an unreachable file is left for the mount protocol, not refused or invented")
	_, read = sites["QmNone"].(map[string]any)["records"]
	assert.Assert(t, !read)
}

func TestInlinedFilesGivesBackWhatTheCompileRead(t *testing.T) {
	project, root := driverSchema()
	fs := afero.NewMemMapFs()
	for path, body := range map[string]string{
		"/project.yaml":   "id: root\n",
		"/site/w.yaml":    "id: QmW\nfile: zones/w.zone\n",
		"/site/gone.yaml": "id: QmGone\nfile: zones/missing.zone\n",
		"/zones/w.zone":   "a\nb",
	} {
		assert.NilError(t, afero.WriteFile(fs, path, []byte(body), 0o644))
	}
	c, err := New(project, root, WithVirtual(fs, "/"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	assert.DeepEqual(t, InlinedFiles(root, obj), map[string][]string{"zones/w.zone": {"a", "b"}})
}

func TestInlinedInReadsEveryMarkInOrder(t *testing.T) {
	iter := engine.DefineIter([]*engine.Attribute{
		engine.String("zfile", engine.Inlined("zrecords")),
		engine.String(engine.Key("body", "file"), engine.Path("body", "file"), engine.Inlined("body-lines")),
		engine.String("plain"),
	}, engine.Nested(engine.DefineGroup("part", engine.DefineIter([]*engine.Attribute{
		engine.String("afile", engine.Inlined("alines")),
	}))))
	assert.DeepEqual(t, inlinedIn(iter), []inlineMark{
		{attribute: "afile", into: "alines"},
		{attribute: "body", into: "body-lines"},
		{attribute: "zfile", into: "zrecords"},
	}, cmp.AllowUnexported(inlineMark{}))
}

func TestReadLinesOfNothing(t *testing.T) {
	fs := afero.NewMemMapFs()
	_, err := readLines(fs, "/absent")
	assert.Assert(t, err != nil)

	assert.NilError(t, afero.WriteFile(fs, "/empty", nil, 0o644))
	lines, err := readLines(fs, "/empty")
	assert.NilError(t, err)
	assert.Equal(t, len(lines), 0, "an empty file is no lines, not one empty line")
}

func TestInlineReadsAFileForAResourceInsideAScope(t *testing.T) {
	flat, _, err := compileFiles(t, map[string]string{
		"/project.yaml":           "id: root\n",
		"/space/shop/space.yaml":  "id: QmShop\n",
		"/space/shop/site/w.yaml": "id: QmW\nfile: zones/w.zone\n",
		"/zones/w.zone":           "a\nb\n",
	})
	assert.NilError(t, err)
	w := flat["space"].(map[string]any)["QmShop"].(map[string]any)["site"].(map[string]any)["QmW"].(map[string]any)
	assert.DeepEqual(t, w["records"], []any{"a", "b"})
}
