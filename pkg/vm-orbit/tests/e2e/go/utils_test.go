package tests

import (
	"context"
	"fmt"
	"os"
	"path"
	"testing"

	"github.com/otiai10/copy"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-orbit/tests/suite"
	builder "github.com/taubyte/tau/pkg/vm-orbit/tests/suite/builders/go"
	"gotest.tools/v3/assert"
)

var (
	wd           string
	fixtureDir   string
	basicWasm    string
	pluginBinary string

	wasmFixtures = []string{"data_helpers", "size_helpers", "basic"}
	pluginName   = "testPlugin"
)

func initializeAssetPaths() (err error) {
	if wd, err = os.Getwd(); err != nil {
		return
	}

	fixtureDir = path.Join(wd, "fixtures")
	basicWasm = path.Join(fixtureDir, "basic.wasm")
	pluginBinary = path.Join(fixtureDir, pluginName)

	return
}

func initializePlugin(extraArgs ...string) (err error) {
	goBuilder := builder.New()
	pluginFile, err := goBuilder.Plugin(path.Join(fixtureDir, "plugin"), pluginName, extraArgs...)
	if err != nil {
		return fmt.Errorf("generating plugin failed with: %w", err)
	}

	if err = copy.Copy(pluginFile, path.Join(fixtureDir, pluginName)); err != nil {
		return fmt.Errorf("copying plugin failed with: %w", err)
	}

	return nil
}

func initializeWasm(name string) (err error) {
	goBuilder := builder.New()
	wasmFile, err := goBuilder.Wasm(context.TODO(), path.Join(fixtureDir, "_code", name+".go"))
	if err != nil {
		return fmt.Errorf("generating %s.wasm failed with: %w", name, err)
	}

	if err = copy.Copy(wasmFile, path.Join(fixtureDir, name+".wasm")); err != nil {
		return fmt.Errorf("copying %s.wasm failed with: %w", name, err)
	}

	return nil
}

func basicCall(t *testing.T, plugin vm.Plugin, wasmModule string, args ...interface{}) vm.Return {
	testingSuite, err := suite.New(context.Background())
	assert.NilError(t, err)
	defer testingSuite.Close()

	err = testingSuite.AttachPlugin(plugin)
	assert.NilError(t, err)

	module, err := testingSuite.WasmModule(wasmModule)
	assert.NilError(t, err)

	ret, err := module.Call(context.TODO(), "ping", args...)
	assert.NilError(t, err)

	return ret
}

func testReturn(t *testing.T, ret vm.Return, expected uint32) {
	var retVal uint32
	err := ret.Reflect(&retVal)
	assert.NilError(t, err)

	assert.Equal(t, retVal, expected)
}
