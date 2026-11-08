package raft

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gotest.tools/v3/assert"
)

func TestKVFSM_Restore_WithManyKeys(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, "/raft/test")

	entries := make(map[string]CRDTEntry)
	for i := 0; i < 100; i++ {
		key := "key" + string(rune(i))
		entries[key] = CRDTEntry{
			Value:     []byte("value" + string(rune(i))),
			Timestamp: uint64(i + 1),
		}
	}

	payload := snapshotPayload{Data: entries, Clock: 100}
	data, err := marshalPayload(payload)
	require.NoError(t, err)

	err = fsm.Restore(data)
	require.NoError(t, err)

	for i := 0; i < 100; i++ {
		key := "key" + string(rune(i))
		val, found := fsm.Get(key)
		assert.Assert(t, found)
		expected := "value" + string(rune(i))
		assert.Equal(t, string(val), expected)
	}
}

func TestFsmAdapter_Restore_Comprehensive(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(t.Context(), store, "/raft/test")

	payload := snapshotPayload{
		Data: map[string]CRDTEntry{
			"key1": {Value: []byte("value1"), Timestamp: 1},
			"key2": {Value: []byte("value2"), Timestamp: 2},
		},
		Clock: 2,
	}
	data, err := marshalPayload(payload)
	require.NoError(t, err)

	err = fsm.Restore(data)
	require.NoError(t, err)

	val, found := fsm.Get("key1")
	assert.Assert(t, found)
	assert.Equal(t, string(val), "value1")
}
