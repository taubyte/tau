package raft

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/hashicorp/raft"
	"github.com/ipfs/go-datastore"
	dsync "github.com/ipfs/go-datastore/sync"
)

func newTestStore() datastore.Batching {
	return dsync.MutexWrap(datastore.NewMapDatastore())
}

func TestKVFSM_Apply_Set(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "mykey", Value: []byte("myvalue")},
	}
	data, err := cbor.Marshal(cmd)
	if err != nil {
		t.Fatalf("failed to marshal command: %v", err)
	}

	log := &raft.Log{Data: data, Index: 1}
	resp := fsm.Apply(log)
	fsmResp, ok := resp.(FSMResponse)
	if !ok {
		t.Fatalf("expected FSMResponse, got %T", resp)
	}

	if fsmResp.Error != nil {
		t.Errorf("expected no error, got %v", fsmResp.Error)
	}

	// Verify stored
	val, ok := fsm.Get("mykey")
	if !ok {
		t.Error("expected key to exist")
	}
	if !bytes.Equal(val, []byte("myvalue")) {
		t.Errorf("expected value 'myvalue', got '%s'", val)
	}
}

func TestKVFSM_Apply_Delete(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	// First set a value
	setCmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "mykey", Value: []byte("myvalue")},
	}
	setData, _ := cbor.Marshal(setCmd)
	fsm.Apply(&raft.Log{Data: setData, Index: 1})

	// Verify it exists
	val, ok := fsm.Get("mykey")
	if !ok || !bytes.Equal(val, []byte("myvalue")) {
		t.Fatal("key should exist before delete")
	}

	// Then delete
	delCmd := Command{
		Type:   CommandDelete,
		Delete: &DeleteCommand{Key: "mykey"},
	}
	delData, _ := cbor.Marshal(delCmd)
	resp := fsm.Apply(&raft.Log{Data: delData, Index: 2})
	fsmResp, ok := resp.(FSMResponse)
	if !ok {
		t.Fatalf("expected FSMResponse, got %T", resp)
	}
	if fsmResp.Error != nil {
		t.Errorf("expected no error, got %v", fsmResp.Error)
	}

	// Verify deleted
	_, ok = fsm.Get("mykey")
	if ok {
		t.Error("expected key to be deleted")
	}
}

func TestKVFSM_Apply_InvalidCommand(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	// Invalid CBOR data
	log := &raft.Log{Data: []byte("invalid"), Index: 1}
	resp := fsm.Apply(log)
	fsmResp, ok := resp.(FSMResponse)
	if !ok {
		t.Fatalf("expected FSMResponse, got %T", resp)
	}
	if fsmResp.Error != ErrInvalidCommand {
		t.Errorf("expected ErrInvalidCommand, got %v", fsmResp.Error)
	}
}

func TestKVFSM_Apply_UnknownCommandType(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	cmd := Command{
		Type: CommandType(99), // Unknown type
	}
	data, _ := cbor.Marshal(cmd)
	log := &raft.Log{Data: data, Index: 1}
	resp := fsm.Apply(log)
	fsmResp, ok := resp.(FSMResponse)
	if !ok {
		t.Fatalf("expected FSMResponse, got %T", resp)
	}
	if fsmResp.Error != ErrInvalidCommand {
		t.Errorf("expected ErrInvalidCommand, got %v", fsmResp.Error)
	}
}

func TestKVFSM_Apply_SetWithNilPayload(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	cmd := Command{
		Type: CommandSet,
		Set:  nil, // Missing payload
	}
	data, _ := cbor.Marshal(cmd)
	log := &raft.Log{Data: data, Index: 1}
	resp := fsm.Apply(log)
	fsmResp, ok := resp.(FSMResponse)
	if !ok {
		t.Fatalf("expected FSMResponse, got %T", resp)
	}
	if fsmResp.Error != ErrInvalidCommand {
		t.Errorf("expected ErrInvalidCommand, got %v", fsmResp.Error)
	}
}

func TestKVFSM_Get_NotFound(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	val, ok := fsm.Get("nonexistent")
	if ok {
		t.Error("expected key to not exist")
	}
	if val != nil {
		t.Error("expected nil value")
	}
}

