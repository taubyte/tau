package livelog

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sync"
)

// The sentinels a caller can tell apart; anything else is the filesystem's error
// with the path.
var (
	// ErrClosed is a write to a closed log, or a read from a closed reader.
	ErrClosed = errors.New("livelog: closed")
	// ErrOffset is a fork at an offset this log does not have — negative, or
	// past the end. It is not clamped: a client resuming at an offset the log
	// never reached is a client reading the wrong log, and telling it so is
	// worth more than silently handing it the end.
	ErrOffset = errors.New("livelog: offset is not in this log")
	// ErrFull is a write past the cap. The bytes that fit are written, the
	// notice below goes in once, and everything after is discarded.
	ErrFull = errors.New("livelog: log is full")
	// ErrTruncated is a file that got SHORTER than the bytes this log has
	// already handed out — something outside this process rewrote it. A reader
	// stops rather than reporting whatever is at that offset now.
	ErrTruncated = errors.New("livelog: file was truncated under a reader")
	// ErrConcurrentRead is two goroutines reading ONE reader. Fork one each:
	// an offset is what a reader is, and two consumers sharing one would each
	// see half a log.
	ErrConcurrentRead = errors.New("livelog: a reader is read by one goroutine at a time")
	// ErrNotALog is a path that is a symlink, a second name for an inode someone else
	// holds, or not an ordinary file; OwnDir answers it for directories too.
	ErrNotALog = errors.New("livelog: not a file this log may be written to")
)

// truncationNotice is what a capped log says instead of the bytes it dropped. A
// truncated log with no marker is a log that lies, and it is the reader who
// pays: an error at the end of a build that "printed nothing after this point"
// is the one line that explains it.
var truncationNotice = []byte("\n[log truncated: this build exceeded its log limit]\n")

// Log is one append-only file, its writer, and its readers. Create makes one;
// every method is safe from any goroutine.
type Log struct {
	path string

	// mu guards everything below and every reader's offset; cond is over it, and every
	// event ending a wait (append, close, reader close) broadcasts under the lock.
	mu   sync.Mutex
	cond *sync.Cond

	// file is the log: the writer appends and every reader preads this one descriptor;
	// the name is never re-resolved after Create.
	file *os.File
	// refs counts the descriptor's holders (the log plus each live reader); the last
	// to let go closes it. The log's own hold is dropped by Unlink, not Close.
	refs int
	// gone is Unlink's idempotence and the reason a fork after it is refused
	// deterministically: the log has let go of the inode, so there is nothing
	// left to borrow even while a reader is still draining one.
	gone bool
	// size is the bytes a reader may read. It advances only AFTER write(2) has
	// returned, which is the whole of "a reader never sees a partial write":
	// there is no offset at which a reader can observe half of a Write, because
	// the number that lets it look was not moved until the write was done.
	size   int64
	max    int64
	full   bool
	closed bool
	// err is the first write failure. It is sticky: a log that lost bytes must
	// not go on pretending, and a reader waiting at the end learns about it
	// rather than blocking forever on a writer that will never write again.
	err error
}

// Option configures a Log at creation.
type Option func(*Log) error

// WithMaxBytes caps the file: past it a Write appends the truncation notice once
// and returns ErrFull, and the log keeps working. Zero is unbounded.
func WithMaxBytes(n int64) Option {
	return func(l *Log) error {
		if n < 0 {
			return fmt.Errorf("livelog: max bytes must not be negative")
		}
		l.max = n
		return nil
	}
}

// Create opens path for appending and empties it. Truncating rather than refusing
// reuses a crashed run's scratch file. Because it destroys at a path anyone can
// compute, the open refuses what an attacker could plant:
//
//	a symbolic link   refused by noFollow in open(2) itself, never check-then-open
//	a hard link       refused by the link count on the open descriptor
//	anything else     a directory, device or fifo (a fifo would block the build)
//
// So truncation happens after these checks, on the descriptor, not as O_TRUNC.
// OwnDir is the primary defence; this holds when the directory is wrong. The file
// is 0600.
func Create(path string, opts ...Option) (*Log, error) {
	l := &Log{path: path}
	l.cond = sync.NewCond(&l.mu)

	for _, opt := range opts {
		if err := opt(l); err != nil {
			return nil, err
		}
	}

	// O_RDWR so readers can pread this descriptor: the only time a name becomes a file
	// here.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND|noFollow, 0o600)
	if err != nil {
		return nil, fmt.Errorf("livelog: creating %s: %w", path, err)
	}

	if err := own(f, path); err != nil {
		f.Close()
		return nil, err
	}

	l.file, l.refs = f, 1
	return l, nil
}

