package starlark

import (
	"fmt"
	"io/fs"
	"testing"

	"go.starlark.net/starlark"
	"gotest.tools/v3/assert"
)

type testModule struct{}

func (tm *testModule) Name() string {
	return "test"
}

func (tm *testModule) E_add(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if args.Len() != 2 {
		return nil, fmt.Errorf("expected exactly two arguments")
	}
	x, err := starlark.AsInt32(args.Index(0))
	if err != nil {
		return nil, fmt.Errorf("first argument must be an integer")
	}
	y, err := starlark.AsInt32(args.Index(1))
	if err != nil {
		return nil, fmt.Errorf("second argument must be an integer")
	}
	return starlark.MakeInt(x + y), nil
}

func (tm *testModule) E_Add2(x int, y int) int {
	return x + y
}

func (tm *testModule) E_Div(x int, y int) (int, error) {
	if y == 0 {
		return 0, fmt.Errorf("second argument cannot be zero")
	}
	return x / y, nil
}

func (tm *testModule) E_hello(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return starlark.String("Hello, Starlark!"), nil
}

func (tm *testModule) E_Concatenate(a, b string) string {
	return a + b
}

func (tm *testModule) E_SumFloat(a, b float64) float64 {
	return a + b
}

func (tm *testModule) E_BoolAnd(a, b bool) bool {
	return a && b
}

func (tm *testModule) E_ListLength(list []interface{}) int {
	return len(list)
}

func (tm *testModule) E_DictSize(dict map[interface{}]interface{}) int {
	return len(dict)
}

func (tm *testModule) E_Nothing() interface{} {
	return nil
}

type testModuleWithUnsupportedType struct{ testModule }

func (tm *testModuleWithUnsupportedType) E_UnsupportedType(x complex128) complex128 {
	return x
}

type printer struct{}

func (c *printer) Name() string {
	return "printer"
}

func (c *printer) E_echo(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	sargs := make([]any, 0, len(args))
	for _, arg := range args {
		sargs = append(sargs, arg.String())
	}

	fmt.Println(sargs...)

	return starlark.String(fmt.Sprint(sargs...)), nil
}

func TestBuiltInFunction(t *testing.T) {
	// Create a VM with the embedded file system.
	vm, err := New(fs.FS(testFiles))
	assert.NilError(t, err, "Failed to create VM")

	// Register the testModule with its Add function as a built-in.
	assert.NilError(t, vm.Module(new(testModule)))

	// Load and execute the script that uses the Add function.
	ctx, err := vm.File("testdata/add.star")
	assert.NilError(t, err, "Failed to load add file")

	// Call the function in the script context.
	result, err := ctx.Call("add")
	assert.NilError(t, err, "Failed to call add function")

	// Check the result.
	if intResult, ok := result.(starlark.Int); ok {
		intValue, _ := intResult.Int64()
		assert.Equal(t, intValue, int64(8), "Expected 8 as the result, got %d", intValue)
	} else {
		t.Errorf("Expected result to be a starlark.Int, got %T", result)
	}
}

func TestMulBuiltInFunction(t *testing.T) {
	// Create a VM with the embedded file system.
	vm, err := New(fs.FS(testFiles))
	assert.NilError(t, err, "Failed to create VM")

	assert.NilError(t, vm.Modules(new(testModule), new(printer)))

	// Load and execute the script that uses the Add function.
	ctx, err := vm.File("testdata/add_echo.star")
	assert.NilError(t, err, "Failed to load add_echo file")

	// Call the function in the script context.
	result, err := ctx.Call("echo")
	assert.NilError(t, err, "Failed to call echo function")

	// Check the result.
	if strResult, ok := result.(starlark.String); ok {
		strValue := strResult.GoString()
		assert.Equal(t, strValue, "8", "Expected 8 as the result, got %d", strValue)
	} else {
		t.Errorf("Expected result to be a starlark.Int, got %T", result)
	}
}

func TestAddNativeGoFunction(t *testing.T) {
	// Create a VM with the embedded file system.
	vm, err := New(testFiles)
	assert.NilError(t, err, "Failed to create VM")

	// Register the testModule with its native Go Add function as a built-in.
	assert.NilError(t, vm.Module(new(testModule)))

	// Load the context to call functions.
	ctx, err := vm.File("testdata/go.star")
	assert.NilError(t, err, "Failed to load go.star")

	// Call the native Go Add function directly.
	result, err := ctx.Call("Add2", starlark.MakeInt(5), starlark.MakeInt(3))
	assert.NilError(t, err, "Failed to call Add2 function")

	// Check the result.
	if intResult, ok := result.(starlark.Int); ok {
		intValue, _ := intResult.Int64()
		assert.Equal(t, intValue, int64(8), "Expected 8 as the result, got %d", intValue)
	} else {
		t.Errorf("Expected result to be a starlark.Int, got %T", result)
	}
}