func TestKVFSM_Keys(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	// Add some keys
	keys := []string{"config/a", "config/b", "data/x"}
	for i, key := range keys {
		cmd := Command{
			Type: CommandSet,
			Set:  &SetCommand{Key: key, Value: []byte("value")},
		}
		data, _ := cbor.Marshal(cmd)
		fsm.Apply(&raft.Log{Data: data, Index: uint64(i + 1)})
	}

	// Get all keys
	allKeys := fsm.Keys("")
	if len(allKeys) != 3 {
		t.Errorf("expected 3 keys, got %d", len(allKeys))
	}

	// Get keys with prefix
	configKeys := fsm.Keys("config/")
	if len(configKeys) != 2 {
		t.Errorf("expected 2 config keys, got %d", len(configKeys))
	}
}

func TestKVFSM_Snapshot_Restore(t *testing.T) {
	store1 := newTestStore()
	fsm1 := newKVFSM(store1, "/raft/test")

	// Add some data
	for i, key := range []string{"key1", "key2", "key3"} {
		cmd := Command{
			Type: CommandSet,
			Set:  &SetCommand{Key: key, Value: []byte("value" + key)},
		}
		data, _ := cbor.Marshal(cmd)
		fsm1.Apply(&raft.Log{Data: data, Index: uint64(i + 1)})
	}

	// Create snapshot
	snap, err := fsm1.Snapshot()
	if err != nil {
		t.Fatalf("failed to create snapshot: %v", err)
	}

	// Write to buffer
	var buf bytes.Buffer
	sink := &testSnapshotSink{Writer: &buf}
	if err := snap.Persist(sink); err != nil {
		t.Fatalf("failed to persist snapshot: %v", err)
	}

	// Restore to new FSM
	store2 := newTestStore()
	fsm2 := newKVFSM(store2, "/raft/test")

	if err := fsm2.Restore(io.NopCloser(&buf)); err != nil {
		t.Fatalf("failed to restore snapshot: %v", err)
	}

	// Verify data restored
	for _, key := range []string{"key1", "key2", "key3"} {
		val, ok := fsm2.Get(key)
		if !ok {
			t.Errorf("expected key %s to exist after restore", key)
			continue
		}
		expected := []byte("value" + key)
		if !bytes.Equal(val, expected) {
			t.Errorf("expected value '%s', got '%s'", expected, val)
		}
	}
}

func TestEncodeSetCommand(t *testing.T) {
	data, err := encodeSetCommand("testkey", []byte("testvalue"))
	if err != nil {
		t.Fatalf("failed to encode: %v", err)
	}

	var cmd Command
	if err := cbor.Unmarshal(data, &cmd); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if cmd.Type != CommandSet {
		t.Errorf("expected CommandSet, got %d", cmd.Type)
	}
	if cmd.Set == nil {
		t.Fatal("expected Set to be non-nil")
	}
	if cmd.Set.Key != "testkey" {
		t.Errorf("expected key 'testkey', got '%s'", cmd.Set.Key)
	}
	if !bytes.Equal(cmd.Set.Value, []byte("testvalue")) {
		t.Errorf("expected value 'testvalue', got '%s'", cmd.Set.Value)
	}
}

func TestEncodeDeleteCommand(t *testing.T) {
	data, err := encodeDeleteCommand("testkey")
	if err != nil {
		t.Fatalf("failed to encode: %v", err)
	}

	var cmd Command
	if err := cbor.Unmarshal(data, &cmd); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if cmd.Type != CommandDelete {
		t.Errorf("expected CommandDelete, got %d", cmd.Type)
	}
	if cmd.Delete == nil {
		t.Fatal("expected Delete to be non-nil")
	}
	if cmd.Delete.Key != "testkey" {
		t.Errorf("expected key 'testkey', got '%s'", cmd.Delete.Key)
	}
}

// testSnapshotSink is a test implementation of raft.SnapshotSink
type testSnapshotSink struct {
	io.Writer
	cancelled bool
}

func (s *testSnapshotSink) ID() string { return "test" }

func (s *testSnapshotSink) Cancel() error {
	s.cancelled = true
	return nil
}

func (s *testSnapshotSink) Close() error { return nil }

func TestKVSnapshot_Release(t *testing.T) {
	snap := &kvSnapshot{data: make(map[string][]byte)}
	// Release should not panic
	snap.Release()
	// Call again should also not panic
	snap.Release()
}

