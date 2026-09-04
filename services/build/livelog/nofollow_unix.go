//go:build unix

package livelog

import (
	"io/fs"
	"os"
	"syscall"
)

// nofollow.go is the half of "a log is never opened through something that is
// not a log" that the standard library has no portable spelling for, and this
// is the file that HAS one. Its counterpart is nofollow_other.go, where every
// answer below is the permissive one, and the difference is written down there.

// noFollow refuses to open a symbolic link, in the open(2) call itself rather
// than in a check beside it. A check-then-open is a race: the window between
// the Lstat and the open is exactly what an attacker who can write in the
// directory is trying to win, and there is no width of that window that is
// safe.
const noFollow = syscall.O_NOFOLLOW

// links is how many names this inode has. It is asked of an OPEN DESCRIPTOR
// (fstat), so it is the count for the file this process is holding rather than
// for whatever the path resolves to a moment later.
//
// IT IS THE OTHER HALF OF noFollow AND NOT A DUPLICATE OF IT. O_NOFOLLOW
// refuses a SYMBOLIC link; a HARD link is not a link at all from open(2)'s side,
// it is the same inode under a second name — so a hard link planted at a log's
// path is opened, truncated and written with none of this process's business in
// it, and no flag anywhere refuses that. A log this service made has exactly
// one name, because it made it.
func links(fi fs.FileInfo) uint64 {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 1
	}
	return uint64(st.Nlink)
}

// ownedByThisProcess is whether this euid owns the thing fi describes.
//
// IT IS THE ONLY QUESTION THAT SURVIVES RUNNING AS ROOT. A directory can be
// tightened to 0700 by anybody with the privilege to chmod it, so "I could
// chmod it, therefore it is mine" is true for root over every directory on the
// box — and a directory root tightened for somebody else is still a directory
// that somebody else can create names in.
func ownedByThisProcess(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return true
	}
	return int(st.Uid) == os.Geteuid()
}

// exclusive is whether the owner is the only one who may create names in this
// directory.
//
// IT IS THE WRITE BITS AND NOT ALL SIX. A directory's write bit is the right to
// add and remove entries in it, so group or other WRITE is exactly "somebody
// else can plant a name" — and group or other READ is somebody else being able
// to list job ids that are derivable from a repository and a commit anyway,
// over files that are 0600 whatever the directory says. Masking 0o077 instead
// would tighten a 0750 directory an operator set that way on purpose, for a
// property this package does not need.
func exclusive(fi fs.FileInfo) bool { return fi.Mode().Perm()&0o022 == 0 }
