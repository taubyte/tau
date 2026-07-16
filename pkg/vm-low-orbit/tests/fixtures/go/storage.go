//go:build storage

package main

//lint:file-ignore U1000 compiled file

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/taubyte/go-sdk/event"
	httpEvent "github.com/taubyte/go-sdk/http/event"
	"github.com/taubyte/go-sdk/storage"
)

//export storagetest
func storagetest(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	if err := _storagetest(h); err != nil {
		h.Write([]byte(fmt.Sprintf("_storagetest failed with %v", err)))
		return 1
	}
	h.Write([]byte(`{"ping": "pong"}`))

	return 0
}

// _storagetest exercises the storage host ABI end to end: add versions, read
// each back byte-for-byte, list versions/files, fetch a cid, delete. It asserts
// the round-trip through the plugin, not IPFS content-addressing (the backend is
// an in-memory mock), so the data it stores is fixed, not random.
func _storagetest(h httpEvent.Event) error {
	_storage, err := storage.New("/basic/123")
	if err != nil {
		return errors.New("creating new storage failed with: " + err.Error())
	}

	data1 := []byte("first version")
	data2 := []byte("second version")

	video := _storage.File("video")

	// add v1, read it back
	version, err := video.Add(data1, false)
	if err != nil {
		return fmt.Errorf("add v1: %w", err)
	}
	if version != 1 {
		return fmt.Errorf("expected version 1, got %d", version)
	}
	if err := readBack(video.Version(1), data1); err != nil {
		return fmt.Errorf("v1 round-trip: %w", err)
	}

	// current version tracks the latest add
	version, err = video.CurrentVersion()
	if err != nil {
		return fmt.Errorf("current version: %w", err)
	}
	if version != 1 {
		return fmt.Errorf("expected current version 1, got %d", version)
	}

	// a cid must round-trip through the host (defined, non-empty)
	cid, err := _storage.Cid("video")
	if err != nil {
		return fmt.Errorf("cid: %w", err)
	}
	if !cid.Defined() {
		return errors.New("expected a defined cid")
	}

	// add v2, read it back
	version, err = video.Add(data2, false)
	if err != nil {
		return fmt.Errorf("add v2: %w", err)
	}
	if version != 2 {
		return fmt.Errorf("expected version 2, got %d", version)
	}
	if err := readBack(video.Version(2), data2); err != nil {
		return fmt.Errorf("v2 round-trip: %w", err)
	}

	// two versions listed
	versions, err := video.Versions()
	if err != nil {
		return fmt.Errorf("list versions: %w", err)
	}
	if len(versions) != 2 {
		return fmt.Errorf("expected 2 versions, got %d", len(versions))
	}

	// ListFiles returns one entry per version (video has v1 + v2 now)
	files, err := _storage.ListFiles()
	if err != nil {
		return fmt.Errorf("list files: %w", err)
	}
	if len(files) != 2 {
		return fmt.Errorf("expected 2 file entries, got %d", len(files))
	}

	// delete v1, only v2 remains
	if err := video.Version(1).Delete(); err != nil {
		return fmt.Errorf("delete v1: %w", err)
	}
	versions, err = video.Versions()
	if err != nil {
		return fmt.Errorf("list versions after delete: %w", err)
	}
	if len(versions) != 1 {
		return fmt.Errorf("expected 1 version after delete, got %d", len(versions))
	}

	return nil
}

func readBack(v *storage.VersionedFile, want []byte) error {
	file, err := v.GetFile()
	if err != nil {
		return err
	}
	got := make([]byte, len(want))
	if _, err := file.Read(got); err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("data mismatch: got %q want %q", got, want)
	}
	return nil
}