func TestFsmAdapter_Apply(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "testkey", Value: []byte("testvalue")},
	}
	data, _ := cbor.Marshal(cmd)

	result := fsm.Apply(&raft.Log{Data: data, Index: 1})
	resp, ok := result.(FSMResponse)
	if !ok {
		t.Fatalf("expected FSMResponse, got %T", result)
	}
	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}

	// Verify value was set
	val, found := fsm.Get("testkey")
	if !found {
		t.Error("expected key to exist")
	}
	if string(val) != "testvalue" {
		t.Errorf("expected 'testvalue', got '%s'", val)
	}
}

func TestFsmAdapter_Snapshot(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	// Set some data
	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "snapkey", Value: []byte("snapvalue")},
	}
	data, _ := cbor.Marshal(cmd)
	fsm.Apply(&raft.Log{Data: data, Index: 1})

	snap, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if snap == nil {
		t.Error("expected non-nil snapshot")
	}
}

func TestFsmAdapter_Restore(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	// Create snapshot data
	snapshotData := map[string][]byte{
		"restored-key": []byte("restored-value"),
	}
	var buf bytes.Buffer
	if err := cbor.NewEncoder(&buf).Encode(snapshotData); err != nil {
		t.Fatalf("failed to encode snapshot: %v", err)
	}

	err := fsm.Restore(io.NopCloser(&buf))
	if err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	// Verify data was restored
	val, found := fsm.Get("restored-key")
	if !found {
		t.Error("expected key to exist after restore")
	}
	if string(val) != "restored-value" {
		t.Errorf("expected 'restored-value', got '%s'", val)
	}
}

func TestKVFSM_Restore_EmptyStore(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	// First add some data
	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "oldkey", Value: []byte("oldvalue")},
	}
	data, _ := cbor.Marshal(cmd)
	fsm.Apply(&raft.Log{Data: data, Index: 1})

	// Restore with empty data (should clear existing)
	emptyData, _ := cbor.Marshal(map[string][]byte{})
	if err := fsm.Restore(io.NopCloser(bytes.NewReader(emptyData))); err != nil {
		t.Fatalf("failed to restore: %v", err)
	}

	// Old data should be gone
	_, ok := fsm.Get("oldkey")
	if ok {
		t.Error("expected old key to be cleared after restore")
	}
}

func TestNewKVFSM(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	if fsm == nil {
		t.Fatal("expected non-nil FSM")
	}
	kvFSM, ok := fsm.(*kvFSM)
	if !ok {
		t.Fatal("expected *kvFSM type")
	}
	if kvFSM.store != store {
		t.Error("expected store to be set")
	}
	if kvFSM.prefix != "/raft/test" {
		t.Errorf("expected prefix '/raft/test', got '%s'", kvFSM.prefix)
	}
}

func TestKVFSM_DataKey(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	kvFSM, ok := fsm.(*kvFSM)
	if !ok {
		t.Fatal("expected *kvFSM type")
	}

	key := kvFSM.dataKey("mykey")
	expected := datastore.NewKey("/raft/test/data/mykey")

	if key != expected {
		t.Errorf("expected key %s, got %s", expected, key)
	}
}

func TestKVFSM_Concurrent(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	done := make(chan bool)

	// Concurrent writes
	go func() {
		for i := 0; i < 100; i++ {
			cmd := Command{
				Type: CommandSet,
				Set:  &SetCommand{Key: "key", Value: []byte("value")},
			}
			data, _ := cbor.Marshal(cmd)
			fsm.Apply(&raft.Log{Data: data, Index: uint64(i + 1)})
		}
		done <- true
	}()

	// Concurrent reads
	go func() {
		for i := 0; i < 100; i++ {
			fsm.Get("key")
		}
		done <- true
	}()

	<-done
	<-done
}

func TestKVFSM_Apply_DeleteWithNilPayload(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	cmd := Command{
		Type:   CommandDelete,
		Delete: nil, // Missing payload
	}
	data, _ := cbor.Marshal(cmd)
	log := &raft.Log{Data: data, Index: 1}
	resp := fsm.Apply(log)
	fsmResp, ok := resp.(FSMResponse)
	if !ok {
		t.Fatalf("expected FSMResponse, got %T", resp)
	}
	if fsmResp.Error != ErrInvalidCommand {
		t.Errorf("expected ErrInvalidCommand, got %v", fsmResp.Error)
	}
}

