package projectI18n_test

import (
	"errors"
	"testing"

	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	"gotest.tools/v3/assert"
)

func TestBothFlagsCannotBeTrue(t *testing.T) {
	err := projectI18n.BothFlagsCannotBeTrue("flag1", "flag2")
	assert.ErrorContains(t, err, "both")
	assert.ErrorContains(t, err, "flag1")
	assert.ErrorContains(t, err, "flag2")
}

func TestErrorVars(t *testing.T) {
	assert.Assert(t, projectI18n.ErrorProjectLocationEmpty != nil)
	assert.Assert(t, projectI18n.ErrorConfigRepositoryNotFound != nil)
	assert.Assert(t, projectI18n.ErrorCodeRepositoryNotFound != nil)
	assert.Assert(t, projectI18n.ErrorNoProjectsFound != nil)
}

func TestProjectNotFound(t *testing.T) {
	err := projectI18n.ProjectNotFound("proj1")
	assert.ErrorContains(t, err, "proj1")
	assert.ErrorContains(t, err, "not found")
}

func TestGettingRepositoriesFailed(t *testing.T) {
	err := projectI18n.GettingRepositoriesFailed("proj", errors.New("err"))
	assert.ErrorContains(t, err, "proj")
	assert.ErrorContains(t, err, "err")
}

func TestProjectBranchesNotEqual(t *testing.T) {
	err := projectI18n.ProjectBranchesNotEqual("main", "dev")
	assert.ErrorContains(t, err, "main")
	assert.ErrorContains(t, err, "dev")
}

func TestErrorDeleteProject(t *testing.T) {
	err := projectI18n.ErrorDeleteProject("proj", errors.New("del err"))
	assert.ErrorContains(t, err, "proj")
	assert.ErrorContains(t, err, "del err")
}

func TestSelectingVisibilityFailed(t *testing.T) {
	err := projectI18n.SelectingVisibilityFailed(errors.New("vis"))
	assert.ErrorContains(t, err, "visibility")
}

func TestGettingProjectsFailed(t *testing.T) {
	err := projectI18n.GettingProjectsFailed(errors.New("auth"))
	assert.ErrorContains(t, err, "getting projects")
}

func TestConfigRepoCreateFailed(t *testing.T) {
	err := projectI18n.ConfigRepoCreateFailed(errors.New("create"))
	assert.ErrorContains(t, err, "config repository")
}

func TestCreatingProjectFailed(t *testing.T) {
	err := projectI18n.CreatingProjectFailed(errors.New("create"))
	assert.ErrorContains(t, err, "creating project failed")
}
