package schema_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

func TestABuildIsReadForAPackageInANamespace(t *testing.T) {
	fs := mountedProject(t, "")
	assert.NilError(t, fs.Remove("/package/docs.yaml"))
	assert.NilError(t, afero.WriteFile(fs, "/namespace/local/meta.yaml",
		[]byte("id: QmQt1CwQTMMBWhPDHmNUzd81oCd21PuxkrxJzSh2SnoUQM\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/namespace/local/package/docs.yaml",
		[]byte("id: QmRgVLmWTBpgTbCPnHRUvKGnHqHf7Yd7t8ZffKeRXPkRLL\ntype: static\n"+delegating), 0o644))

	mounts := mountsOf(t, fs, "production")
	assert.Equal(t, len(mounts), 1)
	assert.Equal(t, mounts[0].Scope, "local")

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"),
		schema.WithEnvironments("production", "staging"), schema.WithEnvironment("production"),
		fulfil(t, mounts[0], "def456", map[string]string{"taubyte.yaml": "build:\n    steps:\n        - name: site\n          image: node:22\n          commands: [npm run build]\n"}))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	flat := obj.Flat()
	ns := only(t, resources(t, flat, "namespace"))
	pkg := only(t, ns["package"].(map[string]any))
	assert.Equal(t, nested(pkg, "build", "kind"), "steps")
}