func TestKVSnapshot_Persist_Success(t *testing.T) {
	data := map[string][]byte{
		"key1": []byte("value1"),
		"key2": []byte("value2"),
	}
	snap := &kvSnapshot{data: data}

	var buf bytes.Buffer
	sink := &testSnapshotSink{Writer: &buf}

	err := snap.Persist(sink)
	if err != nil {
		t.Fatalf("persist failed: %v", err)
	}

	// Verify data can be unmarshaled
	var restored map[string][]byte
	if err := cbor.Unmarshal(buf.Bytes(), &restored); err != nil {
		t.Fatalf("failed to unmarshal snapshot: %v", err)
	}

	if len(restored) != 2 {
		t.Errorf("expected 2 keys, got %d", len(restored))
	}
}

func TestKVFSM_Keys_Empty(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	keys := fsm.Keys("")
	if len(keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(keys))
	}
}

func TestKVFSM_Restore_InvalidCBOR(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	err := fsm.Restore(io.NopCloser(bytes.NewReader([]byte("invalid cbor"))))
	if err == nil {
		t.Error("expected error for invalid CBOR")
	}
}

func TestKVSnapshot_Persist_Empty(t *testing.T) {
	snap := &kvSnapshot{data: map[string][]byte{}}

	var buf bytes.Buffer
	sink := &testSnapshotSink{Writer: &buf}

	err := snap.Persist(sink)
	if err != nil {
		t.Fatalf("persist failed: %v", err)
	}

	// Should have produced valid CBOR for empty map
	if buf.Len() == 0 {
		t.Error("expected non-empty output")
	}
}

type failingWriter struct {
	failOnWrite bool
	failOnClose bool
}

func (f *failingWriter) Write(p []byte) (n int, err error) {
	if f.failOnWrite {
		return 0, fmt.Errorf("write error")
	}
	return len(p), nil
}

func (f *failingWriter) Close() error {
	if f.failOnClose {
		return fmt.Errorf("close error")
	}
	return nil
}

func (f *failingWriter) Cancel() error {
	return nil
}

func (f *failingWriter) ID() string {
	return "test-id"
}

func TestKVSnapshot_Persist_WriteError(t *testing.T) {
	snap := &kvSnapshot{data: map[string][]byte{"key": []byte("value")}}
	sink := &failingWriter{failOnWrite: true}

	err := snap.Persist(sink)
	if err == nil {
		t.Error("expected error on write failure")
	}
}

func TestKVFSM_Keys_Error(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	// Get keys with no data
	keys := fsm.Keys("nonexistent/")
	if len(keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(keys))
	}
}

func TestKVFSM_Multiple_Set_Delete(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	// Set multiple keys
	for i := 0; i < 10; i++ {
		cmd := Command{
			Type: CommandSet,
			Set:  &SetCommand{Key: fmt.Sprintf("key%d", i), Value: []byte(fmt.Sprintf("value%d", i))},
		}
		data, _ := cbor.Marshal(cmd)
		fsm.Apply(&raft.Log{Data: data, Index: uint64(i + 1)})
	}

	// Verify all keys exist (empty prefix = all keys)
	keys := fsm.Keys("")
	if len(keys) != 10 {
		t.Errorf("expected 10 keys, got %d", len(keys))
	}

	// Verify individual keys can be retrieved
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key%d", i)
		val, ok := fsm.Get(key)
		if !ok {
			t.Errorf("expected key %s to exist", key)
			continue
		}
		expected := fmt.Sprintf("value%d", i)
		if string(val) != expected {
			t.Errorf("expected value %s, got %s", expected, val)
		}
	}

	// Delete half
	for i := 0; i < 5; i++ {
		cmd := Command{
			Type:   CommandDelete,
			Delete: &DeleteCommand{Key: fmt.Sprintf("key%d", i)},
		}
		data, _ := cbor.Marshal(cmd)
		fsm.Apply(&raft.Log{Data: data, Index: uint64(i + 11)})
	}

	// Verify only half remain
	keys = fsm.Keys("")
	if len(keys) != 5 {
		t.Errorf("expected 5 keys after delete, got %d", len(keys))
	}
}

// Mock context for testing
type testContext struct {
	context.Context
}

func newTestContext() context.Context {
	return context.Background()
}

func TestKVFSM_Keys_EmptyPrefix(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	// Add some keys
	for i := 0; i < 3; i++ {
		cmd := Command{
			Type: CommandSet,
			Set:  &SetCommand{Key: fmt.Sprintf("key-%d", i), Value: []byte("value")},
		}
		data, _ := cbor.Marshal(cmd)
		fsm.Apply(&raft.Log{Data: data, Index: uint64(i + 1)})
	}

	// Empty prefix should return all keys
	keys := fsm.Keys("")
	if len(keys) != 3 {
		t.Errorf("expected 3 keys with empty prefix, got %d", len(keys))
	}
}