// own checks an open descriptor (fstat, no second walk of the name) is a file this
// package may write over, then empties it; path is only for the error.
func own(f *os.File, path string) error {
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("livelog: creating %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("livelog: creating %s: %w: it is %s", path, ErrNotALog, fi.Mode().Type())
	}
	if n := links(fi); n > 1 {
		return fmt.Errorf("livelog: creating %s: %w: %d names hold this file", path, ErrNotALog, n)
	}
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("livelog: emptying %s: %w", path, err)
	}
	return nil
}

// OwnDir makes dir a directory this process owns that nobody else can write in,
// so no one can create a log name first. os.MkdirAll accepts an existing
// directory of any owner and mode, so:
//
//	not a directory, or a symlink   refused (Lstat)
//	ours but loose                  tightened to 0700
//	somebody else's                 refused; chmod cannot make it safe
//
// A world-writable ancestor whose entries a stranger can rename is not pinned;
// Create's own refusals hold there.
func OwnDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("livelog: a log directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("livelog: log directory %s: %w", dir, err)
	}

	err := ownDir(dir)
	if err == nil || !errors.Is(err, errLoose) {
		return err
	}

	// Ours and loose: the one repair rather than refusal.
	return tighten(dir)
}

// errLoose is a directory that is THIS PROCESS'S and writable by others: the
// one answer OwnDir acts on rather than reports. It is not exported, because
// after OwnDir returns it is either fixed or it is somebody else's problem
// spelled ErrNotALog.
var errLoose = errors.New("livelog: log directory is writable by others")

// tighten closes a loose directory of our own to everybody else, the only write
// OwnDir makes.
func tighten(dir string) error {
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("livelog: log directory %s is writable by others and cannot be "+
			"tightened: %w", dir, err)
	}
	// The repair is checked again, since chmod can succeed without effect on a
	// filesystem without unix permissions.
	return ownDir(dir)
}

// ownDir is the question, and it is asked of a NAME because a directory is what
// a name leads to — Lstat, so a link to a directory is seen as a link.
func ownDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("livelog: log directory %s: %w", dir, err)
	}
	return suitable(dir, fi)
}

// suitable is the three answers apart from the filesystem, so a directory owned by
// another account is testable.
func suitable(dir string, fi fs.FileInfo) error {
	if !fi.IsDir() {
		return fmt.Errorf("livelog: log directory %s: %w: it is %s", dir, ErrNotALog, fi.Mode().Type())
	}
	if !ownedByThisProcess(fi) {
		return fmt.Errorf("livelog: log directory %s: %w: it belongs to somebody else, so "+
			"anything in it can be a name they created", dir, ErrNotALog)
	}
	if !exclusive(fi) {
		return fmt.Errorf("%w: %s is %s", errLoose, dir, fi.Mode().Perm())
	}
	return nil
}

// Path is where this log is on disk.
func (l *Log) Path() string { return l.path }

// Size is how many bytes a fork can read right now. It only ever goes up.
func (l *Log) Size() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.size
}

// Write appends and returns when the bytes are readable by a fork: one write(2)
// under the lock, which readers hold only to learn how far they may read.
func (l *Log) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return 0, ErrClosed
	}
	if l.err != nil {
		return 0, l.err
	}

	room := int64(len(p))
	capped := false
	if l.max > 0 && l.size+room > l.max {
		room, capped = l.max-l.size, true
	}

	var n int
	if room > 0 {
		var err error
		n, err = l.file.Write(p[:room])
		// THE BYTES THAT LANDED ARE READABLE EVEN THOUGH THE WRITE FAILED. A
		// short write is still a write, and hiding it would leave the file
		// longer than the log admits — so size follows what the syscall
		// reported, not what was asked for.
		l.size += int64(n)
		if err != nil {
			l.err = fmt.Errorf("livelog: writing %s: %w", l.path, err)
			l.cond.Broadcast()
			return n, l.err
		}
	}

	if capped && !l.full {
		l.full = true
		// The notice is over the cap, on purpose: a limit that ate its own
		// explanation would be a log that just stops.
		if wrote, err := l.file.Write(truncationNotice); err == nil {
			l.size += int64(wrote)
		}
	}

	l.cond.Broadcast()
	if capped {
		return n, ErrFull
	}
	return n, nil
}

// Close ends the writing: blocked readers get io.EOF, and a later fork still reads
// the whole file. It closes no descriptor (Unlink does). Idempotent; never fails.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.closed = true
	l.cond.Broadcast()
	return nil
}

