package projectLib_test

import (
	"testing"

	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"gotest.tools/v3/assert"
)

func TestBindingFlags_BothEmpty(t *testing.T) {
	a, p, err := projectLib.BindingFlags("", "")
	assert.NilError(t, err)
	assert.Equal(t, a, "")
	assert.Equal(t, p, "")
}

func TestBindingFlags_BothSet(t *testing.T) {
	a, p, err := projectLib.BindingFlags("acme", "prod")
	assert.NilError(t, err)
	assert.Equal(t, a, "acme")
	assert.Equal(t, p, "prod")
}

func TestBindingFlags_OnlyAccount(t *testing.T) {
	_, _, err := projectLib.BindingFlags("acme", "")
	assert.ErrorContains(t, err, "must be set together")
	assert.ErrorContains(t, err, "--account=\"acme\"")
	assert.ErrorContains(t, err, "--plan=\"\"")
}

func TestBindingFlags_OnlyPlan(t *testing.T) {
	_, _, err := projectLib.BindingFlags("", "prod")
	assert.ErrorContains(t, err, "must be set together")
	assert.ErrorContains(t, err, "--account=\"\"")
	assert.ErrorContains(t, err, "--plan=\"prod\"")
}
