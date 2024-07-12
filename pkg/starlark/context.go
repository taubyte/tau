package starlark

import (
	"fmt"

	"go.starlark.net/starlark"
)

func (c *ctx) Call(functionName string, args ...starlark.Value) (starlark.Value, error) {
	fn, ok := c.globals[functionName].(*starlark.Function)
	if !ok {
		return nil, fmt.Errorf("function %s not found", functionName)
	}
	result, err := starlark.Call(c.thread, fn, starlark.Tuple(args), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to call function %s: %w", functionName, err)
	}
	return result, nil
}

func (c *ctx) CallWithNative(functionName string, args ...any) (any, error) {
	fn, ok := c.globals[functionName].(*starlark.Function)
	if !ok {
		return nil, fmt.Errorf("function %s not found", functionName)
	}

	// Convert native Go arguments to Starlark values
	starlarkArgs := make([]starlark.Value, len(args))
	for i, arg := range args {
		val, err := convertToStarlark(arg)
		if err != nil {
			return nil, err
		}
		starlarkArgs[i] = val
	}

	// Call the Starlark function
	result, err := starlark.Call(c.thread, fn, starlark.Tuple(starlarkArgs), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to call function %s: %w", functionName, err)
	}

	// Convert the result back to a Go value
	return convertFromStarlarkBasedOnValue(result), nil
}
