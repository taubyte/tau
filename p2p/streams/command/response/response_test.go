package response

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponse_Get(t *testing.T) {
	resp := Response{
		"key1":   "value1",
		"key2":   42,
		"nested": map[string]interface{}{"inner": "value"},
	}

	val, err := resp.Get("key1")
	require.NoError(t, err)
	assert.Equal(t, "value1", val)

	val, err = resp.Get("key2")
	require.NoError(t, err)
	assert.Equal(t, 42, val)

	val, err = resp.Get("nested")
	require.NoError(t, err)
	assert.NotNil(t, val)
}

func TestResponse_Get_NotFound(t *testing.T) {
	resp := Response{
		"key": "value",
	}

	_, err := resp.Get("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

func TestResponse_Set(t *testing.T) {
	resp := Response{}

	resp.Set("key1", "value1")
	resp.Set("key2", 42)

	val, err := resp.Get("key1")
	require.NoError(t, err)
	assert.Equal(t, "value1", val)

	val, err = resp.Get("key2")
	require.NoError(t, err)
	assert.Equal(t, 42, val)
}

func TestResponse_Set_Overwrite(t *testing.T) {
	resp := Response{
		"key": "original",
	}

	resp.Set("key", "updated")
	val, err := resp.Get("key")
	require.NoError(t, err)
	assert.Equal(t, "updated", val)
}

func TestResponse_EncodeAndDecode(t *testing.T) {
	original := Response{
		"string": "hello",
		"number": float64(42),
		"bool":   true,
		"nested": map[string]interface{}{
			"inner": "value",
		},
	}

	var buf bytes.Buffer
	err := original.Encode(&buf)
	require.NoError(t, err)

	decoded, err := Decode(&buf)
	require.NoError(t, err)

	assert.Equal(t, original["string"], decoded["string"])
	assert.Equal(t, original["bool"], decoded["bool"])
}

func TestResponse_Decode_EmptyReader(t *testing.T) {
	var buf bytes.Buffer
	_, err := Decode(&buf)
	assert.Error(t, err)
}

func TestResponse_EmptyResponse(t *testing.T) {
	original := Response{}

	var buf bytes.Buffer
	err := original.Encode(&buf)
	require.NoError(t, err)

	decoded, err := Decode(&buf)
	require.NoError(t, err)

	assert.NotNil(t, decoded)
	assert.Empty(t, decoded)
}

func TestResponse_ComplexTypes(t *testing.T) {
	original := Response{
		"array": []interface{}{"a", "b", "c"},
		"map": map[string]interface{}{
			"nested_key": "nested_value",
		},
		"null":   nil,
		"number": float64(3.14),
	}

	var buf bytes.Buffer
	err := original.Encode(&buf)
	require.NoError(t, err)

	decoded, err := Decode(&buf)
	require.NoError(t, err)

	assert.Nil(t, decoded["null"])
}

func TestResponse_LargePayload(t *testing.T) {
	// Create a response with large string values
	largeString := make([]byte, 100000)
	for i := range largeString {
		largeString[i] = byte('a' + (i % 26))
	}

	original := Response{
		"large": string(largeString),
	}

	var buf bytes.Buffer
	err := original.Encode(&buf)
	require.NoError(t, err)

	decoded, err := Decode(&buf)
	require.NoError(t, err)

	val, err := decoded.Get("large")
	require.NoError(t, err)
	assert.Equal(t, string(largeString), val)
}

func TestResponse_Set_NilValue(t *testing.T) {
	resp := Response{}
	resp.Set("nil_key", nil)

	val, err := resp.Get("nil_key")
	require.NoError(t, err)
	assert.Nil(t, val)
}

func TestResponse_ManyKeys(t *testing.T) {
	original := Response{}
	for i := 0; i < 100; i++ {
		key := string(rune('a' + i%26))
		original.Set(key, i)
	}

	var buf bytes.Buffer
	err := original.Encode(&buf)
	require.NoError(t, err)

	decoded, err := Decode(&buf)
	require.NoError(t, err)

	// Note: duplicate keys will overwrite, so we check last values
	for i := 0; i < 26; i++ {
		key := string(rune('a' + i))
		val, err := decoded.Get(key)
		require.NoError(t, err)
		assert.NotNil(t, val)
	}
}

func TestResponse_BinaryData(t *testing.T) {
	binaryData := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0xFD}

	original := Response{
		"binary": binaryData,
	}

	var buf bytes.Buffer
	err := original.Encode(&buf)
	require.NoError(t, err)

	decoded, err := Decode(&buf)
	require.NoError(t, err)

	val, err := decoded.Get("binary")
	require.NoError(t, err)
	assert.Equal(t, binaryData, val)
}

func TestResponse_Type(t *testing.T) {
	resp := Response{}
	assert.IsType(t, Response{}, resp)
	assert.IsType(t, map[string]interface{}{}, map[string]interface{}(resp))
}
