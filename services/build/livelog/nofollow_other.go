//go:build !unix

package livelog

import "io/fs"

// nofollow_other.go is what this package can and cannot promise where the three
// answers in nofollow_unix.go have no portable spelling: windows, plan9, js and
// wasip1.
//
// WHAT IS LOST, SAID PLAINLY RATHER THAN LEFT TO BE DISCOVERED:
//
//	noFollow             0. A symbolic link at a log's path is FOLLOWED, and
//	                     the create truncates whatever it points at. On windows
//	                     making one needs SeCreateSymbolicLinkPrivilege (or
//	                     developer mode), so the attacker this defends against
//	                     already has more than this would give them; on plan9
//	                     there are no symbolic links at all; js and wasip1 have
//	                     whatever the host handed the guest.
//	links                1. A hard link planted at a log's path is not noticed.
//	ownedByThisProcess   true. A log directory somebody else owns is accepted.
//	exclusive            true. The permission bits windows reports are a fiction
//	                     the runtime synthesises from one read-only attribute —
//	                     a directory there answers 0777 whatever its ACL says,
//	                     and a chmod to 0700 changes nothing and reports
//	                     success. Asking the question would REFUSE EVERY LOG
//	                     DIRECTORY ON WINDOWS, which is a check that has stopped
//	                     being one; the honest answer is that this platform's
//	                     access control is not in these bits and this package
//	                     does not read the one it is in.
//
// So OwnDir is a MkdirAll with a not-a-directory refusal on these platforms,
// and Create is the standard-library open it always was.
//
// The regular-file check in Create is NOT in this list: fs.FileMode.IsRegular
// is the standard library's everywhere, so a directory, a device or a pipe at a
// log's path is refused on every platform.
const noFollow = 0

func links(fs.FileInfo) uint64 { return 1 }

func ownedByThisProcess(fs.FileInfo) bool { return true }

func exclusive(fs.FileInfo) bool { return true }
