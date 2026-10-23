// A function in Go, built by the standard toolchain: it answers the language,
// the method, the path and the body, so a caller can tell which component
// served it.
//
// Standard Go has no WIT bindings generator: wit-bindgen-go's output uses
// parameter types gc's go:wasmexport and go:wasmimport refuse. So the export
// is lowered by hand, following the component model's canonical ABI for tau's
// handler world, which flattens `handle: func(req: request) -> response` to
// eight i32 parameters and an i32 pointer to the response.
package main

import "unsafe"

// THE REALLOC IS cabi_export_realloc AND NOT cabi_realloc. wit-component
// hands a main module's cabi_realloc to the WASI adapter too, which calls it
// from inside the Go runtime's own WASI calls during _initialize; Go cannot be
// re-entered there and throws. Under this name it serves only the export, and
// the adapter allocates for itself.

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
