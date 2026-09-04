package livelog

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	// ErrNotALog is a path that resolved to something this package will not
	// write a build's output over: a symbolic link, a second name for an inode
	// somebody else also holds, or anything that is not an ordinary file. It is
	// the one sentinel here that is about the DIRECTORY as much as the file —
	// OwnDir answers with it too — because both are the same question asked at
	// two depths: is what this process is about to write into a thing it made.
	ErrNotALog = errors.New("livelog: not a file this log may be written to")
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

	// file is THE LOG. Everything reads and writes this one descriptor: the
	// writer appends to it and every reader preads it at its own offset, so a
	// log is the INODE THIS DESCRIPTOR HOLDS and never the name it was opened
	// under. Nothing here re-resolves that name after Create.
	file *os.File
	// refs is how many holders that descriptor has — the log itself, plus one
	// per live reader — and the descriptor is closed by whichever of them lets
	// go last. It is what keeps POSIX's promise about an unlinked inode inside
	// this process: a reader forked before the log ended keeps reading after
	// it, and the file goes when nobody is left.
	//
	// THE LOG'S OWN HOLD IS DROPPED BY Unlink AND NOT BY Close, because the two
	// verbs own different things: Close ends the WRITING, Unlink ends the LOG.
	// So "the log is over, it is not gone" is a fact about the descriptor
	// rather than about a name that still happens to be on a disk.
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
// AND TRUNCATING IS EXACTLY WHY THE OPEN IS THE CAREFUL ONE. This call
// destroys whatever is at path, with this process's privileges, at a path its
// caller derived rather than chose: a job id is a hash of three public strings
// and is documented as guessable, so a run's log path is a value anybody who
// can name the repository can compute. Somebody who can create a name in the
// log directory therefore knows which name to create, and the three things
// they can put there are the three refusals below:
//
//	A SYMBOLIC LINK   opened, the create follows it and truncates the file at
//	                  the far end — any file this process can write, anywhere on
//	                  the box. noFollow refuses it IN open(2), not in a check
//	                  beside it, because a check-then-open is the race the
//	                  attacker is playing for.
//	A HARD LINK       a second name for an inode they do not own but can read.
//	                  No open flag refuses that; the count does, asked of the
//	                  descriptor after it is open and before anything is
//	                  written.
//	ANYTHING ELSE     a directory, a device, a fifo. A fifo is the quiet one: a
//	                  build writing into it blocks on a reader that never comes,
//	                  and the run is gone for as long as its claim lasts.
//
// SO THE TRUNCATION IS NOT IN THE OPEN ANY MORE. O_TRUNC destroys before
// anything can be asked, which would make the checks a report rather than a
// defence; the file is opened, interrogated through its own descriptor — fstat,
// so there is no name to re-resolve and nothing to race — and truncated after.
//
// NONE OF THIS REPLACES OWNING THE DIRECTORY, which is OwnDir below and is the
// half that stops the name from being creatable at all. This is what holds when
// that half is wrong: an operator who pointed the log directory at something
// shared, a directory that was loose before this process tightened it, a
// process running as somebody who is not the only writer there.
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

	// O_RDWR, NOT O_WRONLY, and that is the whole of "a log is an inode". This
	// open is the ONLY time a name is turned into a file here; every read
	// afterwards is a pread on what it returned. Opened write-only, the readers
	// had to open the path again — and a path is not a file. It can be gone by
	// then (the log directory is somewhere the OS sweeps), or, worse, be
	// somebody else's file with the same name.
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

// own is Create's second half, and it is the counterpart of OwnDir one level
// down: it asks an OPEN DESCRIPTOR whether this is a file this package may
// write a build's output over, and EMPTIES it if it is. path is carried only so
// that a refusal can name it.
//
// THE TWO ARE ONE FUNCTION BECAUSE THE ORDER IS THE POINT. Truncating is what
// destroys, so it happens after the questions and never as an open flag; and
// every question is asked of the DESCRIPTOR — fstat, not a second walk of the
// name — so there is no window between what was checked and what is emptied.
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

