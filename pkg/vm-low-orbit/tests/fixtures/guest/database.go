//go:build database

package main

//lint:file-ignore U1000 compiled file

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/taubyte/go-sdk/database"
	"github.com/taubyte/go-sdk/event"
)

var (
	expectedString = "DatabaseTest"
	expected       = []byte(expectedString)
)

//export databasetest
func databasetest(e event.Event) uint32 {
	keystore, err := database.New("/basic/v1")
	if err != nil {
		panic(errors.New("Creating new database failed with:" + err.Error()))
	}

	err = keystore.Put("test", expected)
	if err != nil {
		panic(errors.New("Keystore put failed with:" + err.Error()))
	}

	err = keystore.Put("test/1", expected)
	if err != nil {
		panic(errors.New("Keystore put failed with:" + err.Error()))
	}

	err = keystore.Put("test/2s", expected)
	if err != nil {
		panic(errors.New("Keystore put failed with:" + err.Error()))
	}

	data, err := keystore.Get("test")
	if err != nil {
		panic(errors.New("Keystore get failed with:" + err.Error()))
	}

	h, err := e.HTTP()
	if err != nil {
		panic(err)
	}

	_, err = h.Write(data)
	if err != nil {
		panic(err)
	}

	if data == nil {
		fmt.Println("Get returned nil data")
	}

	if comparison := bytes.Compare(data, expected); comparison != 0 {
		panic("Data sent not same as data got")
	}

	err = keystore.Delete("test")
	if err != nil {
		panic(err)
	}

	resp, err := keystore.Get("test")
	if err == nil {
		panic(errors.New("expecting this to fail after delete"))
	}

	if len(resp) != 0 {
		panic(errors.New("response should be empty"))
	}

	keys, err := keystore.List("")
	if err != nil {
		panic(errors.New("failed calling keystore list"))
	}

	if len(keys) != 4 {
		panic(fmt.Sprintf("expected 4 keys got %d", len(keys)))
	}

	keys, err = keystore.List("test")
	if err != nil {
		panic(errors.New("failed calling keystore list"))
	}

	if len(keys) != 2 {
		panic(fmt.Sprintf("expected 2 keys got %d", len(keys)))
	}

	return 0
}
