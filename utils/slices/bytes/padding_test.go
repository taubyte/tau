package bytes

import (
	"bytes"
	"fmt"
	"testing"
)

const testString = "hello world"

func TestPad(t *testing.T) {
	padSize := 5

	paddedData := Pad([]byte(testString), padSize)
	expectedSize := len(testString) + padSize
	if len(paddedData) != expectedSize {
		t.Errorf("expected length `%d` got `%d`", expectedSize, len(paddedData))
		return
	}

	expectedPadding := make([]byte, padSize)
	trimmedData := bytes.TrimSuffix(paddedData, expectedPadding)
	if !bytes.Equal([]byte(testString), trimmedData) {
		t.Error("bytes are not the same")
	}
}

func TestPadLCM(t *testing.T) {
	lcm := 5

	paddedData := PadLCM([]byte(testString), lcm)
	expectedSize := 15
	if len(paddedData) != expectedSize {
		t.Errorf("expected length `%d` got `%d`", expectedSize, len(paddedData))
		return
	}

	expectedPadding := make([]byte, (expectedSize - len(testString)))
	trimmedData := bytes.TrimSuffix(paddedData, expectedPadding)
	if !bytes.Equal([]byte(testString), trimmedData) {
		t.Error("bytes are not the same")
	}
}

func TestStripPadding(t *testing.T) {
	padSize := 5

	paddedData := Pad([]byte(testString), padSize)
	trimmedData := StripPadding(paddedData)

	if !bytes.Equal(trimmedData, []byte(testString)) {
		fmt.Println(trimmedData, []byte(testString))
		t.Error("bytes are not the same")
	}
}