func TestDivNativeGoWithErrorFunction(t *testing.T) {
	// Create a VM with the embedded file system.
	vm, err := New(testFiles)
	assert.NilError(t, err, "Failed to create VM")

	// Register the testModule with its AddWithError function as a built-in.
	assert.NilError(t, vm.Module(new(testModule)))

	// Load the context to call functions.
	ctx, err := vm.File("testdata/go.star")
	assert.NilError(t, err, "Failed to load go.star")

	// Call the AddWithError function with valid arguments.
	_, err = ctx.CallWithNative("Div", 15, 0)
	assert.Error(t, err, "failed to call function Div: second argument cannot be zero")

	// Call the AddWithError function with valid arguments.
	result, err := ctx.CallWithNative("Div", 15, 3)
	assert.NilError(t, err, "Failed to call Div function with valid arguments")

	// Check the result.
	if intValue, ok := result.(int64); ok {
		assert.Equal(t, intValue, int64(5), "Expected 5 as the result, got %d", intValue)
	} else {
		t.Errorf("Expected result to be an int, got %T", result)
	}
}

func TestAddNativeGoFunctionWithNative(t *testing.T) {
	// Create a VM with the embedded file system.
	vm, err := New(testFiles)
	assert.NilError(t, err, "Failed to create VM")

	// Register the testModule with its native Go Add function as a built-in.
	assert.NilError(t, vm.Module(new(testModule)))

	// Load the context to call functions.
	ctx, err := vm.File("testdata/go.star")
	assert.NilError(t, err, "Failed to load go.star")

	// Call the native Go Add function directly.
	result, err := ctx.CallWithNative("Add2", 5, 3)
	assert.NilError(t, err, "Failed to call Add2 function")

	// Check the result.
	if intValue, ok := result.(int64); ok {
		assert.Equal(t, intValue, int64(8), "Expected 8 as the result, got %d", intValue)
	} else {
		t.Errorf("Expected result to be an int, got %T", result)
	}
}

func TestHelloFunctionWithNative(t *testing.T) {
	// Create a VM with the embedded file system.
	vm, err := New(testFiles)
	assert.NilError(t, err, "Failed to create VM")

	// Register the testModule with its Hello function as a built-in.
	assert.NilError(t, vm.Module(new(testModule)))

	// Load the context to call functions.
	ctx, err := vm.File("testdata/go.star")
	assert.NilError(t, err, "Failed to load go.star")

	// Call the hello function directly.
	result, err := ctx.CallWithNative("Hello")
	assert.NilError(t, err, "Failed to call Hello function")

	// Check the result.
	if strResult, ok := result.(string); ok {
		assert.Equal(t, strResult, "Hello, Starlark!", "Expected 'Hello, Starlark!', got %s", strResult)
	} else {
		t.Errorf("Expected result to be a starlark.String, got %T", result)
	}
}

func TestHelloFunction(t *testing.T) {
	// Create a VM with the embedded file system.
	vm, err := New(testFiles)
	assert.NilError(t, err, "Failed to create VM")

	// Register the testModule with its Hello function as a built-in.
	assert.NilError(t, vm.Module(new(testModule)))

	// Load the context to call functions.
	ctx, err := vm.File("testdata/go.star")
	assert.NilError(t, err, "Failed to load go.star")

	// Call the hello function directly.
	result, err := ctx.Call("Hello")
	assert.NilError(t, err, "Failed to call Hello function")

	// Check the result.
	if strResult, ok := result.(starlark.String); ok {
		assert.Equal(t, string(strResult), "Hello, Starlark!", "Expected 'Hello, Starlark!', got %s", strResult)
	} else {
		t.Errorf("Expected result to be a starlark.String, got %T", result)
	}
}

func TestConcatenateFunction(t *testing.T) {
	// Create a VM with the embedded file system.
	vm, err := New(testFiles)
	assert.NilError(t, err, "Failed to create VM")

	// Register the testModule with its functions as a built-in.
	assert.NilError(t, vm.Module(new(testModule)))

	// Load the context to call functions.
	ctx, err := vm.File("testdata/go.star")
	assert.NilError(t, err, "Failed to load go.star")

	// Call the Concatenate function directly.
	result, err := ctx.Call("Concatenate", starlark.String("Hello, "), starlark.String("World!"))
	assert.NilError(t, err, "Failed to call Concatenate function")

	// Check the result.
	if strResult, ok := result.(starlark.String); ok {
		assert.Equal(t, string(strResult), "Hello, World!", "Expected 'Hello, World!', got %s", strResult)
	} else {
		t.Errorf("Expected result to be a starlark.String, got %T", result)
	}
}

