//go:build storage_method

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"

	"github.com/taubyte/go-sdk/event"
	"github.com/taubyte/go-sdk/storage"
)

//export methodStorage
func methodStorage(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		fmt.Println("ERR", err)
		return 1
	}

	_, err = storage.New("/smartop/storage")
	if err != nil {
		fmt.Println("ERR", err)
		return 1
	}

	_, err = h.Write([]byte("Success"))
	if err != nil {
		fmt.Println("ERR", err)
		return 1
	}

	return 0
}
