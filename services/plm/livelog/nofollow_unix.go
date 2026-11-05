//go:build unix

package livelog

import (
	"io/fs"
	"os"
	"syscall"
)

// noFollow refuses to open a symbolic link, in the open(2) call itself rather
// than in a check beside it. A check-then-open is a race: the window between
// the Lstat and the open is exactly what an attacker who can write in the
// directory is trying to win, and there is no width of that window that is
// safe.
const noFollow = syscall.O_NOFOLLOW

// links is the inode's name count, asked of the open descriptor. A hard link is not
// a link to open(2), so only the count refuses one; a log this service made has one
// name.
func links(fi fs.FileInfo) uint64 {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 1
	}
	return uint64(st.Nlink)
}

// ownedByThisProcess is whether this euid owns fi; the one question that holds when
// running as root, who can chmod anything.
func ownedByThisProcess(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return true
	}
	return int(st.Uid) == os.Geteuid()
}

// exclusive is whether only the owner may create names here: the group and other
// write bits, not read, so an operator's 0750 is left alone.
func exclusive(fi fs.FileInfo) bool { return fi.Mode().Perm()&0o022 == 0 }
