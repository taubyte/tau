package command

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/p2p/streams/packer"
)

func TestNew(t *testing.T) {
	body := Body{"key": "value"}
	cmd := New("testCommand", body)

	assert.NotNil(t, cmd)
	assert.Equal(t, "testCommand", cmd.Command)
	assert.Equal(t, body, cmd.Body)
}

func TestCommand_Name(t *testing.T) {
	cmd := New("myCommand", Body{})
	assert.Equal(t, "myCommand", cmd.Name())
}

func TestCommand_SetName(t *testing.T) {
	cmd := New("original", Body{})

	err := cmd.SetName("newName")
	require.NoError(t, err)
	assert.Equal(t, "newName", cmd.Name())
}

func TestCommand_SetName_InvalidType(t *testing.T) {
	cmd := New("original", Body{})

	err := cmd.SetName(12345)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot convert to string")
}

func TestCommand_Get(t *testing.T) {
	body := Body{
		"key1": "value1",
		"key2": 42,
	}
	cmd := New("test", body)

	val, ok := cmd.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", val)

	val, ok = cmd.Get("key2")
	assert.True(t, ok)
	assert.Equal(t, 42, val)

	_, ok = cmd.Get("nonexistent")
	assert.False(t, ok)
}

func TestCommand_Set(t *testing.T) {
	cmd := New("test", Body{})

	cmd.Set("newKey", "newValue")
	val, ok := cmd.Get("newKey")
	assert.True(t, ok)
	assert.Equal(t, "newValue", val)
}

func TestCommand_Delete(t *testing.T) {
	body := Body{"key": "value"}
	cmd := New("test", body)

	_, ok := cmd.Get("key")
	assert.True(t, ok)

	cmd.Delete("key")

	_, ok = cmd.Get("key")
	assert.False(t, ok)
}

func TestCommand_Raw(t *testing.T) {
	body := Body{
		"key1": "value1",
		"key2": 42,
	}
	cmd := New("test", body)

	raw := cmd.Raw()
	assert.Equal(t, body["key1"], raw["key1"])
	assert.Equal(t, body["key2"], raw["key2"])
	assert.Equal(t, len(body), len(raw))
}

func TestCommand_EncodeAndDecode(t *testing.T) {
	body := Body{
		"string": "hello",
		"number": float64(42), // CBOR uses float64 for numbers
		"bool":   true,
	}
	cmd := New("testCmd", body)

	var buf bytes.Buffer
	err := cmd.Encode(&buf)
	require.NoError(t, err)

	// Decode
	decoded, err := Decode(nil, &buf)
	require.NoError(t, err)

	assert.Equal(t, cmd.Command, decoded.Command)
	assert.Equal(t, cmd.Body["string"], decoded.Body["string"])
	assert.Equal(t, cmd.Body["bool"], decoded.Body["bool"])
}

func TestCommand_Connection_NoConnection(t *testing.T) {
	cmd := New("test", Body{})

	_, err := cmd.Connection()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no connection found")
}

func TestDecode_EmptyReader(t *testing.T) {
	var buf bytes.Buffer
	_, err := Decode(nil, &buf)
	assert.Error(t, err)
}

func TestMagicAndVersion(t *testing.T) {
	assert.Equal(t, Magic[0], byte(0x01))
	assert.Equal(t, Magic[1], byte(0xec))
	assert.Equal(t, Version, packer.Version(0x01))
}

func TestBody_Type(t *testing.T) {
	body := Body{
		"key": "value",
	}
	assert.IsType(t, Body{}, body)
	assert.IsType(t, map[string]interface{}{}, map[string]interface{}(body))
}

func TestCommand_Delete_NonExistentKey(t *testing.T) {
	cmd := New("test", Body{"existing": "value"})

	// Should not panic when deleting non-existent key
	assert.NotPanics(t, func() {
		cmd.Delete("nonexistent")
	})

	// Existing key should still be there
	val, ok := cmd.Get("existing")
	assert.True(t, ok)
	assert.Equal(t, "value", val)
}

func TestCommand_SetOverwrite(t *testing.T) {
	cmd := New("test", Body{"key": "original"})

	cmd.Set("key", "updated")
	val, ok := cmd.Get("key")
	assert.True(t, ok)
	assert.Equal(t, "updated", val)
}

func TestCommand_BodyWithComplexTypes(t *testing.T) {
	body := Body{
		"nested": map[string]interface{}{
			"inner": "value",
		},
		"array": []interface{}{"a", "b", "c"},
	}
	cmd := New("complex", body)

	var buf bytes.Buffer
	err := cmd.Encode(&buf)
	require.NoError(t, err)

	decoded, err := Decode(nil, &buf)
	require.NoError(t, err)

	assert.Equal(t, cmd.Command, decoded.Command)
}
