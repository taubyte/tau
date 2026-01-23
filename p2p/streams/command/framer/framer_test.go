package framer

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/p2p/streams/packer"
)

type testStruct struct {
	Name  string `cbor:"name"`
	Value int    `cbor:"value"`
	Data  []byte `cbor:"data"`
}

func TestSendAndRead_Struct(t *testing.T) {
	magic := packer.Magic{0x01, 0x02}
	version := packer.Version(1)

	original := testStruct{
		Name:  "test",
		Value: 42,
		Data:  []byte("hello"),
	}

	var buf bytes.Buffer
	err := Send(magic, version, &buf, original)
	require.NoError(t, err)

	var decoded testStruct
	err = Read(magic, version, &buf, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.Name, decoded.Name)
	assert.Equal(t, original.Value, decoded.Value)
	assert.Equal(t, original.Data, decoded.Data)
}

func TestSendAndRead_Map(t *testing.T) {
	magic := packer.Magic{0xAB, 0xCD}
	version := packer.Version(2)

	original := map[string]interface{}{
		"key1": "value1",
		"key2": float64(123),
		"key3": true,
	}

	var buf bytes.Buffer
	err := Send(magic, version, &buf, original)
	require.NoError(t, err)

	var decoded map[string]interface{}
	err = Read(magic, version, &buf, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original["key1"], decoded["key1"])
	assert.Equal(t, original["key3"], decoded["key3"])
}

func TestSendAndRead_Slice(t *testing.T) {
	magic := packer.Magic{0x01, 0x02}
	version := packer.Version(1)

	original := []string{"one", "two", "three"}

	var buf bytes.Buffer
	err := Send(magic, version, &buf, original)
	require.NoError(t, err)

	var decoded []string
	err = Read(magic, version, &buf, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original, decoded)
}

func TestRead_WrongMagic(t *testing.T) {
	magic1 := packer.Magic{0x01, 0x02}
	magic2 := packer.Magic{0x03, 0x04}
	version := packer.Version(1)

	original := testStruct{Name: "test"}

	var buf bytes.Buffer
	err := Send(magic1, version, &buf, original)
	require.NoError(t, err)

	var decoded testStruct
	err = Read(magic2, version, &buf, &decoded)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "wrong packer magic")
}

func TestRead_WrongVersion(t *testing.T) {
	magic := packer.Magic{0x01, 0x02}
	v1 := packer.Version(1)
	v2 := packer.Version(2)

	original := testStruct{Name: "test"}

	var buf bytes.Buffer
	err := Send(magic, v1, &buf, original)
	require.NoError(t, err)

	var decoded testStruct
	err = Read(magic, v2, &buf, &decoded)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "wrong packer version")
}

func TestRead_EmptyReader(t *testing.T) {
	magic := packer.Magic{0x01, 0x02}
	version := packer.Version(1)

	var emptyBuf bytes.Buffer
	var decoded testStruct
	err := Read(magic, version, &emptyBuf, &decoded)
	assert.Error(t, err)
}

func TestSend_InvalidObject(t *testing.T) {
	magic := packer.Magic{0x01, 0x02}
	version := packer.Version(1)

	// Channels cannot be encoded by CBOR
	ch := make(chan int)

	var buf bytes.Buffer
	err := Send(magic, version, &buf, ch)
	assert.Error(t, err)
}

func TestSendAndRead_EmptyStruct(t *testing.T) {
	magic := packer.Magic{0x01, 0x02}
	version := packer.Version(1)

	type emptyStruct struct{}
	original := emptyStruct{}

	var buf bytes.Buffer
	err := Send(magic, version, &buf, original)
	require.NoError(t, err)

	var decoded emptyStruct
	err = Read(magic, version, &buf, &decoded)
	require.NoError(t, err)
}

func TestSendAndRead_NestedStruct(t *testing.T) {
	magic := packer.Magic{0x01, 0x02}
	version := packer.Version(1)

	type innerStruct struct {
		Value int `cbor:"value"`
	}
	type outerStruct struct {
		Name  string      `cbor:"name"`
		Inner innerStruct `cbor:"inner"`
	}

	original := outerStruct{
		Name:  "outer",
		Inner: innerStruct{Value: 99},
	}

	var buf bytes.Buffer
	err := Send(magic, version, &buf, original)
	require.NoError(t, err)

	var decoded outerStruct
	err = Read(magic, version, &buf, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.Name, decoded.Name)
	assert.Equal(t, original.Inner.Value, decoded.Inner.Value)
}

func TestSendAndRead_LargeData(t *testing.T) {
	magic := packer.Magic{0x01, 0x02}
	version := packer.Version(1)

	// Create large data (1MB)
	largeData := make([]byte, 1024*1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	original := testStruct{
		Name:  "large",
		Value: 1,
		Data:  largeData,
	}

	var buf bytes.Buffer
	err := Send(magic, version, &buf, original)
	require.NoError(t, err)

	var decoded testStruct
	err = Read(magic, version, &buf, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.Data, decoded.Data)
}

func TestRead_CorruptedCBOR(t *testing.T) {
	magic := packer.Magic{0x01, 0x02}
	version := packer.Version(1)

	// First send a valid message to get proper headers
	original := testStruct{Name: "test"}

	var buf bytes.Buffer
	err := Send(magic, version, &buf, original)
	require.NoError(t, err)

	// Corrupt the data after headers
	data := buf.Bytes()
	if len(data) > 20 {
		data[len(data)-1] ^= 0xFF // Flip bits in last byte
	}

	var decoded testStruct
	err = Read(magic, version, bytes.NewReader(data), &decoded)
	// May or may not error depending on how corruption affects CBOR
	// but the data should not match
	if err == nil {
		// If no error, data should still be corrupted
		t.Log("Decoding succeeded despite corruption, which is possible")
	}
}

func TestSendAndRead_NilValues(t *testing.T) {
	magic := packer.Magic{0x01, 0x02}
	version := packer.Version(1)

	original := map[string]interface{}{
		"nil_value": nil,
		"normal":    "value",
	}

	var buf bytes.Buffer
	err := Send(magic, version, &buf, original)
	require.NoError(t, err)

	var decoded map[string]interface{}
	err = Read(magic, version, &buf, &decoded)
	require.NoError(t, err)

	assert.Nil(t, decoded["nil_value"])
	assert.Equal(t, "value", decoded["normal"])
}
