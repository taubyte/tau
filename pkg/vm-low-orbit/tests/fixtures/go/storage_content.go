//go:build storage_content

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"
	"io"

	"github.com/taubyte/go-sdk/event"
	"github.com/taubyte/go-sdk/storage"
)

var (
	expectedStorageCid = "bafybeidqnk6czrgcaydbxw54tf2qpjmd5pcefpeoudytygrbukfoaawo4i"
)

//export storageContentTest
func storageContentTest(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	if err := _storageContentTest(); err != nil {
		h.Write([]byte(fmt.Sprintf("storageContentTest failed with %v", err)))
		return 1
	}
	h.Write([]byte(`{"ping": "pong"}`))

	return 0
}

func _storageContentTest() error {
	data := []byte("Hello World")
	data2 := []byte(" Hello World AGAIN")

	content, err := storage.Create()
	if err != nil {
		return fmt.Errorf("failed creating new content with %v", err)
	}

	_, err = content.Write(data)
	if err != nil {
		return fmt.Errorf("failed writing with %v", err)
	}

	// Should fail since its at the end
	readData, err := io.ReadAll(content)
	if err != nil {
		return fmt.Errorf("read empty failed with %v", err)
	}
	if len(readData) > 0 {
		return fmt.Errorf("expected readData to be empty got %s", string(readData))
	}

	_, err = content.Seek(0, 0)
	if err != nil {
		return fmt.Errorf("failed seek with %v", err)
	}

	readData, err = io.ReadAll(content)
	if err != nil {
		return fmt.Errorf("failed first reading file with %v", err)
	}
	if string(readData) != string(data) {
		return fmt.Errorf("not matching string in read %s != %s", string(readData), string(data))
	}

	_, err = content.Write(data2)
	if err != nil {
		return fmt.Errorf("failed writing data to file with %v", err)
	}

	_, err = content.Seek(0, 0)
	if err != nil {
		return fmt.Errorf("failed seek 2 with %v", err)
	}

	readData, err = io.ReadAll(content)
	if err != nil {
		return fmt.Errorf("failed reading file with %v", err)
	}

	expectedLength := len(data) + len(data2)
	if len(readData) != expectedLength {
		return fmt.Errorf("read is not the same length as written %d != %d", len(readData), expectedLength)
	}

	cid, err := content.Push()
	if err != nil {
		return fmt.Errorf("failed pushing file with %v", err)
	}

	if cid.String() != expectedStorageCid {
		return fmt.Errorf("CID's do not match after push %s != %s", cid.String(), expectedStorageCid)
	}

	getContent, err := storage.Open(cid)
	if err != nil {
		return fmt.Errorf("failed getting cid %s with %v", cid.String(), err)
	}

	_, err = getContent.Seek(0, 0)
	if err != nil {
		return fmt.Errorf("failed seeking getContent wtih %v", err)
	}

	readData, err = io.ReadAll(getContent)
	if err != nil {
		return fmt.Errorf("failed last reading file with %v", err)
	}

	if len(readData) != len(data)+len(data2) {
		return fmt.Errorf("read is not the same length as written %d != %d", len(readData), len(data)+len(data2))
	}

	getCid, err := getContent.Cid()
	if err != nil {
		return fmt.Errorf("failed getting cid with %v", err)
	}

	if getCid.String() != expectedStorageCid {
		return fmt.Errorf("CID's do not match after open %s != %s", getCid.String(), cid.String())
	}

	return nil
}
