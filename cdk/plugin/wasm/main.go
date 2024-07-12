//go:build wasi || wasm
// +build wasi wasm

package main

import (
	_ "github.com/extism/go-pdk/wasi-reactor"
)

func main() {}
