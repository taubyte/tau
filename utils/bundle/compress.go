package bundle

import (
	"compress/lzw"
	"fmt"
	"io"
	"os"
)

// Compress will compress the given file inPath to the given outPath
func Compress(inPath, outPath string, bufferSize int) (*os.File, error) {
	in, err := os.Open(inPath)
	if err != nil {
		return nil, fmt.Errorf("open file `%s` failed with: %s", inPath, err)
	}
	defer in.Close()

	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0640)
	if err != nil {
		return nil, fmt.Errorf("open file `%s` failed with: %s", outPath, err)
	}

	lzwWriter := lzw.NewWriter(out, lzw.LSB, 8)
	defer lzwWriter.Close()

	buf := make([]byte, bufferSize)

	_, err = io.CopyBuffer(lzwWriter, in, buf)
	if err != nil {
		return nil, fmt.Errorf("copy buffer failed with %s", err)
	}

	return out, nil
}
