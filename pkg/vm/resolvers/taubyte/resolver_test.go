package resolver_test

import (
	"fmt"
	"testing"

	gocontext "context"

	"github.com/taubyte/tau/core/vm"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	"github.com/taubyte/tau/pkg/specs/methods"
	"github.com/taubyte/tau/services/tns/mocks"

	"github.com/taubyte/tau/pkg/vm/context"
	"github.com/taubyte/tau/pkg/vm/test_utils"

	resolv "github.com/taubyte/tau/pkg/vm/resolvers/taubyte"
	"gotest.tools/v3/assert"
)

func basicLookUp(t *testing.T, global bool, mAddr, expectedUri string) (mocks.MockedTns, vm.Resolver, vm.Context) {
	tns, resolver, err := test_utils.Resolver(global)
	assert.NilError(t, err)

	ctx, err := test_utils.Context()
	assert.NilError(t, err)

	if len(mAddr) > 0 {
		uri, err := resolver.Lookup(ctx, mAddr)
		assert.NilError(t, err)

		if len(expectedUri) > 0 {
			assert.Equal(t, uri.String(), expectedUri)
		}
	}

	return tns, resolver, ctx
}

func TestResolverHTTP(t *testing.T) {
	test_utils.ResetVars()
	mAddr := fmt.Sprintf("/dns4/%s/https/%s/%s", test_utils.TestHost, resolv.PATH_PROTOCOL_NAME, test_utils.TestPath)
	basicLookUp(t, false, mAddr, mAddr)
}

func TestResolverFS(t *testing.T) {
	test_utils.ResetVars()
	basicLookUp(t, false, "/file/"+test_utils.Wd, "/file/"+test_utils.Wd)
}

func TestResolverProjectDFS(t *testing.T) {
	test_utils.ResetVars()
	basicLookUp(t, false, functionSpec.ModuleName(test_utils.TestFunc.Name), "/dfs/"+test_utils.MockConfig.Cid)
}

func TestResolverDFS(t *testing.T) {
	test_utils.ResetVars()
	basicLookUp(t, false, "/dfs/"+test_utils.MockConfig.Cid, "/dfs/"+test_utils.MockConfig.Cid)
}

func TestResolverDFSGlobal(t *testing.T) {
	moduleName := functionSpec.ModuleName(test_utils.TestFunc.Name)

	tns, resolver, ctx := basicLookUp(t, true, moduleName, "/dfs/"+test_utils.MockConfig.Cid)

	// Test Failures
	wasmPath, err := functionSpec.Tns().WasmModulePath(ctx.Project(), "", test_utils.TestFunc.Name)
	assert.NilError(t, err)

	// replace the wasm current path with nil, rather than a string array
	err = tns.Push(wasmPath.Slice(), nil)
	assert.NilError(t, err)

	// Current call failure: object retrieved by Current call is expected to be a []string, in this case its nil, thus failing
	if _, err = resolver.Lookup(ctx, moduleName); err == nil {
		t.Error("expected error")
		return
	}

	tns.Delete(wasmPath)
	// TNS Fetch wasm module path Failure: the tns store has been deleted, resulting in failure to fetch.
	if _, err = resolver.Lookup(ctx, moduleName); err == nil {
		t.Error("expected error")
		return
	}
}

func TestResolverDFSFailures(t *testing.T) {
	test_utils.ResetVars()

	tns, resolver, ctx := basicLookUp(t, false, "", "")

	assetHash, err := methods.GetTNSAssetPath(ctx.Project(), ctx.Resource(), ctx.Branches()[0])
	assert.NilError(t, err)

	// Replace asset index with nil value
	err = tns.Push(assetHash.Slice(), nil)
	assert.NilError(t, err)

	moduleName := functionSpec.ModuleName(test_utils.TestFunc.Name)

	// Typecast error: Expected asset cid to be string, but nil value is retrieved
	if _, err = resolver.Lookup(ctx, moduleName); err == nil {
		t.Error("expected error")
		return
	}

	// Delete the assetHash index
	tns.Delete(assetHash)

	// Fetch Error: assetHash index does not exist, thus Fetch fails
	if _, err = resolver.Lookup(ctx, moduleName); err == nil {
		t.Error("expected error")
		return
	}

	wasmPath, err := functionSpec.Tns().WasmModulePath(ctx.Project(), ctx.Application(), test_utils.TestFunc.Name)
	assert.NilError(t, err)

	// Push empty `current`` path to the `current` list
	tns.Push(wasmPath.Slice(), []string{""})

	// Parser Error: `current` path is parsed using regex, an empty string value results in failure of the parser
	if _, err = resolver.Lookup(ctx, moduleName); err == nil {
		t.Error("expected error")
		return
	}

	// Push invalid `current` path, to be used to create asset hash.
	tns.Push(wasmPath.Slice(), []string{"current"})

	// AssetHash Error: the asset hash helper method requires a project Id, resource Id, and branch
	// All are empty thus resulting in failure
	if _, err = resolver.Lookup(ctx, moduleName); err == nil {
		t.Error("expected error")
		return
	}

	// Push multiple `current` values
	tns.Push(wasmPath.Slice(), []string{"current", "current"})

	// Current Path Length Error: There may not be more than one `current` path, thus failure
	if _, err = resolver.Lookup(ctx, moduleName); err == nil {
		t.Error("expected error")
		return
	}

	// Fetch Error: no function "hello_world" has been registered to TNS
	if _, err = resolver.Lookup(ctx, functionSpec.ModuleName("hello_world")); err == nil {
		t.Error("expected error")
		return
	}

	// Create context with no Project,resource, application, branch, or commit
	ctx, err = context.New(gocontext.Background())
	assert.NilError(t, err)

	// WasmModulePathFromModule Error: WasmModulePathFromModule requires a project, and application
	if _, err = resolver.Lookup(ctx, moduleName); err == nil {
		t.Error("expected error")
		return
	}
}

func TestResolverLookupFailures(t *testing.T) {
	test_utils.ResetVars()

	_, resolver, err := test_utils.Resolver(false)
	assert.NilError(t, err)

	ctx, err := test_utils.Context()
	assert.NilError(t, err)

	// Module name should be in convention <type>/<name>
	if _, err = resolver.Lookup(ctx, test_utils.TestFunc.Name); err == nil {
		t.Error("expected error")
		return
	}

	// Module type `funcs` is not recognized, for functions module type is `functions`
	if _, err = resolver.Lookup(ctx, "funcs/"+test_utils.TestFunc.Name); err == nil {
		t.Error("expected error")
		return
	}
}
