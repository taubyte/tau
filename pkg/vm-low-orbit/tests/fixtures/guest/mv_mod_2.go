//go:build mv_mod_2

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"
	"io"

	"github.com/taubyte/go-sdk/event"
	"github.com/taubyte/go-sdk/i2mv/memview"
)

//go:wasm-module mv_writer
//export mv_1
func mvCall() uint32

//export mv_2
func mv_2(e event.Event) {
	id := mvCall()

	h, err := e.HTTP()
	if err != nil {
		return
	}

	mv, err := memview.Open(id)
	if err != nil {
		h.Write([]byte(fmt.Sprintf("unable to open mv id `%d` with: %s", id, err)))
		h.Return(404)
		return
	}

	data, err := io.ReadAll(mv)
	if err != nil {
		h.Write([]byte(fmt.Sprintf("unable to read mv with: %s", err)))
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
