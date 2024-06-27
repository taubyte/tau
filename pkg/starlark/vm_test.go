package starlark

import (
	"embed"
	"testing"

	"go.starlark.net/starlark"
	"gotest.tools/v3/assert"
)

// Embed the necessary Starlark scripts for testing.
//
//go:embed testdata/*
var testFiles embed.FS

//go:embed testdata/main.star
var mainFiles embed.FS

//go:embed testdata/utilities.star
var utilityFiles embed.FS

// TestFibonacciFunction tests the fibonacci function from a Starlark script.
func TestFibonacciFunction(t *testing.T) {
	vm, err := New(testFiles)
	assert.NilError(t, err, "Failed to create VM")

	ctx, err := vm.File("testdata/fibonacci.star")
	assert.NilError(t, err, "Failed to load file")

	testCases := []struct {
		input    int
		expected int
	}{
		{input: 0, expected: 0},
		{input: 1, expected: 1},
		{input: 2, expected: 1},
		{input: 3, expected: 2},
		{input: 4, expected: 3},
		{input: 5, expected: 5},
		{input: 10, expected: 55},
	}

	for _, tc := range testCases {
		result, err := ctx.Call("fibonacci", starlark.MakeInt(tc.input))
		assert.NilError(t, err, "Failed to call fibonacci with input %d", tc.input)
		if intResult, ok := result.(starlark.Int); ok {
			intValue, ok := intResult.Int64()
			assert.Assert(t, ok, "Failed to convert result to Int64")
			assert.Equal(t, intValue, int64(tc.expected), "Fibonacci(%d) = %v, want %d", tc.input, result, tc.expected)
		} else {
			t.Errorf("Expected result to be a starlark.Int, got %T", result)
		}
	}
}

// TestImportFunction tests importing a function from another module in the same filesystem.
func TestImportFunction(t *testing.T) {
	vm, err := New(testFiles)
	assert.NilError(t, err, "Failed to create VM")

	ctx, err := vm.File("testdata/main.star")
	assert.NilError(t, err, "Failed to load main file")

	result, err := ctx.Call("main")
	assert.NilError(t, err, "Failed to call main function")

	if intResult, ok := result.(starlark.Int); ok {
		intValue, ok := intResult.Int64()
		assert.Assert(t, ok, "Failed to convert result to Int64")
		assert.Equal(t, intValue, int64(10), "main() returned %v, want %v", intValue, 10)
	} else {
		t.Errorf("Expected result to be a starlark.Int, got %T", result)
	}
}

func TestImportAcrossFileSystems(t *testing.T) {
	vm, err := New(mainFiles, utilityFiles)
	assert.NilError(t, err, "Failed to create VM")

	ctx, err := vm.File("testdata/main.star")
	assert.NilError(t, err, "Failed to load main file")

	result, err := ctx.Call("main")
	assert.NilError(t, err, "Failed to call main function")

	if intResult, ok := result.(starlark.Int); ok {
		intValue, ok := intResult.Int64()
		assert.Assert(t, ok, "Failed to convert result to Int64")
		assert.Equal(t, intValue, int64(10), "main() returned %v, want %v", intValue, 10)
	} else {
		t.Errorf("Expected result to be a starlark.Int, got %T", result)
	}
}
