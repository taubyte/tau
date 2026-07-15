//go:build storage

package main

//lint:file-ignore U1000 compiled file

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"github.com/taubyte/go-sdk/event"
	httpEvent "github.com/taubyte/go-sdk/http/event"
	"github.com/taubyte/go-sdk/storage"
)

var (
	testData         string = "No storage needed to send to IPFS"
	expectedCid      string = "bafybeiavzbz2ugyergky6plyluts2evta5hu6ehhicmuow4zm6vd42pugq"
	expectedVideoCid string = "bafybeidnuo6zjxk2bpn4iyxjlafmuplej24yhsoceutcly2edbv523omge"
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

func _storagetest(h httpEvent.Event) error {
	_storage, err := storage.New("/basic/123")
	if err != nil {
		return errors.New("Creating new storage failed with:" + err.Error())
	}
	dataSize := 4

	//TODO:: Grab file from assets use bindata or embed
	inData1 := make([]byte, dataSize)
	rand.Read(inData1)

	_storage.RemainingCapacity()

	// EXECUTION: Name our data "video" and add inData1 with replace false
	// EXPECTATION: version set to 1
	video := _storage.File("video")
	version, err := video.Add(inData1, false)
	if err != nil {
		return err
	}
	if version != 1 {
		return errors.New("31 expected version to be 1")
	}

	version, err = video.CurrentVersion()
	if err != nil {
		return fmt.Errorf("32 ERROR %v", err)
	}

	if version != 1 {
		return errors.New("33 Expected version to be 1")
	}

	cid, err := _storage.Cid("video")
	if err != nil {
		return fmt.Errorf("failed storage cid with %s", err)
	}

	if cid.String() != expectedVideoCid {
		return fmt.Errorf("cid does not match. %s != %s", cid, expectedVideoCid)
	}

	// EXECUTION: List our "video" versions
	// EXPECTATION: Length of versions list is 1
	versions, err := video.Versions()
	if err != nil {
		return fmt.Errorf("39, %w", err)
	}

	if len(versions) != 1 {
		return errors.New("43 expected length of current versions to be 1")
	}

	// EXECUTION: Get our version 1 file
	// EXPECTATION: version 1 should be same as indata1
	file, err := video.Version(1).GetFile()
	if err != nil {
		return fmt.Errorf("50, %w", err)
	}

	outData, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("55, %w", err)
	}

	if !bytes.Equal(outData, inData1) {
		return errors.New("59 Data not same ")
	}

	inData2 := make([]byte, 4)
	rand.Read(inData2)

	// EXECUTION: Add our inData2 file with replace set to false
	// EXPECTATION: added file should be version 2
	version, err = video.Add(inData2, false)
	if err != nil {
		return fmt.Errorf("67, %w", err)
	}

	if version != 2 {
		return errors.New("73 wrong version")
	}

	// EXECUTION: List our "video" versions
	// EXPECTATION: Length of versions list is 2
	versions, err = video.Versions()
	if err != nil {
		return fmt.Errorf("78, %w", err)
	}

	if len(versions) != 2 {
		return errors.New("82 expected length of current versions to be 2")
	}

	// EXECUTION: Get our version 2 file
	// EXPECTATION: version 2 should be same as indata2
	file, err = video.Version(2).GetFile()
	if err != nil {
		return fmt.Errorf("91, %w", err)
	}

	outData, err = io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("96, %w", err)
	}

	if !bytes.Equal(outData, inData2) {
		return errors.New("100 Data not same ")
	}

	// EXECUTION: Add our inData1 file with replace set to true
	// EXPECTATION: added file should be version 2
	version, err = video.Add(inData1, true)
	if err != nil {
		return fmt.Errorf("107, %w", err)
	}

	files, err := _storage.ListFiles()
	if err != nil {
		return fmt.Errorf("109 list files failed with %v", err)
	}

	if len(files) != 2 {
		return fmt.Errorf("110 expected 2 files got %d", len(files))
	}

	if version != 2 {
		return errors.New("111 wrong version")
	}

	// EXECUTION: List our "video" versions
	// EXPECTATION: Length of versions list is 1
	versions, err = video.Versions()
	if err != nil {
		return fmt.Errorf("118, %w", err)
	}

	if len(versions) != 2 {
		return errors.New("122 expected length of current versions to be 2")
	}

	// EXECUTION: Get our version 2 file
	// EXPECTATION: version 2 should be same as indata1
	file, err = video.Version(2).GetFile()
	if err != nil {
		return fmt.Errorf("129, %w", err)
	}

	outData, err = io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("134, %w", err)
	}

	if !bytes.Equal(outData, inData1) {
		return errors.New("138 Data not same ")
	}

	// EXECUTION: Add our inData2 file with replace set to false
	// EXPECTATION: added file should be version 3
	version, err = video.Add(inData2, false)
	if err != nil {
		return fmt.Errorf("145, %w", err)
	}

	if version != 3 {
		return errors.New("149 wrong version")
	}

	// EXECUTION: List our "video" versions
	// EXPECTATION: Length of versions list is 3
	versions, err = video.Versions()
	if err != nil {
		return fmt.Errorf("156, %w", err)
	}

	if len(versions) != 3 {
		return errors.New("160 expected length of current versions to be 3")
	}

	// EXECUTION: Get our version 3 file
	// EXPECTATION: version 3 should be same as indata2
	file, err = video.Version(3).GetFile()
	if err != nil {
		return fmt.Errorf("167, %w", err)
	}

	outData, err = io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("172, %w", err)
	}

	if !bytes.Equal(outData, inData2) {
		return errors.New("176 Data not same ")
	}

	// EXECUTION: Delete our version 2 file
	// EXPECTATION: version 2 should be delete
	err = video.Version(2).Delete()
	if err != nil {
		return fmt.Errorf("183, %w", err)
	}

	// EXECUTION: Get our Version 3 file
	// EXPECTATION: version 3 should be same as inData2
	file, err = video.Version(3).GetFile()
	if err != nil {
		return fmt.Errorf("190, %w", err)
	}

	outData, err = io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("195, %w", err)
	}

	if !bytes.Equal(outData, inData2) {
		return errors.New("199 Data not same ")
	}

	// EXECUTION: List our "video" versions
	// EXPECTATION: Length of versions list is 3
	versions, err = video.Versions()
	if err != nil {
		return fmt.Errorf("206, %w", err)
	}

	if len(versions) != 2 {
		return errors.New("210 expected length of current versions to be 2")
	}

	// EXECUTION: Delete all file versions
	// EXPECTATION: Getting and listing file should fail
	err = video.DeleteAllVersions()
	if err != nil {
		return fmt.Errorf("215, %w", err)
	}

	_, err = video.Versions()
	if err == nil {
		return errors.New("222 exepcted this to error")
	}

	_, err = video.Version(1).GetFile()
	if err == nil {
		return errors.New("227 expected this to error")
	}

	_, err = video.Version(2).GetFile()
	if err == nil {
		return errors.New("232 expected this to error")
	}

	_, err = video.Version(3).GetFile()
	if err == nil {
		return errors.New("237 expected this to error")
	}

	_, err = video.Version(0).GetFile()
	if err == nil {
		return errors.New("242 expected this to error")
	}

	return nil
}
