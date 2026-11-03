//go:build !linux

package volume

import "os"

// extent has the rest of a file as data: without SEEK_DATA there is nothing to
// ask where its holes are, so they are read as a plain read reads them.
func extent(_ *os.File, at, size int64) (int64, int64, error) {
	return at, size, nil
}
