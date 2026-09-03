// Package livelog is an append-only file that can be read while it is still
// being written: one writer appends, and any number of readers FORK OFF IT AT
// AN OFFSET and follow it to the end.
//
// It is what a build's output is, and the three things it has to be are the
// three lives of a log:
//
//	WHILE THE BUILD RUNS   the writer appends. No consensus, no RPC per line,
//	                       no replicated entry per row — a log is content, and
//	                       nothing about content needs agreement.
//	WHILE SOMEBODY WATCHES a reader forks at any offset and follows. Many at
//	                       once, each at its own. One that catches up BLOCKS
//	                       until there is more or the writer closes; it does
//	                       not return EOF early, which is the bug every naive
//	                       tail has.
//	WHEN THE BUILD ENDS    the file is read back once, by a fork like any
//	                       other, and handed to a byte store. Then it is
//	                       removed: what survives the run is a content id.
//
// THE OFFSET IS THE WHOLE MECHANISM. v1 has no equivalent of it — patrick can
// hand you a log only once the build has ended and the whole thing has reached
// IPFS — so "watch this build" was not a thing that could be built on it. Here
// a viewer joining late forks at 0 and catches up; a viewer whose connection
// dropped forks at the offset it had and misses nothing in between; and a
// streaming proxy is fork-a-reader-and-io.Copy, because Reader is an io.Reader
// and nothing else.
//
// # What "durable enough" means here, exactly
//
// A Write returns when the bytes are READABLE BY A FORK, and that is the whole
// promise. It is one write(2) with no fsync: the bytes are in the page cache,
// every subsequent open of the path sees them, and they survive this process
// dying. They do not survive the MACHINE dying — and they are not asked to,
// because a machine that dies loses the claim on the job too, and the job is
// redelivered to a node that starts a new attempt with a new log. An fsync per
// line would buy durability against the one failure whose recovery already
// discards the log, and would cost a disk round trip per line of every build.
//
// It assumes a LOCAL filesystem, and that is a real assumption rather than a
// figure of speech: read-after-write between two file descriptors is POSIX
// page-cache coherence, and a network filesystem with attribute caching (NFS)
// can serve a reader a shorter file than the writer has written. A log
// directory on one of those is a configuration this package cannot detect and
// does not defend against.
//
// # A byte offset, and what that costs
//
// An offset is a BYTE offset, not a record number. Two consequences, both
// deliberate:
//
//   - a reconnecting client can resume mid-line. Nothing here knows what a line
//     is, so a client that cares resumes at the last newline it saw and drops
//     the partial one — which is a client-side decision it can make and this
//     package cannot make for it.
//   - there is no index. A record offset would need a record->byte map that is
//     itself durable, replicated to a reconnecting reader, and kept in step
//     with a file being appended to concurrently. The file IS the index here:
//     an offset is a seek, and a fork costs one open and one number.
//
// # Bounded memory, and who blocks
//
// A reader holds no buffer of its own: it reads from the file at its own
// offset, so a reader that is slower than the writer costs one file descriptor
// and the bytes it asked for — never a queue of what it has not caught up to.
// The FILE is the buffer, which is what a file is for. So the writer never
// blocks on a reader and a reader can never make the build wait, at any lag.
//
// What that trades away is unbounded DISK, and a build script chooses how much
// it writes — so WithMaxBytes is a cap rather than an option nobody sets: past
// it, the writer appends one truncation notice and then discards, and says so
// with ErrFull.
//
// # What an unlinked file does
//
// Unlink removes the path. On POSIX a descriptor already open keeps reading the
// inode, so every reader that had forked keeps working to the end and the
// writer keeps appending — and a fork taken AFTER the removal fails, because
// there is no path left to open. That is exactly the shape the handoff wants:
// ship the bytes, unlink, and let the readers still attached drain.
package livelog
