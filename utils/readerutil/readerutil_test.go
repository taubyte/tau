package readerutil

import (
	"fmt"
	"io"
	"strings"
)

func ExampleNewCountingReader() {
	var (
		// r is the io.Reader we'd like to count read from.
		r  = strings.NewReader("Hello world")
		n  int64
		cr = NewCountingReader(r, &n)
	)
	// Read from the wrapped io.Reader, CountingReader will count the bytes.
	io.Copy(io.Discard, cr)
	fmt.Printf("Read %d bytes\n", n)
	// Output: Read 11 bytes
}
