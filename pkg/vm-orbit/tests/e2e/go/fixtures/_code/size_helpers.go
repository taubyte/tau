package lib

import (
	"fmt"

	"github.com/taubyte/go-sdk/utils/codec"
)

//go:wasm-module testing
//export readWriteSize
func readWriteSize(*byte, uint32, *uint32, *byte, uint32, *uint32, *byte, uint32, *uint32)

//export ping
func ping() {
	if err := codec.Convert(bytesSlice).To(&bytesSliceEncoded); err != nil {
		panic(err)
	}

	bytesSliceEncodedSize = uint32(len(bytesSliceEncoded))

	if err := codec.Convert(stringSlice).To(&stringSliceEncoded); err != nil {
		panic(err)
	}

	stringSliceEncodedSize = uint32(len(stringSliceEncoded))

	readWriteSize(&bytesSliceEncoded[0], bytesSliceEncodedSize, &bytesSliceRcvSize, &stringValBytes[0], stringValBytesSize, &stringRcvSize, &stringSliceEncoded[0], stringSliceEncodedSize, &stringSliceRcvSize)

	if bytesSliceEncodedSize != bytesSliceRcvSize {
		panic(fmt.Sprintf("bytes: %d!=%d", bytesSliceEncodedSize, bytesSliceRcvSize))
	}

	if stringValBytesSize != stringRcvSize {
		panic(fmt.Sprintf("string: %d!=%d", stringValBytesSize, stringRcvSize))
	}

	if stringSliceEncodedSize != stringSliceRcvSize {
		panic(fmt.Sprintf("string slice: %d!=%d", stringSliceEncodedSize, stringSliceRcvSize))
	}
}

var (
	bytesSlice             = [][]byte{{42, 43, 44}, {45, 46, 47}}
	bytesSliceEncoded      []byte
	bytesSliceEncodedSize  uint32
	bytesSliceRcvSize      uint32
	stringVal              = "hello world"
	stringValBytes         = []byte("hello world")
	stringValBytesSize     = uint32(len(stringValBytes))
	stringRcvSize          uint32
	stringSlice            = []string{"hello world", "goodbye world"}
	stringSliceEncoded     []byte
	stringSliceEncodedSize uint32
	stringSliceRcvSize     uint32
)
