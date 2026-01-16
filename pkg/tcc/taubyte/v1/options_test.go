package compiler

import (
	"testing"

	"github.com/spf13/afero"
	"gotest.tools/v3/assert"
)

func TestWithVirtual(t *testing.T) {
	// Setup: Create virtual filesystem
	fs := afero.NewMemMapFs()
	path := "/test/path"

	// Execute
	option := WithVirtual(fs, path)

	compiler := &Compiler{}
	err := option(compiler)

	// Verify
	assert.NilError(t, err)
	assert.Equal(t, len(compiler.seerOptions), 1)
}

func TestWithLocal(t *testing.T) {
	// Setup
	path := "fixtures/config"

	// Execute
	option := WithLocal(path)

	compiler := &Compiler{}
	err := option(compiler)

	// Verify
	assert.NilError(t, err)
	assert.Equal(t, len(compiler.seerOptions), 1)
}

func TestWithBranch(t *testing.T) {
	// Setup
	branch := "test-branch"

	// Execute
	option := WithBranch(branch)

	compiler := &Compiler{}
	err := option(compiler)

	// Verify
	assert.NilError(t, err)
	assert.Equal(t, compiler.branch, branch)
}

func TestWithBranch_Default(t *testing.T) {
	// Setup: Create compiler using New() with filesystem but without branch option
	compiler, err := New(WithLocal("fixtures/config"))

	// Verify: Should have default branch
	assert.NilError(t, err)
	assert.Equal(t, compiler.branch, DefaultBranch)
}
