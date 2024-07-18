package source

import (
	"bytes"
	"context"
	"testing"

	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	fixtures "github.com/taubyte/tau/pkg/vm/fixtures/wasm"
	"github.com/taubyte/tau/pkg/vm/test_utils"
	"gotest.tools/v3/assert"
)

func TestSource(t *testing.T) {
	test_utils.ResetVars()

	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	_, loader, _, _, _, err := test_utils.Loader(ctx, bytes.NewReader(fixtures.Recursive))
	assert.NilError(t, err)

	source := New(loader)

	tctx, err := test_utils.Context()
	assert.NilError(t, err)

	sourceModule, err := source.Module(tctx, functionSpec.ModuleName(test_utils.TestFunc.Name))
	assert.NilError(t, err)

	sourceData := []byte(sourceModule)
	assert.DeepEqual(t, fixtures.NonCompressRecursive, sourceData)

	// Test Failures

	// Load Failure: invalid module name does not follow convention <type>/<name>
	if _, err = source.Module(tctx, test_utils.TestFunc.Name); err == nil {
		t.Error("expected error")
	}
}
