package interp

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

func promoteSchema(requireAll bool) (engine.Schema, *engine.Node) {
	root := engine.Root(
		[]*engine.Attribute{engine.String("id"), engine.String("name")},
		engine.DefineGroup("clouds", engine.DefineIter([]*engine.Attribute{
			engine.String("account"),
			engine.String("plan"),
		}, PromoteEnvKeyed("clouds", "cloud", []string{"account", "plan"}, requireAll))),
	).With(engine.Leaf("project"), engine.Identity("id", "name"))
	return engine.SchemaDefinition(root), root
}

func promoteCompile(t *testing.T, requireAll bool, project string, opts ...Option) (map[string]any, error) {
	t.Helper()
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml", []byte(project), 0o644))
	schema, root := promoteSchema(requireAll)
	c, err := New(schema, root, append([]Option{WithVirtual(fs, "/")}, opts...)...)
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	if err != nil {
		return nil, err
	}
	return obj.Flat(), nil
}

const cloudsProject = "id: root\nname: shop\nclouds:\n" +
	"    aws.example.com:\n        account: acme\n        plan: pro\n" +
	"    half:\n        account: solo\n" +
	"    blank: {}\n"

func TestPromoteHoistsTheSelectedEntryAndDropsTheMap(t *testing.T) {
	flat, err := promoteCompile(t, true, cloudsProject, WithEnv("cloud", "aws.example.com"))
	assert.NilError(t, err)
	assert.Equal(t, flat["account"], "acme")
	assert.Equal(t, flat["plan"], "pro")
	_, kept := flat["clouds"]
	assert.Assert(t, !kept, "the map is compile-only")
}

func TestPromoteWithNothingToPromote(t *testing.T) {
	for name, tc := range map[string]struct {
		project string
		env     []Option
	}{
		"no env key":      {cloudsProject, nil},
		"unknown entry":   {cloudsProject, []Option{WithEnv("cloud", "gcp")}},
		"entry with none": {cloudsProject, []Option{WithEnv("cloud", "blank")}},
		"no map at all":   {"id: root\nname: shop\n", []Option{WithEnv("cloud", "aws.example.com")}},
	} {
		t.Run(name, func(t *testing.T) {
			flat, err := promoteCompile(t, true, tc.project, tc.env...)
			assert.NilError(t, err)
			assert.DeepEqual(t, flat, map[string]any{"id": "root", "name": "shop"})
		})
	}
}

func TestPromoteAPartialEntry(t *testing.T) {
	_, err := promoteCompile(t, true, cloudsProject, WithEnv("cloud", "half"))
	assert.ErrorContains(t, err, `clouds["half"] is incomplete`)

	flat, err := promoteCompile(t, false, cloudsProject, WithEnv("cloud", "half"))
	assert.NilError(t, err)
	assert.Equal(t, flat["account"], "solo")
	_, plan := flat["plan"]
	assert.Assert(t, !plan, "an unset field is not promoted as empty")
	_, kept := flat["clouds"]
	assert.Assert(t, !kept)
}
