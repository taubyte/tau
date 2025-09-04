package readerutil

import (
	"bytes"
	"io"
	"strings"
)

// Size tries to determine the length of r. If r is an io.Seeker, Size may seek
// to guess the length.
func Size(r io.Reader) (size int64, ok bool) {
	switch rt := r.(type) {
	case *bytes.Buffer:
		return int64(rt.Len()), true
	case *bytes.Reader:
		return int64(rt.Len()), true
	case *strings.Reader:
		return int64(rt.Len()), true
	case io.Seeker:
		pos, err := rt.Seek(0, io.SeekCurrent)
		if err != nil {
			return
		}
		end, err := rt.Seek(0, io.SeekEnd)
		if err != nil {
			return
		}
		size = end - pos
		pos1, err := rt.Seek(pos, io.SeekStart)
		if err != nil || pos1 != pos {
			msg := "failed to restore seek position"
			if err != nil {
				msg += ": " + err.Error()
			}
			panic(msg)
		}
		return size, true
	}
	return 0, false
}
