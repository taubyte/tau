package main

import (
	"context"

	"github.com/taubyte/tau/pkg/vm-orbit/satellite"
)

// plugin.Export() takes a structure with methods
// methods with the W_prefix will be exported to the wasm module
type helloWorlder struct{}

var helloWorld = "hello world!"

// our dFunc that will be calling this method will need to know the length of the written string
// to allocate enough memory for the data to be written
func (t *helloWorlder) W_helloSize(ctx context.Context, module satellite.Module, sizePtr uint32) uint32 {
	if _, err := module.WriteStringSize(sizePtr, helloWorld); err != nil {
		return 1
	}

	return 0
}

// this method will write the actual data, this will be called after our dfunc has resolved the data's size
func (t *helloWorlder) W_hello(ctx context.Context, module satellite.Module, stringPtr uint32) uint32 {
	if _, err := module.WriteString(stringPtr, helloWorld); err != nil {
		return 1
	}

	return 0
}
