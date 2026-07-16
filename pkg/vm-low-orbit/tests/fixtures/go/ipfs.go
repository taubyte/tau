//go:build ipfs

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"
	"io"

	"github.com/taubyte/go-sdk/event"
	"github.com/taubyte/go-sdk/ipfs/client"
)

//export someipfs
func someipfs(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	if err := runTestIPFS(); err != nil {
		h.Write([]byte(fmt.Sprintf("runTestIPFS failed with %v", err)))
		return 1
	}
	h.Write([]byte(`{"ping": "pong"}`))

	return 0
}

// runTestIPFS exercises the ipfs content host ABI end to end: create a content
// buffer, write/seek/read it back in-process, push it (host addresses it by
// cid), then open that cid and confirm the same bytes and cid come back. It
// asserts the round-trip, not a specific IPFS cid (the backend is a mock).
func runTestIPFS() error {
	data := []byte("Hello World")
	data2 := []byte(" Hello World AGAIN")

	ipfsClient, err := client.New()
	if err != nil {
		return err
	}

	content, err := ipfsClient.Create()
	if err != nil {
		return fmt.Errorf("create content: %w", err)
	}

	if _, err = content.Write(data); err != nil {
		return fmt.Errorf("write data: %w", err)
	}

	// reading at the end yields nothing
	readData, err := io.ReadAll(content)
	if err != nil {
		return fmt.Errorf("read at end: %w", err)
	}
	if len(readData) > 0 {
		return fmt.Errorf("expected empty read at end, got %q", readData)
	}

	// seek back, read what we wrote
	if _, err = content.Seek(0, 0); err != nil {
		return fmt.Errorf("seek: %w", err)
	}
	readData, err = io.ReadAll(content)
	if err != nil {
		return fmt.Errorf("read after seek: %w", err)
	}
	if string(readData) != string(data) {
		return fmt.Errorf("read %q, want %q", readData, data)
	}

	// append more, read the whole thing back
	if _, err = content.Write(data2); err != nil {
		return fmt.Errorf("write data2: %w", err)
	}
	if _, err = content.Seek(0, 0); err != nil {
		return fmt.Errorf("seek 2: %w", err)
	}
	readData, err = io.ReadAll(content)
	if err != nil {
		return fmt.Errorf("read all: %w", err)
	}
	if len(readData) != len(data)+len(data2) {
		return fmt.Errorf("read len %d, want %d", len(readData), len(data)+len(data2))
	}

	// push: the host addresses the content and hands back a cid
	if _, err = content.Seek(0, 0); err != nil {
		return fmt.Errorf("seek before push: %w", err)
	}
	cid, err := content.Push()
	if err != nil {
		return fmt.Errorf("push: %w", err)
	}
	if !cid.Defined() {
		return fmt.Errorf("push returned an undefined cid")
	}

	// open the pushed cid: same bytes, same cid
	getContent, err := ipfsClient.Open(cid)
	if err != nil {
		return fmt.Errorf("open %s: %w", cid, err)
	}
	if _, err = getContent.Seek(0, 0); err != nil {
		return fmt.Errorf("seek opened: %w", err)
	}
	readData, err = io.ReadAll(getContent)
	if err != nil {
		return fmt.Errorf("read opened: %w", err)
	}
	if len(readData) != len(data)+len(data2) {
		return fmt.Errorf("opened len %d, want %d", len(readData), len(data)+len(data2))
	}

	getCid, err := getContent.Cid()
	if err != nil {
		return fmt.Errorf("opened cid: %w", err)
	}
	if getCid.String() != cid.String() {
		return fmt.Errorf("opened cid %s != pushed cid %s", getCid, cid)
	}

	return nil
}