// release drops one hold on the descriptor, closing it on the last. Called with mu
// held.
func (l *Log) release() error {
	l.refs--
	if l.refs > 0 {
		return nil
	}
	return l.file.Close()
}

// Unlink ends the log: the name goes and this log releases the inode; writing and
// forking end. Forked readers drain to the end. Idempotent; it reports what it
// could not do and does the rest.
func (l *Log) Unlink() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	var failed []error
	if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		failed = append(failed, fmt.Errorf("livelog: removing %s: %w", l.path, err))
	}

	l.closed = true
	l.cond.Broadcast()
	if !l.gone {
		l.gone = true
		if err := l.release(); err != nil {
			failed = append(failed, fmt.Errorf("livelog: closing %s: %w", l.path, err))
		}
	}
	return errors.Join(failed...)
}

// Fork returns a reader at offset that borrows the log's descriptor, so it reads
// the whole file after Close, survives Unlink once forked, and reads this log even
// if the name was removed or reused. It fails only after Unlink. A fork is a
// number, so nothing written before the first read is missed.
func (l *Log) Fork(offset int64) (*Reader, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if offset < 0 || offset > l.size {
		return nil, fmt.Errorf("%w: %d is not in [0,%d]", ErrOffset, offset, l.size)
	}
	if l.gone {
		return nil, fmt.Errorf("livelog: forking a reader of %s: %w", l.path, ErrClosed)
	}

	l.refs++
	return &Reader{l: l, at: offset}, nil
}

// Reader follows a Log from one offset, an io.ReadCloser for one consumer: a
// second concurrent Read is refused (ErrConcurrentRead). Close and Offset may be
// called from another goroutine; Close abandons a blocked Read.
type Reader struct {
	l *Log
	// at is guarded by l.mu, so that Close and Offset can be called while a
	// Read is in flight.
	at     int64
	closed bool
	// reading refuses a second concurrent Read, which would otherwise silently
	// duplicate and skip bytes with no data race to detect.
	reading bool
}

// Offset is where this reader has got to. It is what a client stores to
// reconnect: fork again at this number and nothing is read twice or skipped.
func (r *Reader) Offset() int64 {
	r.l.mu.Lock()
	defer r.l.mu.Unlock()
	return r.at
}

// Read blocks at the end of the log until there is more; io.EOF means the writer
// closed and this reader reached the end.
func (r *Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	l := r.l
	l.mu.Lock()
	if r.reading {
		l.mu.Unlock()
		return 0, ErrConcurrentRead
	}
	r.reading = true
	defer func() {
		l.mu.Lock()
		r.reading = false
		l.mu.Unlock()
	}()

	for {
		if r.closed {
			l.mu.Unlock()
			return 0, ErrClosed
		}
		if r.at < l.size {
			break
		}
		// The sticky error is checked before closed, or a log that lost bytes would end
		// with a clean io.EOF.
		if l.err != nil {
			err := l.err
			l.mu.Unlock()
			return 0, err
		}
		if l.closed {
			l.mu.Unlock()
			return 0, io.EOF
		}
		// A wait releases the lock, so an append, a close or this reader's own
		// Close all get in — and each of them broadcasts.
		l.cond.Wait()
	}
	at, available := r.at, l.size-r.at
	l.mu.Unlock()

	want := int64(len(p))
	if available < want {
		want = available
	}

	// Outside the lock: [at, at+want) is already written and never changes. ReadAt
	// carries its offset in the syscall, so one descriptor serves the writer and every
	// reader.
	n, err := r.l.file.ReadAt(p[:want], at)

	l.mu.Lock()
	r.at = at + int64(n)
	l.mu.Unlock()

	if err != nil {
		if errors.Is(err, io.EOF) {
			// The log says these bytes are there and the file says they are
			// not, so somebody outside this process shortened it.
			return n, fmt.Errorf("%w: %s at %d", ErrTruncated, l.path, at+int64(n))
		}
		return n, fmt.Errorf("livelog: reading %s: %w", l.path, err)
	}
	return n, nil
}

// Close releases this reader, including one blocked at the end of the log. It
// is idempotent, and it never affects the writer or another reader.
func (r *Reader) Close() error {
	l := r.l
	l.mu.Lock()
	if r.closed {
		l.mu.Unlock()
		return nil
	}
	r.closed = true
	// Under the lock and before the descriptor goes: a Read blocked in Wait
	// must see closed rather than a file it can no longer read.
	l.cond.Broadcast()
	// The descriptor closes only when this was the last holder.
	err := l.release()
	l.mu.Unlock()

	if err != nil {
		return fmt.Errorf("livelog: closing a reader of %s: %w", l.path, err)
	}
	return nil
}
