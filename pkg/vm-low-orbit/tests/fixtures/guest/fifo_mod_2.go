//go:build fifo_mod_2

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"
	"io"

	"github.com/taubyte/go-sdk/event"
	"github.com/taubyte/go-sdk/i2mv/fifo"
)

//go:wasm-module fs//tmp/1710338774/artifact.wasm
//export fifo_1
func fifoCall() uint32

//export fifo_2
func fifo_2(e event.Event) {
	id := fifoCall()
	h, err := e.HTTP()
	if err != nil {
		return
	}

	ff, err := fifo.Open(id)
	if err != nil {
		h.Write([]byte(fmt.Sprintf("unable to open fifo id `%d` with: %s", id, err)))
		h.Return(404)
		return
	}

	data, err := io.ReadAll(ff)
	if err != nil {
		h.Write([]byte(fmt.Sprintf("unable to read fifo with: %s", err)))
		h.Return(404)
		return
	}

	if string(data) != "hello world" {
		h.Write([]byte(fmt.Sprintf("`%d` expected data `hello world` got `%s`", id, string(data))))
		h.Return(404)
	}

	h.Write([]byte(string(data)))
	h.Return(200)
}
