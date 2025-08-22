package bytes_test

import (
	"bytes"
	"fmt"

	byteUtil "github.com/taubyte/tau/utils/slices/bytes"
)

func ExamplePad() {
	padSize := 5
	data := []byte("hello world")

	paddedData := byteUtil.Pad(data, padSize)

	data = append(data, make([]byte, padSize)...)
	if !bytes.Equal(data, paddedData) {
		return
	}

	fmt.Println(len(paddedData))
	// Output: 16
}

func ExamplePadLCM() {
	lcm := 5
	data := []byte("hello world")

	paddedData := byteUtil.PadLCM(data, lcm)

	data = append(data, make([]byte, 4)...)
	if !bytes.Equal(data, paddedData) {
		return
	}

	fmt.Println(len(paddedData))
	// Output: 15
}
