//go:build !linux

package volume

import (
	"io/fs"
	"os"
)

// openEntry reads name's own stat, never what it links to, and opens it when
// it is a regular file.
//
// WITHOUT O_PATH there is no handle on an entry short of opening it, so a
// regular file is opened by name once its type is known, and described by the
// stat of what was opened. A name that changed in between is read where it now
// leads, which the root keeps inside the volume.
func openEntry(root *os.Root, name string) (entry, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return entry{}, err
	}
	opened := entry{info: info}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		if opened.link, err = root.Readlink(name); err != nil {
			return entry{}, err
		}
	case info.Mode().IsRegular():
		if opened.content, err = root.Open(name); err != nil {
			return entry{}, err
		}
		if opened.info, err = opened.content.Stat(); err != nil {
			opened.content.Close()
			return entry{}, err
		}
	}
	return opened, nil
}