func TestKVFSM_Keys_NoMatch(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	// Add some keys
	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "key-1", Value: []byte("value")},
	}
	data, _ := cbor.Marshal(cmd)
	fsm.Apply(&raft.Log{Data: data, Index: 1})

	// Prefix that doesn't match should return empty
	keys := fsm.Keys("nonexistent")
	if len(keys) != 0 {
		t.Errorf("expected 0 keys for nonexistent prefix, got %d", len(keys))
	}
}

func TestKVSnapshot_Release_WithData(t *testing.T) {
	// Create snapshot with some data
	snap := &kvSnapshot{
		data: map[string][]byte{
			"key1": []byte("value1"),
			"key2": []byte("value2"),
		},
	}

	// Release should not panic and is a no-op
	snap.Release()

	// Data should still be accessible after Release (no-op)
	if len(snap.data) != 2 {
		t.Errorf("expected 2 keys, got %d", len(snap.data))
	}
}

func TestKVSnapshot_Release_Empty(t *testing.T) {
	// Create empty snapshot
	snap := &kvSnapshot{
		data: make(map[string][]byte),
	}

	// Release should not panic
	snap.Release()
}

func TestKVSnapshot_Release_Nil(t *testing.T) {
	// Create snapshot with nil data
	snap := &kvSnapshot{
		data: nil,
	}

	// Release should not panic
	snap.Release()
}

func TestKVFSM_Restore_WithExistingData(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	// First add multiple keys
	for _, key := range []string{"key1", "key2", "key3"} {
		cmd := Command{
			Type: CommandSet,
			Set:  &SetCommand{Key: key, Value: []byte("original-" + key)},
		}
		data, _ := cbor.Marshal(cmd)
		fsm.Apply(&raft.Log{Data: data, Index: 1})
	}

	// Verify data exists
	val, ok := fsm.Get("key1")
	if !ok {
		t.Fatal("expected key1 to exist before restore")
	}
	if string(val) != "original-key1" {
		t.Fatalf("expected original-key1, got %s", val)
	}

	// Restore with different data
	newData := map[string][]byte{
		"newkey1": []byte("newvalue1"),
		"newkey2": []byte("newvalue2"),
	}
	restoreData, _ := cbor.Marshal(newData)

	if err := fsm.Restore(io.NopCloser(bytes.NewReader(restoreData))); err != nil {
		t.Fatalf("failed to restore: %v", err)
	}

	// Old data should be gone
	_, ok = fsm.Get("key1")
	if ok {
		t.Error("expected old key1 to be cleared after restore")
	}

	// New data should exist
	val, ok = fsm.Get("newkey1")
	if !ok {
		t.Error("expected newkey1 to exist after restore")
	}
	if string(val) != "newvalue1" {
		t.Errorf("expected newvalue1, got %s", val)
	}
}

func TestKVFSM_SnapshotAndRestore_RoundTrip(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/test")

	// Set up some data
	testData := map[string]string{
		"config/host": "localhost",
		"config/port": "8080",
		"users/admin": "admin-data",
		"users/guest": "guest-data",
	}
	for key, value := range testData {
		cmd := Command{
			Type: CommandSet,
			Set:  &SetCommand{Key: key, Value: []byte(value)},
		}
		data, _ := cbor.Marshal(cmd)
		fsm.Apply(&raft.Log{Data: data, Index: 1})
	}

	// Take a snapshot
	snapshot, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("failed to snapshot: %v", err)
	}

	// Persist snapshot to buffer
	var buf bytes.Buffer
	sink := &testSnapshotSink{Writer: &buf}
	if err := snapshot.Persist(sink); err != nil {
		t.Fatalf("failed to persist: %v", err)
	}

	// Create new FSM and restore
	store2 := newTestStore()
	fsm2 := newKVFSM(store2, "/raft/test2")

	if err := fsm2.Restore(io.NopCloser(bytes.NewReader(buf.Bytes()))); err != nil {
		t.Fatalf("failed to restore: %v", err)
	}

	// Verify all data restored correctly
	for key, expectedValue := range testData {
		val, ok := fsm2.Get(key)
		if !ok {
			t.Errorf("expected key %s to exist after restore", key)
			continue
		}
		if string(val) != expectedValue {
			t.Errorf("key %s: expected %s, got %s", key, expectedValue, val)
		}
	}
}
