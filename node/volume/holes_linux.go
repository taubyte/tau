package volume

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// extent is where the data at or after at starts in the first size bytes of
// file, and where the hole after it does, as the filesystem says through
// SEEK_DATA and SEEK_HOLE.
//
// NO DATA PAST at IS A HOLE TO THE END, unless the file has become shorter than
// size since it was opened, which ends the read where a plain one would have
// ended. A filesystem that cannot say, or says what cannot be, has data
// everywhere, and it is all read.
func extent(file *os.File, at, size int64) (int64, int64, error) {
	data, err := file.Seek(at, unix.SEEK_DATA)
	switch {
	case errors.Is(err, unix.ENXIO):
		info, err := file.Stat()
		if err != nil {
			return 0, 0, err
		}
		if info.Size() < size {
			return 0, 0, io.EOF
		}
		return size, size, nil
	case err != nil || data < at:
		return at, size, nil
	}
	end, err := file.Seek(data, unix.SEEK_HOLE)
	if err != nil || end <= data {
		end = size
	}
	return min(data, size), min(end, size), nil
}
