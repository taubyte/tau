package decompile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/object"
	compiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
	"gotest.tools/v3/assert"
)

func TestDecompileRoundTrip(t *testing.T) {
	// Compile from fixtures (for decompiling)
	c, err := compiler.New(compiler.WithLocal("../fixtures/config"), compiler.WithBranch("master"))
	assert.NilError(t, err)

	obj, validations, err := c.Compile(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, len(validations) > 0, "should have validations")

	// Decompile to in-memory filesystem
	memFs := afero.NewMemMapFs()
	d, err := New(WithVirtual(memFs, "/"))
	assert.NilError(t, err)

	err = d.Decompile(obj)
	assert.NilError(t, err)

	// Recompile from decompiled YAML
	c2, err := compiler.New(compiler.WithVirtual(memFs, "/"), compiler.WithBranch("master"))
	assert.NilError(t, err)

	obj2, validations2, err := c2.Compile(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, len(validations2) > 0, "should have validations")

	// Compile again from original fixtures (for comparison, since decompile modifies obj)
	c3, err := compiler.New(compiler.WithLocal("../fixtures/config"), compiler.WithBranch("master"))
	assert.NilError(t, err)

	obj3, _, err := c3.Compile(context.Background())
	assert.NilError(t, err)

	// Compare recompiled with fresh compile
	newObj := obj3.Flat()["object"].(map[string]interface{})
	newObj2 := obj2.Flat()["object"].(map[string]interface{})

	assert.Assert(t, cmp.Equal(newObj, newObj2), cmp.Diff(newObj2, newObj))
}

func TestDecompileBasic(t *testing.T) {
	c, err := compiler.New(compiler.WithLocal("../fixtures/config"), compiler.WithBranch("master"))
	assert.NilError(t, err)

	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	// Decompile to in-memory filesystem
	memFs := afero.NewMemMapFs()
	d, err := New(WithVirtual(memFs, "/"))
	assert.NilError(t, err)

	err = d.Decompile(obj)
	assert.NilError(t, err)

	// Verify that config.yaml exists
	exists, err := afero.Exists(memFs, "/config.yaml")
	assert.NilError(t, err)
	assert.Assert(t, exists, "config.yaml should exist")

	// Verify that at least one resource file exists (e.g., domains)
	// The exact files depend on the fixtures
	domainsExists, _ := afero.Exists(memFs, "/domains")
	if domainsExists {
		// If domains directory exists, verify it has files
		files, err := afero.ReadDir(memFs, "/domains")
		assert.NilError(t, err)
		assert.Assert(t, len(files) > 0, "domains directory should have files")
	}
}

func TestWithLocal(t *testing.T) {
	// Create a temporary directory for decompilation
	tempDir := t.TempDir()

	// Test WithLocal option with temp directory
	d, err := New(WithLocal(tempDir))
	assert.NilError(t, err)
	assert.Assert(t, d != nil)

	// Compile from fixtures
	c, err := compiler.New(compiler.WithLocal("../fixtures/config"), compiler.WithBranch("master"))
	assert.NilError(t, err)

	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	// Decompile using WithLocal to temp directory
	err = d.Decompile(obj)
	assert.NilError(t, err)

	// Verify that config.yaml was created in temp directory
	configPath := filepath.Join(tempDir, "config.yaml")
	_, err = os.Stat(configPath)
	assert.NilError(t, err)
}

func TestNew_NoOptions(t *testing.T) {
	// Test New with no options - should fail because seer needs filesystem
	d, err := New()
	assert.ErrorContains(t, err, "file system")
	assert.Assert(t, d == nil)
}

func TestNew_OptionError(t *testing.T) {
	// Test New with an option that returns an error
	errOption := func(d *Decompiler) error {
		return fmt.Errorf("test option error")
	}

	d, err := New(errOption)
	assert.ErrorContains(t, err, "test option error")
	assert.Assert(t, d == nil)
}

func TestDecompile_EmptyObject(t *testing.T) {
	memFs := afero.NewMemMapFs()
	d, err := New(WithVirtual(memFs, "/"))
	assert.NilError(t, err)

	// Create an object with required id field
	obj := object.New[object.Refrence]()
	// Use a valid CID format for id
	obj.Set("id", "QmYjtig7VJQ6XsnUjqqJvj7QaMcCAwtrgNdahSiFofrE7o")

	// This should work fine
	err = d.Decompile(obj)
	assert.NilError(t, err)
}

func TestDecompile_MinimalObject(t *testing.T) {
	memFs := afero.NewMemMapFs()
	d, err := New(WithVirtual(memFs, "/"))
	assert.NilError(t, err)

	// Create a minimal object with a valid CID id
	obj := object.New[object.Refrence]()
	obj.Set("id", "QmYjtig7VJQ6XsnUjqqJvj7QaMcCAwtrgNdahSiFofrE7o")

	// This should work
	err = d.Decompile(obj)
	assert.NilError(t, err)

	// Verify config.yaml was created
	exists, err := afero.Exists(memFs, "/config.yaml")
	assert.NilError(t, err)
	assert.Assert(t, exists, "config.yaml should exist")
}
