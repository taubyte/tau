//go:build fifo_mod_1

package main

//lint:file-ignore U1000 compiled file

import (
	"github.com/taubyte/go-sdk/i2mv/fifo"
)

//export fifo_1
func fifo_1() uint32 {
	data := []byte("hello world")
	id, ff := fifo.New(true)

	ff.Write(data)

	return id
}
