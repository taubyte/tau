package lib

import (
	"fmt"
)

//go:wasm-module helloWorld
//export helloSize
func helloSize(sizePtr *uint32) uint32

//go:wasm-module helloWorld
//export hello
func hello(sizePtr *byte) uint32

//export helloWorld
func helloWorld() {
	var sizePtr uint32
	if err0 := helloSize(&sizePtr); err0 != 0 {
		panic(err0)
	}

	data := make([]byte, sizePtr)
	if err0 := hello(&data[0]); err0 != 0 {
		panic(err0)
	}

	fmt.Println(string(data))
}
