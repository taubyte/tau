package loginI18n_test

import (
	"errors"
	"testing"

	loginI18n "github.com/taubyte/tau/tools/tau/i18n/login"
	"gotest.tools/v3/assert"
)

func TestErrorNoEmailsFound(t *testing.T) {
	assert.Assert(t, loginI18n.ErrorNoEmailsFound != nil)
	assert.ErrorContains(t, loginI18n.ErrorNoEmailsFound, "no emails")
}

func TestCreateFailed(t *testing.T) {
	err := loginI18n.CreateFailed("github", errors.New("err"))
	assert.ErrorContains(t, err, "github")
	assert.ErrorContains(t, err, "err")
}

func TestSelectFailed(t *testing.T) {
	err := loginI18n.SelectFailed("login1", errors.New("err"))
	assert.ErrorContains(t, err, "login1")
}

func TestGetProfilesFailed(t *testing.T) {
	err := loginI18n.GetProfilesFailed(errors.New("err"))
	assert.ErrorContains(t, err, "profiles")
}

func TestDoesNotExistIn(t *testing.T) {
	err := loginI18n.DoesNotExistIn("x", []string{"a", "b"})
	assert.ErrorContains(t, err, "x")
}

func TestSettingDefaultFailed(t *testing.T) {
	err := loginI18n.SettingDefaultFailed(errors.New("err"))
	assert.ErrorContains(t, err, "default")
}
