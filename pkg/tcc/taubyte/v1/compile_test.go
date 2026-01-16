package compiler

import (
	"context"
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/pkg/config-compiler/compile"
	projectLib "github.com/taubyte/tau/pkg/schema/project"
	"gotest.tools/v3/assert"
)

var fakeMeta = patrick.Meta{
	Repository: patrick.Repository{
		Provider: "github",
		Branch:   "master",
		ID:       12356,
	},
	HeadCommit: patrick.HeadCommit{
		ID: "345690",
	},
}

var generatedDomainRegExp = regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)

func TestCompile(t *testing.T) {
	project, err := projectLib.Open(projectLib.SystemFS("fixtures/config"))
	assert.NilError(t, err)

	rc, err := compile.CompilerConfig(project, fakeMeta, generatedDomainRegExp)
	assert.NilError(t, err)

	oldCompiler, err := compile.New(rc, compile.Dev())
	assert.NilError(t, err)

	err = oldCompiler.Build()
	assert.NilError(t, err)

	compiler, err := New(WithLocal("fixtures/config"), WithBranch("master"))
	assert.NilError(t, err)

	obj, err := compiler.Compile(context.Background())
	assert.NilError(t, err)

	newObj := obj.Flat()["object"].(map[string]interface{})
	oldObj := oldCompiler.Object()

	assert.Assert(t, cmp.Equal(newObj, oldObj), cmp.Diff(oldObj, newObj))

	indexes := obj.Flat()["indexes"].(map[string]interface{})

	// older compiler has a bug where it does not handle messaging inside an app
	// delete it to make the deep equal works
	delete(indexes, "p2p/pubsub/QmUgRE95oaisf5cK1DNaKizPQS7mqtd3zZ68wuUEKfoWoB")

	assert.Assert(t, cmp.Equal(indexes, oldCompiler.Indexes()), cmp.Diff(oldCompiler.Indexes(), indexes))

}
