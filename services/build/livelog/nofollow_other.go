//go:build !unix

package livelog

import "io/fs"

// Where nofollow_unix.go's answers have no portable spelling (windows, plan9, js,
// wasip1), the permissive ones:
//
//	noFollow             0: a symlink at a log's path is followed
//	links                1: a planted hard link is not noticed
//	ownedByThisProcess   true: another owner's directory is accepted
//	exclusive            true: windows' mode bits are synthesized, so checking
//	                     them would refuse every directory
//
// The regular-file check in Create still applies everywhere.
const noFollow = 0

func links(fs.FileInfo) uint64 { return 1 }

func ownedByThisProcess(fs.FileInfo) bool { return true }

func exclusive(fs.FileInfo) bool { return true }