// OwnDir makes dir a directory THIS PROCESS OWNS AND NOBODY ELSE CAN WRITE IN,
// and is the half of a log's safety that is about the name rather than the
// file.
//
// A LOG DIRECTORY IS THE WHOLE PRECONDITION OF THE ATTACK Create refuses. The
// paths in it are derived from public values, so they are known in advance;
// what an attacker still needs is the ability to CREATE one of those names
// first. In a directory this process owns at 0700, nobody else can, and every
// path-shaped operation inside it — the create, the removal, the readers' own
// name-free reads — is safe for the ordinary reason rather than for a clever
// one.
//
// os.MkdirAll IS NOT THAT, and that is the defect this exists for. It succeeds
// on a directory that is already there whoever made it and whatever its mode
// is: MkdirAll(d, 0o700) over an existing 0777 d returns nil and leaves it
// 0777. So "the service creates its log directory 0700" was true of a fresh box
// and false of every box where anything had created that name first — including
// an attacker who created it precisely so that it would be.
//
// THE THREE ANSWERS, and only the middle one is a repair:
//
//	NOT A DIRECTORY, or a symbolic link
//	                     refused. Lstat, so a link to a directory is seen as a
//	                     link: followed, this would tighten and then write into
//	                     a directory chosen by whoever made the link.
//	OURS BUT LOOSE       tightened to 0700 here. It is ours, so it is ours to
//	                     fix, and leaving it for somebody to notice is leaving
//	                     the hole open with a note on it.
//	SOMEBODY ELSE'S      refused, and there is nothing this process can do to
//	                     make it safe: chmod would work as root and would leave
//	                     the OWNER — who is not us — still able to create names
//	                     in it.
//
// WHAT IT DOES NOT DO is take the directory away from a name-swap under a
// world-writable ANCESTOR: this resolves dir by name and so does everything
// after it, so an ancestor whose entries a stranger can rename is a directory
// this cannot pin. What holds there is Create's own refusal, one level down.
// The ordinary case — a scratch directory under a sticky temp dir — has no such
// ancestor.
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

	// OURS AND LOOSE, which is the one answer that is a repair rather than a
	// refusal. Leaving it for somebody to notice is leaving the hole open with
	// a note on it.
	return tighten(dir)
}

// errLoose is a directory that is THIS PROCESS'S and writable by others: the
// one answer OwnDir acts on rather than reports. It is not exported, because
// after OwnDir returns it is either fixed or it is somebody else's problem
// spelled ErrNotALog.
var errLoose = errors.New("livelog: log directory is writable by others")

// tighten is the one REPAIR this package makes rather than reports: a log
// directory that is this process's own and writable by others is closed to
// everybody else. It is a step of its own because it is the only WRITE OwnDir
// makes, and a repair that could not be made has to read differently from a
// check that refused.
func tighten(dir string) error {
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("livelog: log directory %s is writable by others and cannot be "+
			"tightened: %w", dir, err)
	}
	// AND THE REPAIR IS ASKED AGAIN RATHER THAN ASSUMED. A chmod that returned
	// nil on a filesystem that does not carry unix permissions has changed
	// nothing, and the whole point of this call is that what it leaves behind
	// is a fact rather than an intention. A second errLoose is not a repair to
	// try twice — it is the answer.
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

// suitable is the three answers, taken apart from the filesystem so that the
// one this process cannot arrange on a real disk — a directory belonging to
// somebody else, which needs a second account — is still something a test can
// ask.
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

// Close ENDS THE WRITING. Every reader blocked at the end is released with a
// clean io.EOF, and a fork taken afterwards still reads the whole file: the log
// is over, it is not gone. Unlink is what makes it gone.
//
// IT CLOSES NO DESCRIPTOR, and that is the split. A log whose descriptor went
// when the writer finished would have nothing left for the one read that
// matters — the handoff at the end of a build reads the WHOLE log, after the
// writer has closed, and a viewer that arrived a moment late reads it too. So
// the writing ends here and the inode is let go by Unlink, which is the verb
// that undoes Create.
//
// It cannot fail, and it returns an error anyway so that a caller closing an
// io.WriteCloser does not have to know that. It is idempotent, because the
// caller that closes on the way out of a run and the caller that closes because
// the run failed are the same caller twice.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.closed = true
	l.cond.Broadcast()
	return nil
}

