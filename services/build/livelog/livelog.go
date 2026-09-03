package livelog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// The three failures a caller can tell apart, and nothing else is exported as a
// sentinel: everything else that goes wrong here is the filesystem's own error,
// passed through with the path in it.
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
)

// truncationNotice is what a capped log says instead of the bytes it dropped. A
// truncated log with no marker is a log that lies, and it is the reader who
// pays: an error at the end of a build that "printed nothing after this point"
// is the one line that explains it.
var truncationNotice = []byte("\n[log truncated: this build exceeded its log limit]\n")

// Log is one append-only file, its writer, and the readers following it.
//
// The zero value is not usable; Create makes one. Write and Close are safe to
// call from any goroutine, though a build has one writer by construction; Fork,
// Size, Path and Unlink are safe from any.
type Log struct {
	path string

	// mu guards everything below AND the offsets of every reader. cond is over
	// it: a reader that has caught up waits on it, and the three things that
	// can end that wait — an append, the writer closing, a reader closing
	// itself — all broadcast under the lock, so there is no wakeup to lose.
	mu   sync.Mutex
	cond *sync.Cond

	file *os.File
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

// WithMaxBytes caps the file. Past the cap a Write appends the truncation
// notice once and reports ErrFull, and the log keeps working: a build that
// writes too much is not a build that fails, it is a build whose log says where
// it stopped being kept.
//
// Zero is unbounded, which is what a caller with its own bound wants. Nobody
// running a stranger's build script should choose it.
func WithMaxBytes(n int64) Option {
	return func(l *Log) error {
		if n < 0 {
			return fmt.Errorf("livelog: max bytes must not be negative")
		}
		l.max = n
		return nil
	}
}

// Create opens path for appending, truncating anything already there.
//
// TRUNCATING RATHER THAN REFUSING is the choice for a name that is already
// unique per run — the caller names a file after the job and the attempt, and
// an attempt number never repeats — so a file left behind by a process that
// died is scratch to be reused, not a collision to fail on. Refusing would turn
// somebody else's crash into this build's failure.
//
// The file is 0600: a build log carries whatever the build printed, which
// includes whatever it printed by mistake.
func Create(path string, opts ...Option) (*Log, error) {
	l := &Log{path: path}
	l.cond = sync.NewCond(&l.mu)

	for _, opt := range opts {
		if err := opt(l); err != nil {
			return nil, err
		}
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("livelog: creating %s: %w", path, err)
	}
	l.file = f
	return l, nil
}

// Path is where this log is on disk.
func (l *Log) Path() string { return l.path }

// Size is how many bytes a fork can read right now. It only ever goes up.
func (l *Log) Size() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.size
}

// Write appends, and returns when what it wrote is readable by a fork.
//
// It is one write(2) under the lock. Holding the lock across the syscall is
// what keeps two writers from interleaving their bytes and what keeps size in
// step with the file; the readers do not contend for it, because a reader holds
// it only long enough to learn how far it may read.
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

// Close ends the log. Every reader blocked at the end is released with a clean
// io.EOF, and a fork taken afterwards still reads the whole file.
//
// It is idempotent, because the caller that closes on the way out of a run and
// the caller that closes because the run failed are the same caller twice.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}
	l.closed = true
	err := l.file.Close()
	l.cond.Broadcast()
	if err != nil {
		return fmt.Errorf("livelog: closing %s: %w", l.path, err)
	}
	return nil
}

// Unlink removes the file's NAME, which is what the verb means and why it is
// not called Remove: readers that already forked keep reading the inode to the
// end (POSIX: it outlives the name for as long as a descriptor is open), the
// writer keeps appending to it, and only a Fork taken afterwards fails, because
// a fork is the one operation that needs a path.
//
// Unlinking a log that is still open is therefore allowed and is not a mistake.
// It is idempotent.
func (l *Log) Unlink() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("livelog: removing %s: %w", l.path, err)
	}
	return nil
}

// Fork hands back a reader positioned at offset.
//
// IT TAKES ITS OWN DESCRIPTOR, and that is what makes the two hard cases work:
// a fork taken after the writer closed still reads the whole file, and a reader
// already forked survives the file being unlinked under it. Sharing the
// writer's descriptor would break both.
//
// AND NOTHING WRITTEN BETWEEN THE FORK AND THE FIRST READ IS MISSED, because a
// fork is a NUMBER and not a subscription. The naive tail — snapshot what is
// there, then subscribe to what comes next — loses whatever lands between the
// two, and there is no window here for anything to land in.
func (l *Log) Fork(offset int64) (*Reader, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if offset < 0 || offset > l.size {
		return nil, fmt.Errorf("%w: %d is not in [0,%d]", ErrOffset, offset, l.size)
	}

	// Under the lock, so that an Unlink cannot land between the check and the
	// open and leave the caller with an error that reads like a missing file.
	f, err := os.Open(l.path)
	if err != nil {
		return nil, fmt.Errorf("livelog: forking a reader of %s: %w", l.path, err)
	}
	return &Reader{l: l, file: f, at: offset}, nil
}

// Reader follows a Log from one offset.
//
// It is an io.ReadCloser and nothing more, so a streaming proxy is a Fork and
// an io.Copy. ONE READER IS FOR ONE CONSUMER, and a second goroutine reading it
// is refused rather than left to interleave (ErrConcurrentRead) — forking is
// cheap and an offset is the whole of what a reader is. Close and Offset may be
// called from another goroutine: Close is how a blocked Read is abandoned when
// whoever was watching goes away.
type Reader struct {
	l    *Log
	file *os.File
	// at is guarded by l.mu, so that Close and Offset can be called while a
	// Read is in flight.
	at     int64
	closed bool
	// reading is what makes "one Reader, one consumer" a REFUSAL rather than a
	// comment. Two goroutines reading one Reader both take `at` under the lock,
	// both read from it, and both advance it — so the same bytes come out twice
	// and some never do, with no data race for -race to find, because every
	// field they touch is behind the mutex. A silent duplication in a build log
	// is the kind of thing somebody debugs for an afternoon.
	reading bool
}

// Offset is where this reader has got to. It is what a client stores to
// reconnect: fork again at this number and nothing is read twice or skipped.
func (r *Reader) Offset() int64 {
	r.l.mu.Lock()
	defer r.l.mu.Unlock()
	return r.at
}

// Read blocks at the end of the log until there is more, rather than returning
// EOF — which is the difference between following a log and reading a file.
//
// EOF means the writer closed AND this reader reached the end. Nothing else
// returns it, and in particular "there is nothing right now" never does.
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
		if l.closed {
			l.mu.Unlock()
			return 0, io.EOF
		}
		if l.err != nil {
			err := l.err
			l.mu.Unlock()
			return 0, err
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

	// OUTSIDE THE LOCK, because [at, at+want) can no longer change: this file
	// only grows, and everything below size has been written already. So a slow
	// reader on a big buffer never holds the writer up.
	n, err := r.file.ReadAt(p[:want], at)

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
	l.mu.Unlock()

	if err := r.file.Close(); err != nil {
		return fmt.Errorf("livelog: closing a reader of %s: %w", l.path, err)
	}
	return nil
}
