package loader_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	fixtures "github.com/taubyte/tau/pkg/vm/fixtures/wasm"
	loaders "github.com/taubyte/tau/pkg/vm/loaders/wazero"
	test "github.com/taubyte/tau/pkg/vm/test_utils"
	"gotest.tools/v3/assert"
)

func TestLoader(t *testing.T) {
	test.ResetVars()

	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	cid, loader, resolver, _, simple, err := test.Loader(ctx, bytes.NewReader(fixtures.Recursive))
	assert.NilError(t, err)

	tctx, err := test.Context()
	assert.NilError(t, err)

	moduleName := functionSpec.ModuleName(test.TestFunc.Name)

	reader, err := loader.Load(tctx, moduleName)
	assert.NilError(t, err)

	source, err := io.ReadAll(reader)
	assert.NilError(t, err)

	assert.DeepEqual(t, fixtures.NonCompressRecursive, source)

	// Test Failures

	// Delete ipfs stored file
	err = simple.DeleteFile(cid)
	assert.NilError(t, err)

	// No Reader Error: All backends have been checked, but all returned nil readers.
	if _, err = loader.Load(tctx, moduleName); err == nil {
		t.Error("expected error")
	}

	// New Loader with no backends
	loader = loaders.New(resolver)
	// Backend Error: Creating a loader with no backends results in failure
	if _, err = loader.Load(tctx, moduleName); err == nil {
		t.Error("expected error")
	}

	// Lookup Error: Attempting to load module that does not follow convention of <type>/<name>
	if _, err = loader.Load(tctx, test.TestFunc.Name); err == nil {
		t.Error("expected error")
	}

}
