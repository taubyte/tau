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
// offset, so a reader that is slower than the writer costs an offset and the
// bytes it asked for — not even a descriptor, and never a queue of what it has
// not caught up to.
// The FILE is the buffer, which is what a file is for. So the writer never
// blocks on a reader and a reader can never make the build wait, at any lag.
//
// What that trades away is unbounded DISK, and a build script chooses how much
// it writes — so WithMaxBytes is a cap rather than an option nobody sets: past
// it, the writer appends one truncation notice and then discards, and says so
// with ErrFull.
//
// # A LOG IS AN INODE, AND THE NAME IS ONLY HOW IT WAS FOUND
//
// Create resolves the path ONCE, and nothing here ever resolves it again: the
// writer appends to that descriptor and every reader PREADS THAT SAME
// DESCRIPTOR at its own offset. A fork borrows it; it does not open anything.
//
// It was the other way around for one round, and the round cost a whole class
// of build its log. A fork opened the path again, and the directory these files
// live in is somewhere the OS sweeps on its own schedule — so a name removed
// while a build ran left the whole log unreadable, with every byte of it still
// on the inode the writer had open. The handoff at the end of the build then
// found nothing to read and said nothing about it. The name is a convenience
// for an operator looking at a disk. It is not what a log is, and the sweep is
// now something this package cannot notice.
//
// WHAT ONE SHARED DESCRIPTOR COSTS, all three of them:
//
//   - PORTABILITY: none that was not already spent. Reading is os.File.ReadAt,
//     which is pread(2) on unix and an offset read on Windows — no dup(2), no
//     /proc/self/fd, nothing outside the standard library. The one place it is
//     weaker is Windows, where the runtime emulates pread by seeking under the
//     file's own lock, so many readers serialize there; this package already
//     says it assumes a local POSIX filesystem.
//   - CONCURRENCY: a pread carries its offset in the syscall, so it neither
//     reads nor moves the description's file offset and cannot be torn by the
//     appends going on beside it. The runtime does not take the file's read
//     lock for it either, so a slow reader on a big buffer does not hold the
//     writer or another reader up. What a reader must not do is Read or Seek
//     the shared descriptor, and it has no way to: a Reader holds an offset.
//   - DESCRIPTORS: one per log rather than one per reader, which is the
//     direction that helps — a build being watched by fifty viewers costs the
//     same one descriptor as a build being watched by nobody.
//
// # What ends a log, and what merely removes its name
//
// Close ends the WRITING: readers drain what is there and then see io.EOF, and
// a fork taken afterwards still reads the whole file, because the handoff at
// the end of a build is exactly that fork.
//
// Unlink ends the LOG. It is what undoes Create — the name goes and this log
// lets go of the inode — and a reader that had already forked keeps the inode
// alive until it is done, the last holder closing the file. That is POSIX's own
// rule about an unlinked file, kept inside this process by a count rather than
// borrowed from the kernel, and it is the shape the handoff wants: ship the
// bytes, unlink, and let the readers still attached drain.
//
// A NAME REMOVED BY SOMEBODY ELSE IS NEITHER. It is not a decision by the owner
// of this log that the log is over, so it changes nothing at all — and neither
// does a name TAKEN OVER, which is the sharper half: rotated aside and
// re-created, or claimed by a second process, the path now leads to another
// inode with a stranger's bytes in it and nothing about that reads as an error.
// A reader here cannot be pointed at it, because no reader here resolves a
// name.
package livelog
