//go:build rand

package main

//lint:file-ignore U1000 compiled file

import (
	"bytes"

	"github.com/taubyte/go-sdk/crypto/rand"
	"github.com/taubyte/go-sdk/event"
)

//export randtest
func randtest(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		panic(err)
	}

	reader1 := rand.NewReader()
	reader2 := rand.NewReader()
	reader3 := rand.NewReader()

	buffer1 := make([]byte, 32)
	buffer2 := make([]byte, 32)
	buffer3 := make([]byte, 32)

	reader1.Read(buffer1)
	reader2.Read(buffer2)
	reader3.Read(buffer3)

	errReturn := func(msg string) uint32 {
		h.Write([]byte(msg))
		h.Return(404)
		return 1
	}

	if bytes.Equal(buffer1, buffer2) {
		return errReturn("buffer1 and buffer2 should not be the same")
	}

	if bytes.Equal(buffer1, buffer3) {
		return errReturn("Buffer1 and buffer3 should not be the same")
	}

	if bytes.Equal(buffer2, buffer3) {
		return errReturn("Buffer2 and buffer3 should not be the same")
	}

	h.Write([]byte("All buffers are random"))
	h.Return(200)
	return 1
}