func TestSumFloatFunction(t *testing.T) {
	// Create a VM with the embedded file system.
	vm, err := New(testFiles)
	assert.NilError(t, err, "Failed to create VM")

	// Register the testModule with its functions as a built-in.
	assert.NilError(t, vm.Module(new(testModule)))

	// Load the context to call functions.
	ctx, err := vm.File("testdata/go.star")
	assert.NilError(t, err, "Failed to load go.star")

	// Call the SumFloat function directly.
	result, err := ctx.Call("SumFloat", starlark.Float(5.5), starlark.Float(3.5))
	assert.NilError(t, err, "Failed to call SumFloat function")

	// Check the result.
	if floatResult, ok := result.(starlark.Float); ok {
		assert.Equal(t, float64(floatResult), 9.0, "Expected 9.0 as the result, got %f", floatResult)
	} else {
		t.Errorf("Expected result to be a starlark.Float, got %T", result)
	}
}

func TestAndFunction(t *testing.T) {
	// Create a VM with the embedded file system.
	vm, err := New(testFiles)
	assert.NilError(t, err, "Failed to create VM")

	// Register the testModule with its functions as a built-in.
	assert.NilError(t, vm.Module(new(testModule)))

	// Load the context to call functions.
	ctx, err := vm.File("testdata/go.star")
	assert.NilError(t, err, "Failed to load go.star")

	// Call the And function directly.
	result, err := ctx.Call("And", starlark.Bool(true), starlark.Bool(false))
	assert.NilError(t, err, "Failed to call And function")

	// Check the result.
	if boolResult, ok := result.(starlark.Bool); ok {
		assert.Equal(t, bool(boolResult), false, "Expected false as the result, got %v", boolResult)
	} else {
		t.Errorf("Expected result to be a starlark.Bool, got %T", result)
	}
}

func TestListLengthFunction(t *testing.T) {
	// Create a VM with the embedded file system.
	vm, err := New(testFiles)
	assert.NilError(t, err, "Failed to create VM")

	// Register the testModule with its functions as a built-in.
	assert.NilError(t, vm.Module(new(testModule)))

	// Load the context to call functions.
	ctx, err := vm.File("testdata/go.star")
	assert.NilError(t, err, "Failed to load go.star")

	// Call the ListLength function directly.
	list := &starlark.List{}
	list.Append(starlark.MakeInt(1))
	list.Append(starlark.MakeInt(2))
	list.Append(starlark.MakeInt(3))

	result, err := ctx.Call("ListLength", list)
	assert.NilError(t, err, "Failed to call ListLength function")

	// Check the result.
	if intResult, ok := result.(starlark.Int); ok {
		intValue, _ := intResult.Int64()
		assert.Equal(t, intValue, int64(3), "Expected 3 as the result, got %d", intValue)
	} else {
		t.Errorf("Expected result to be a starlark.Int, got %T", result)
	}
}

func TestDictSizeFunction(t *testing.T) {
	// Create a VM with the embedded file system.
	vm, err := New(testFiles)
	assert.NilError(t, err, "Failed to create VM")

	// Register the testModule with its functions as a built-in.
	assert.NilError(t, vm.Module(new(testModule)))

	// Load the context to call functions.
	ctx, err := vm.File("testdata/go.star")
	assert.NilError(t, err, "Failed to load go.star")

	// Call the DictSize function directly.
	dict := &starlark.Dict{}
	dict.SetKey(starlark.String("a"), starlark.MakeInt(1))
	dict.SetKey(starlark.String("b"), starlark.MakeInt(2))

	result, err := ctx.Call("DictSize", dict)
	assert.NilError(t, err, "Failed to call DictSize function")

	// Check the result.
	if intResult, ok := result.(starlark.Int); ok {
		intValue, _ := intResult.Int64()
		assert.Equal(t, intValue, int64(2), "Expected 2 as the result, got %d", intValue)
	} else {
		t.Errorf("Expected result to be a starlark.Int, got %T", result)
	}
}

func TestNothingFunction(t *testing.T) {
	// Create a VM with the embedded file system.
	vm, err := New(testFiles)
	assert.NilError(t, err, "Failed to create VM")

	// Register the testModule with its functions as a built-in.
	assert.NilError(t, vm.Module(new(testModule)))

	// Load the context to call functions.
	ctx, err := vm.File("testdata/go.star")
	assert.NilError(t, err, "Failed to load go.star")

	// Call the Nothing function directly.
	result, err := ctx.Call("Nothing")
	assert.NilError(t, err, "Failed to call Nothing function")

	// Check the result.
	assert.Equal(t, result, starlark.None, "Expected None as the result, got %v", result)
}

func TestUnsupportedTypeFunction(t *testing.T) {
	// Create a VM with the embedded file system.
	vm, err := New(testFiles)
	assert.NilError(t, err, "Failed to create VM")

	// Register the testModule with its functions as a built-in.
	assert.Error(t, vm.Module(new(testModuleWithUnsupportedType)), "failed to add module `test` with unsupported argument type: complex128")
}

