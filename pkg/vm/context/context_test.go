package context

import (
	"context"
	"errors"
	"testing"

	spec "github.com/taubyte/tau/pkg/specs/common"
	"gotest.tools/v3/assert"
)

var (
	projectId     = "projectId"
	applicationId = "appId"
	resourceId    = "resourceId"
	branch        = "main"
	commit        = "commit"

	errorFoo = errors.New("forced failure")
)

func errOption() Option {
	return func(vc *vmContext) error {
		return errorFoo
	}
}

func TestContext(t *testing.T) {
	baseContext := context.Background()
	ctx, err := New(baseContext, Project(projectId), Application(applicationId), Resource(resourceId), Commit(commit))
	assert.NilError(t, err)

	assert.Equal(t, ctx.Application(), applicationId)
	assert.DeepEqual(t, ctx.Branches(), spec.DefaultBranches)
	assert.Equal(t, ctx.Commit(), commit)
	assert.Equal(t, ctx.Project(), projectId)
	assert.Equal(t, ctx.Resource(), resourceId)

	ctx, err = New(baseContext, Branch(branch))
	assert.NilError(t, err)

	assert.DeepEqual(t, ctx.Branches(), []string{branch})

	// Options error: errOption always returns error, when applying options New will fail
	_, err = New(baseContext, errOption())
	assert.Error(t, err, errorFoo.Error())
}
