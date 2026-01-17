package bytes

import (
	"bytes"
	"math"
)

// Pad returns a buffer of the original data with padded trailing 0s of length of given paddingSize
func Pad(data []byte, paddingSize int) []byte {
	paddedData := make([]byte, len(data)+paddingSize)
	copy(paddedData, data)

	return paddedData
}

// PadLCM returns a buffer with length of the Least Common Multiple of the given lcm value
// with the original data with padded trailing 0s
func PadLCM(data []byte, lcm int) []byte {
	var size int
	if len(data) < 1 {
		size = lcm
	}

	size = int(math.Ceil(float64(len(data))/float64(lcm))) * lcm
	paddedData := make([]byte, size)
	copy(paddedData, data)

	return paddedData
}

// StripPadding returns a buffer of the data stripped of all trailing 0s
func StripPadding(data []byte) []byte {
	return bytes.TrimRightFunc(data, func(r rune) bool {
		return byte(r) == 0
	})
}
