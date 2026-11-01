// A function in Go, built by the standard toolchain, answering the language,
// method, path and body. Standard Go has no WIT bindings generator (gc's
// wasmexport refuses wit-bindgen-go's types), so the export is lowered by hand per
// the canonical ABI: `handle` flattens to eight i32 parameters and an i32 response
// pointer.
package main

import "unsafe"

// The realloc is cabi_export_realloc, not cabi_realloc: wit-component would hand
// cabi_realloc to the WASI adapter too, which calls it inside the Go runtime's own
// WASI calls during _initialize, where Go cannot be re-entered.

// held keeps what the host was handed alive until it has read it; the
// component model calls cabi_post_handle once it has.
var held [][]byte

//go:wasmexport cabi_export_realloc
func cabiRealloc(old, oldSize, align, size uint32) uint32 {
	buf := make([]byte, size+align)
	held = append(held, buf)
	at := uintptr(unsafe.Pointer(&buf[0]))
	at = (at + uintptr(align) - 1) &^ (uintptr(align) - 1)
	if old != 0 {
		copy(unsafe.Slice((*byte)(unsafe.Pointer(at)), size), unsafe.Slice((*byte)(unsafe.Pointer(uintptr(old))), oldSize))
	}
	return uint32(at)
}

func bytesAt(ptr, n uint32) []byte {
	if n == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), n)
}

func addressOf(b []byte) uint32 {
	return uint32(uintptr(unsafe.Pointer(&b[0])))
}

// handle receives the request's method, path, headers and body, each as a
// pointer and a length, and answers where the response is: a u16 status at 0,
// then the headers and the body as pointer and length pairs from offset 4.
//
//go:wasmexport handle
func handle(methodPtr, methodLen, pathPtr, pathLen, headersPtr, headersLen, bodyPtr, bodyLen uint32) uint32 {
	body := []byte("go " + string(bytesAt(methodPtr, methodLen)) + " " + string(bytesAt(pathPtr, pathLen)) + " ")
	body = append(body, bytesAt(bodyPtr, bodyLen)...)

	response := make([]byte, 20)
	held = append(held, body, response)
	put16(response[0:], 200)
	put32(response[12:], addressOf(body))
	put32(response[16:], uint32(len(body)))
	return addressOf(response)
}

//go:wasmexport cabi_post_handle
func cabiPostHandle(uint32) {
	held = nil
}

func put16(b []byte, v uint16) {
	b[0], b[1] = byte(v), byte(v>>8)
}

func put32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}

func main() {}
