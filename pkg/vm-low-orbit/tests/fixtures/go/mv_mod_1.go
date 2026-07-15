//go:build mv_mod_1

package main

//lint:file-ignore U1000 compiled file

import (
	"github.com/taubyte/go-sdk/i2mv/memview"
)

//export mv_1
func mv_1() uint32 {
	data := []byte("hello world")
	id, _, err := memview.New(data, true)
	if err != nil {
		panic(err)
	}

	return id
}