// release drops one hold on the descriptor and closes it when the last one
// goes. Called with mu held.
//
// THE COUNT IS THE POSIX RULE, MOVED INSIDE THIS PROCESS. An unlinked inode
// lives while a descriptor is open; here one descriptor is open while a holder
// is left, and the holders are the writer and the readers that forked off it.
func (l *Log) release() error {
	l.refs--
	if l.refs > 0 {
		return nil
	}
	return l.file.Close()
}

// Unlink ENDS THE LOG. It is what undoes Create, and it does the two halves of
// that in one call: the NAME goes, and this log lets go of the inode it has
// held since Create. Writing ends with it, and so does forking.
//
// READERS ALREADY FORKED DRAIN IT TO THE END. They hold the inode themselves,
// the last one out closes it, and that is POSIX's own rule about an unlinked
// file kept inside this process rather than borrowed from the kernel — so the
// handoff this exists for works: ship the bytes, unlink, let whoever is
// watching finish.
//
// A NAME REMOVED BY SOMEBODY ELSE IS NOT THIS CALL, and the difference is the
// whole of why a log is an inode. The log directory is somewhere the OS sweeps;
// a sweeper takes the NAME and can take nothing else, because after Create
// nothing here resolves that name again. This call is a decision by the owner
// of the log that the log is over; a sweep is not.
//
// It is idempotent. It reports what it could not do — a removal that failed for
// a reason other than the file already being gone, and a descriptor that could
// not be closed — and it does the rest anyway.
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

// Fork hands back a reader positioned at offset.
//
// IT BORROWS THE LOG'S DESCRIPTOR RATHER THAN OPENING THE PATH, and that is
// what makes the three hard cases work: a fork taken after the writer closed
// still reads the whole file, a reader already forked survives the log being
// unlinked under it, and a fork taken after somebody else removed or REUSED the
// name reads this log rather than nothing or a stranger's file. There is one
// open file description per log and every reader preads it at its own offset,
// so no reader can be pointed at a different inode than the writer is on — and
// none of them costs a descriptor.
//
// IT FAILS ONLY ON A LOG THAT HAS BEEN UNLINKED, because that is the one call
// after which this log holds no inode to lend. Not on a closed one: a build
// that has finished is exactly the log the handoff reads.
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
	if l.gone {
		return nil, fmt.Errorf("livelog: forking a reader of %s: %w", l.path, ErrClosed)
	}

	l.refs++
	return &Reader{l: l, at: offset}, nil
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
	l *Log
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
		// THE ERROR IS ASKED BEFORE THE CLOSE, and that order is the whole of
		// whether the sticky error is reachable at all. A log that lost bytes
		// is closed on its way out like every other — the run ends, the writer
		// closes, and only then is the whole thing read back — so a reader that
		// asked "is it closed" first got a clean io.EOF at the end of a log
		// that was missing the lines saying why it stopped, and the one thing
		// downstream of it, the handoff to a byte store, could not tell that
		// log from a complete one. Everything readable has already been handed
		// out by the break above; what is left to say is why there is no more.
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

	// OUTSIDE THE LOCK, because [at, at+want) can no longer change: this file
	// only grows, and everything below size has been written already. So a slow
	// reader on a big buffer never holds the writer up.
	//
	// IT IS THE LOG'S OWN DESCRIPTOR, PREAD AT THIS READER'S OFFSET. ReadAt
	// carries the offset in the syscall, so it neither reads nor moves the
	// description's own file offset — which is why one descriptor can serve the
	// writer's appends and every reader at once, and why the stdlib takes no
	// read lock for it. The descriptor cannot go while this reader holds a ref;
	// a Close racing this read is os.File's own business, and it answers
	// ErrClosed rather than reading a recycled fd.
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
	// AND THE DESCRIPTOR GOES ONLY IF THIS WAS THE LAST HOLDER. A reader
	// closing while the build still writes costs nothing but the count; the
	// reader that lets go after the writer has finished is the one that closes
	// the file, and it is the one that reports a failure to.
	err := l.release()
	l.mu.Unlock()

	if err != nil {
		return fmt.Errorf("livelog: closing a reader of %s: %w", l.path, err)
	}
	return nil
}