func TestModulesSkipsExistingModules(t *testing.T) {
	vmInstance, err := New(testFiles)
	assert.NilError(t, err)

	assert.NilError(t, vmInstance.Modules(new(testModule)))

	internal := vmInstance.(*vm)
	assert.Equal(t, len(internal.builtins), 1)

	assert.NilError(t, vmInstance.Modules(new(testModule), new(printer)))
	assert.Equal(t, len(internal.builtins), 2)
}

func TestModulesReturnsError(t *testing.T) {
	vmInstance, err := New(testFiles)
	assert.NilError(t, err)

	err = vmInstance.Modules(new(testModuleWithUnsupportedType))
	assert.Error(t, err, "adding modules failed on module `test` with unsupported argument type: complex128")
}

func TestMakeGoFuncArgumentCountMismatch(t *testing.T) {
	methods, err := registerMethods(new(testModule))
	assert.NilError(t, err)

	callable, ok := methods["add2"].(starlark.Callable)
	assert.Assert(t, ok)

	_, err = starlark.Call(&starlark.Thread{Name: "test"}, callable, nil, nil)
	assert.Error(t, err, "expected 2 arguments, got 0")
}

func TestMakeGoFuncArgumentConversionError(t *testing.T) {
	methods, err := registerMethods(new(testModule))
	assert.NilError(t, err)

	callable, ok := methods["add2"].(starlark.Callable)
	assert.Assert(t, ok)

	args := starlark.Tuple{starlark.String("not-an-int"), starlark.String("still-not-an-int")}
	_, err = starlark.Call(&starlark.Thread{Name: "test"}, callable, args, nil)
	assert.ErrorContains(t, err, "got string, want int")
}

type multiReturnModule struct{}

func (m *multiReturnModule) Name() string {
	return "multi"
}

func (m *multiReturnModule) E_MultiValues() (int, string) {
	return 1, "one"
}

func TestMakeGoFuncMultipleReturnValues(t *testing.T) {
	methods, err := registerMethods(new(multiReturnModule))
	assert.NilError(t, err)

	callable, ok := methods["multiValues"].(starlark.Callable)
	assert.Assert(t, ok)

	result, err := starlark.Call(&starlark.Thread{Name: "test"}, callable, nil, nil)
	assert.NilError(t, err)

	tuple, ok := result.(starlark.Tuple)
	assert.Assert(t, ok)
	assert.Equal(t, len(tuple), 2)
}

type unsupportedReturnModule struct{}

func (m *unsupportedReturnModule) Name() string {
	return "badreturn"
}

func (m *unsupportedReturnModule) E_BadReturn() map[string]int {
	return map[string]int{"a": 1}
}

func TestMakeGoFuncUnsupportedReturnType(t *testing.T) {
	methods, err := registerMethods(new(unsupportedReturnModule))
	assert.NilError(t, err)

	callable, ok := methods["badReturn"].(starlark.Callable)
	assert.Assert(t, ok)

	_, err = starlark.Call(&starlark.Thread{Name: "test"}, callable, nil, nil)
	assert.ErrorContains(t, err, "unsupported type map[string]int")
}

func newTestCtx(t *testing.T, source string) *ctx {
	t.Helper()

	thread := &starlark.Thread{Name: "test"}
	globals, err := starlark.ExecFile(thread, "test.star", source, nil)
	assert.NilError(t, err)

	return &ctx{
		thread:  thread,
		globals: globals,
	}
}

func TestCtxCallFunctionNotFound(t *testing.T) {
	c := &ctx{
		thread:  &starlark.Thread{Name: "test"},
		globals: starlark.StringDict{},
	}

	_, err := c.Call("missing")
	assert.Error(t, err, "function missing not found")
}

func TestCtxCallPropagatesErrors(t *testing.T) {
	c := newTestCtx(t, `
def takes_one(x):
    return x
`)

	_, err := c.Call("takes_one", starlark.MakeInt(1), starlark.MakeInt(2))
	assert.ErrorContains(t, err, "failed to call function takes_one")
}

func TestCtxCallWithNativeFunctionNotFound(t *testing.T) {
	c := &ctx{
		thread:  &starlark.Thread{Name: "test"},
		globals: starlark.StringDict{},
	}

	_, err := c.CallWithNative("missing")
	assert.Error(t, err, "function missing not found")
}

func TestCtxCallWithNativeArgumentConversionError(t *testing.T) {
	c := newTestCtx(t, `
def identity(x):
    return x
`)

	_, err := c.CallWithNative("identity", struct{}{})
	assert.ErrorContains(t, err, "unsupported type struct {}")
}
