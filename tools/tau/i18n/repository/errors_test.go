package repositoryI18n_test

import (
	"errors"
	"testing"

	repositoryI18n "github.com/taubyte/tau/tools/tau/i18n/repository"
	"gotest.tools/v3/assert"
)

func TestRegisteringRepositoryFailed(t *testing.T) {
	err := repositoryI18n.RegisteringRepositoryFailed("repo1", errors.New("err"))
	assert.ErrorContains(t, err, "repo1")
	assert.ErrorContains(t, err, "err")
}

func TestErrorUnregisterRepositories(t *testing.T) {
	err := repositoryI18n.ErrorUnregisterRepositories(errors.New("unreg"))
	assert.ErrorContains(t, err, "un-registering")
}

func TestErrorListRepositories(t *testing.T) {
	err := repositoryI18n.ErrorListRepositories("user", errors.New("list err"))
	assert.ErrorContains(t, err, "user")
	assert.ErrorContains(t, err, "list err")
}

func TestUnknownTemplate(t *testing.T) {
	err := repositoryI18n.UnknownTemplate("tpl", []string{"a", "b"})
	assert.ErrorContains(t, err, "tpl")
	assert.ErrorContains(t, err, "unknown template")
}

func TestErrorDeleteRepository(t *testing.T) {
	err := repositoryI18n.ErrorDeleteRepository("repo", errors.New("del"))
	assert.ErrorContains(t, err, "repo")
	assert.ErrorContains(t, err, "del")
}

func TestErrorAdminRights(t *testing.T) {
	assert.Assert(t, repositoryI18n.ErrorAdminRights != nil)
	assert.ErrorContains(t, repositoryI18n.ErrorAdminRights, "admin")
}
