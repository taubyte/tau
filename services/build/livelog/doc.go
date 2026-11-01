// Package livelog is an append-only file read while it is written: one writer
// appends, and any number of readers fork at an offset and follow to the end,
// blocking when caught up rather than returning EOF early. At the end of a build
// the file is read back by a fork, handed to a byte store, and removed.
//
// A Write returns when the bytes are readable by a fork: one write(2), no fsync.
// They survive this process dying, not the machine, whose death also loses the
// claim. It assumes a local filesystem (NFS attribute caching breaks
// read-after-write).
//
// Offsets are bytes, not records: a client may resume mid-line, and there is no
// index. Readers hold no buffer, so the writer never blocks on a reader;
// WithMaxBytes caps disk, after which the writer appends one notice and returns
// ErrFull.
//
// A log is an inode: Create resolves the path once, and the writer and every
// reader use that one descriptor (readers via pread at their own offsets). A name
// swept or replaced by somebody else changes nothing. Close ends the writing;
// Unlink ends the log, with forked readers keeping the inode until they finish.
package livelog
