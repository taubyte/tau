package volume

import (
	"io/fs"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// openEntry opens name itself, never what it links to.
//
// O_PATH HOLDS THE ENTRY WITHOUT OPENING IT, so a device or a fifo a workload
// left in its volume is never opened either. A regular file's bytes are opened
// again through that handle once its own stat says what it is, which makes them
// that file's and no other's, whatever the name has become since.
func openEntry(root *os.Root, name string) (entry, error) {
	handle, err := root.OpenFile(name, unix.O_PATH, 0)
	if err != nil {
		return entry{}, err
	}
	defer handle.Close()

	info, err := handle.Stat()
	if err != nil {
		return entry{}, err
	}
	opened := entry{info: info}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target := make([]byte, unix.PathMax)
		n, err := unix.Readlinkat(int(handle.Fd()), "", target)
		if err != nil {
			return entry{}, err
		}
		opened.link = string(target[:n])
	case info.Mode().IsRegular():
		if opened.content, err = os.Open("/proc/self/fd/" + strconv.Itoa(int(handle.Fd()))); err != nil {
			return entry{}, err
		}
	}
	return opened, nil
}
