package livelog

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// settle is how long a test waits before concluding that something DID NOT
// happen — a read that must still be blocked, a writer that must not have been
// held up. It is the one unavoidable sleep in a file about blocking: proving a
// negative about concurrency is proving that nothing happened in some window,
// and the window has to be a number.
const settle = 50 * time.Millisecond

func newLog(t *testing.T, opts ...Option) *Log {
	t.Helper()
	l, err := Create(filepath.Join(t.TempDir(), "build.log"), opts...)
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })
	return l
}

// write appends and fails the test if it could not, WITHOUT stopping the
// goroutine it was called on: several tests write from a goroutine of their
// own, and require's FailNow off the test's own goroutine is documented as not
// working — it would turn a failure into a hang.
func write(t *testing.T, l *Log, s string) {
	t.Helper()
	n, err := l.Write([]byte(s))
	if err != nil || n != len(s) {
		t.Errorf("writing %q: wrote %d bytes, %v", s, n, err)
	}
}

// closing is Close with the same property.
func closing(t *testing.T, l *Log) {
	t.Helper()
	if err := l.Close(); err != nil {
		t.Errorf("closing the log: %v", err)
	}
}

// readN reads exactly n bytes, however many Reads that takes.
func readN(t *testing.T, r io.Reader, n int) string {
	t.Helper()
	buf := make([]byte, n)
	got, err := io.ReadFull(r, buf)
	require.NoError(t, err)
	return string(buf[:got])
}

// reading runs a Read on its own goroutine and reports what it got, so a test
// can assert that it is STILL BLOCKED.
type reading struct {
	done chan struct{}
	n    int
	err  error
	buf  []byte
}

func read(r io.Reader, size int) *reading {
	out := &reading{done: make(chan struct{}), buf: make([]byte, size)}
	go func() {
		defer close(out.done)
		out.n, out.err = r.Read(out.buf)
	}()
	return out
}

func (r *reading) blocked(t *testing.T) {
	t.Helper()
	select {
	case <-r.done:
		t.Fatalf("a read at the end of an open log returned %d bytes, %v — it must block", r.n, r.err)
	case <-time.After(settle):
	}
}

func (r *reading) wait(t *testing.T) (string, error) {
	t.Helper()
	select {
	case <-r.done:
		return string(r.buf[:r.n]), r.err
	case <-time.After(5 * time.Second):
		t.Fatal("a read that should have returned is still blocked")
		return "", nil
	}
}

// A FORK IS A NUMBER, so everything written before it is there to be read, and
// a reader that reads it all gets it in one piece.
func TestAForkReadsEverythingWrittenBeforeIt(t *testing.T) {
	l := newLog(t)
	write(t, l, "compiling\n")
	write(t, l, "linking\n")

	r, err := l.Fork(0)
	require.NoError(t, err)
	defer r.Close()

	require.Equal(t, int64(18), l.Size())
	require.Equal(t, "compiling\nlinking\n", readN(t, r, 18))
	require.Equal(t, int64(18), r.Offset())
}

// AN OFFSET IS NOT CLAMPED. A client resuming past the end is reading the wrong
// log — a different attempt's, most likely — and telling it so is worth more
// than handing it an empty stream that looks like a build which printed
// nothing.
func TestAForkOutsideTheLogIsRefused(t *testing.T) {
	l := newLog(t)
	write(t, l, "12345")

	for _, offset := range []int64{-1, 6, 1 << 40} {
		r, err := l.Fork(offset)
		require.ErrorIs(t, err, ErrOffset, "offset %d", offset)
		require.Nil(t, r)
	}

	// The end itself is inside the log: it is where a reader that has caught up
	// sits, and where a reconnecting one resumes.
	r, err := l.Fork(5)
	require.NoError(t, err)
	require.NoError(t, r.Close())
}

// A READER THAT CATCHES UP BLOCKS. It does not return EOF, and that is the one
// thing a naive tail gets wrong: EOF at the end of an open file says "this log
// is over" when it means "there is nothing yet".
func TestAReaderAtTheEndBlocksUntilTheNextWrite(t *testing.T) {
	l := newLog(t)
	write(t, l, "step one\n")

	r, err := l.Fork(0)
	require.NoError(t, err)
	defer r.Close()
	require.Equal(t, "step one\n", readN(t, r, 9))

	pending := read(r, 64)
	pending.blocked(t)

	write(t, l, "step two\n")
	got, err := pending.wait(t)
	require.NoError(t, err)
	require.Equal(t, "step two\n", got)
}

// MANY READERS, EACH AT ITS OWN OFFSET, while the writer is still appending.
func TestTwoReadersHoldTheirOwnOffsets(t *testing.T) {
	l := newLog(t)
	write(t, l, "aaaa")

	early, err := l.Fork(0)
	require.NoError(t, err)
	defer early.Close()

	late, err := l.Fork(4)
	require.NoError(t, err)
	defer late.Close()

	write(t, l, "bbbb")

	require.Equal(t, "aaaabbbb", readN(t, early, 8))
	require.Equal(t, "bbbb", readN(t, late, 4))
	require.Equal(t, int64(8), early.Offset())
	require.Equal(t, int64(8), late.Offset())
}

// CLOSING THE WRITER RELEASES EVERY BLOCKED READER, with a clean end rather
// than an error — the build is over, and that is not a failure to read.
func TestClosingTheWriterReleasesEveryBlockedReader(t *testing.T) {
	l := newLog(t)
	write(t, l, "done\n")

	const readers = 8
	pending := make([]*reading, readers)
	for i := range pending {
		r, err := l.Fork(5)
		require.NoError(t, err)
		defer r.Close()
		pending[i] = read(r, 16)
	}
	for _, p := range pending {
		p.blocked(t)
	}

	require.NoError(t, l.Close())

	for i, p := range pending {
		got, err := p.wait(t)
		require.ErrorIs(t, err, io.EOF, "reader %d", i)
		require.Empty(t, got)
	}
}

// AND A FORK TAKEN AFTER THE CLOSE STILL READS THE WHOLE FILE. The log is over;
// it is not gone.
func TestAForkAfterCloseReadsTheWholeLogAndThenEnds(t *testing.T) {
	l := newLog(t)
	write(t, l, "the whole thing\n")
	require.NoError(t, l.Close())

	r, err := l.Fork(0)
	require.NoError(t, err)
	defer r.Close()

	got, err := io.ReadAll(r)
	require.NoError(t, err, "io.ReadAll stops at EOF, which a closed log gives once it is drained")
	require.Equal(t, "the whole thing\n", string(got))
}

// A READER SLOWER THAN THE WRITER MISSES NOTHING AND HOLDS NOTHING UP. The file
// is the buffer: the writer runs to completion at full speed while the reader
// is still near the start, and the reader then finds every byte, in order.
func TestAReaderSlowerThanTheWriterMissesNothing(t *testing.T) {
	l := newLog(t)

	const lines = 500
	written := make(chan struct{})
	go func() {
		defer close(written)
		for i := range lines {
			write(t, l, fmt.Sprintf("line %04d\n", i))
		}
		closing(t, l)
	}()

	r, err := l.Fork(0)
	require.NoError(t, err)
	defer r.Close()

	// One byte at a time, from a reader that started before the writer did.
	var got bytes.Buffer
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		got.Write(buf[:n])
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
	}
	<-written

	var want bytes.Buffer
	for i := range lines {
		fmt.Fprintf(&want, "line %04d\n", i)
	}
	require.Equal(t, want.String(), got.String())
}

// A WRITE RACING A FORK. The fork is taken at whatever the size is, and
// everything written after it — including a write already in flight on another
// goroutine — is read by it, with nothing repeated and nothing lost.
func TestAForkTakenWhileWritingSeesExactlyWhatFollowsIt(t *testing.T) {
	l := newLog(t)

	const rounds = 200
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range rounds {
			write(t, l, fmt.Sprintf("%03d|", i))
		}
		closing(t, l)
	}()

	// Fork at "wherever this is now", which lands in the middle of the run.
	at := l.Size()
	r, err := l.Fork(at)
	require.NoError(t, err)
	defer r.Close()

	rest, err := io.ReadAll(r)
	require.NoError(t, err)
	wg.Wait()

	whole, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	require.Equal(t, string(whole[at:]), string(rest),
		"a reader forked at %d must see the file from %d, whatever landed in between", at, at)
	require.Equal(t, rounds*4, len(whole))
}

// NO READER EVER SEES A PARTIAL WRITE, and this measures the invariant that
// makes it true rather than the symptom: size only moves after write(2) has
// returned, so the readable length is always a whole number of records.
func TestTheReadableLengthIsNeverInsideAWrite(t *testing.T) {
	l := newLog(t)

	const (
		record  = 4096
		records = 100
	)
	line := bytes.Repeat([]byte("x"), record)

	stop := make(chan struct{})
	bad := make(chan int64, 1)
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if size := l.Size(); size%record != 0 {
				select {
				case bad <- size:
				default:
				}
				return
			}
		}
	}()

	for range records {
		n, err := l.Write(line)
		require.NoError(t, err)
		require.Equal(t, record, n)
	}
	close(stop)

	select {
	case size := <-bad:
		t.Fatalf("a reader could have read %d bytes, which is inside a %d-byte write", size, record)
	default:
	}
	require.Equal(t, int64(record*records), l.Size())
}

// CONCURRENT FORKS WHILE WRITING. Every reader gets the whole log, whatever
// instant it joined at — this is the -race case, and the assertion is that all
// of them agree with the file.
func TestManyReadersForkedAtDifferentInstantsAllGetTheWholeLog(t *testing.T) {
	l := newLog(t)

	const (
		readers = 16
		rounds  = 100
	)

	var wg sync.WaitGroup
	got := make([]string, readers)
	for i := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := l.Fork(0)
			if !assertNoError(t, err) {
				return
			}
			defer r.Close()
			out, err := io.ReadAll(r)
			assertNoError(t, err)
			got[i] = string(out)
		}()
	}

	for i := range rounds {
		write(t, l, fmt.Sprintf("%05d\n", i))
	}
	require.NoError(t, l.Close())
	wg.Wait()

	whole, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	for i, out := range got {
		require.Equal(t, string(whole), out, "reader %d", i)
	}
}

// assertNoError is require's job from a goroutine that is not the test's:
// require.NoError calls FailNow, and FailNow off the test goroutine is
// documented as not working.
func assertNoError(t *testing.T, err error) bool {
	t.Helper()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
		return false
	}
	return true
}

// A READER IS ABANDONED BY CLOSING IT, including one blocked at the end — which
// is what a streaming proxy does when the client on the other side goes away.
func TestClosingAReaderReleasesItsOwnBlockedRead(t *testing.T) {
	l := newLog(t)
	write(t, l, "x")

	r, err := l.Fork(1)
	require.NoError(t, err)

	pending := read(r, 8)
	pending.blocked(t)

	require.NoError(t, r.Close())
	_, err = pending.wait(t)
	require.ErrorIs(t, err, ErrClosed)

	// And it stays closed, without touching the log or another reader.
	_, err = r.Read(make([]byte, 1))
	require.ErrorIs(t, err, ErrClosed)
	require.NoError(t, r.Close(), "closing twice is the same close")

	other, err := l.Fork(0)
	require.NoError(t, err)
	defer other.Close()
	require.Equal(t, "x", readN(t, other, 1))
}

// UNLINK ENDS THE LOG, AND WHOEVER WAS READING FINISHES. That is the handoff:
// ship the bytes, unlink, let the watchers drain. What ends is the log — the
// name, the writing and this log's hold on the inode — and what does not is a
// reader that already forked.
func TestUnlinkEndsTheLogAndLetsItsReadersDrain(t *testing.T) {
	l := newLog(t)
	write(t, l, "kept\n")

	r, err := l.Fork(0)
	require.NoError(t, err)
	defer r.Close()

	require.NoError(t, l.Unlink())
	require.NoError(t, l.Unlink(), "unlinking twice is the same unlink")
	require.NoFileExists(t, l.Path())

	// The reader holds the inode: it reads what was there and then ends,
	// because the log is over.
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, "kept\n", string(got))

	_, err = l.Write([]byte("after\n"))
	require.ErrorIs(t, err, ErrClosed, "a log that has been let go is not written to")

	_, err = l.Fork(0)
	require.ErrorIs(t, err, ErrClosed, "and there is no inode left for a new reader to borrow")
}

// A NAME REMOVED BY SOMEBODY ELSE COSTS NOTHING AT ALL, and this is the one
// that used to lose a whole build's log. The directory these live in is
// somewhere the OS sweeps; a sweeper can take the NAME, and after Create
// nothing here ever resolves that name again — so the writer keeps appending to
// the inode it holds, a reader forked before the sweep keeps reading it, and a
// reader forked AFTER the sweep still gets every byte.
func TestASweptNameCostsTheLogNothing(t *testing.T) {
	l := newLog(t)
	write(t, l, "cloning\n")

	early, err := l.Fork(0)
	require.NoError(t, err)
	defer early.Close()

	require.NoError(t, os.Remove(l.Path()), "somebody else's sweep, not this log's Unlink")
	require.NoFileExists(t, l.Path())

	write(t, l, "compiling\n")
	require.Equal(t, "cloning\ncompiling\n", readN(t, early, 18))

	late, err := l.Fork(0)
	require.NoError(t, err, "a fork borrows the descriptor this log holds, never the path")
	defer late.Close()
	require.Equal(t, "cloning\ncompiling\n", readN(t, late, 18))
	require.Equal(t, int64(18), l.Size())
}

// AND A NAME THAT NOW BELONGS TO SOMEBODY ELSE'S FILE IS THE SHARPER HALF OF
// IT. A sweep that removes the name loses a log; a name REUSED — moved aside
// and re-created, which is what a rotation or a second process picking the same
// path does — hands a reader that resolves the path a different inode with
// somebody else's bytes in it, and nothing about that reads as an error. The
// log is the inode this log opened, so both the writer and every fork stay on
// it whatever the directory says afterwards.
func TestANameTakenOverByAnotherFileIsNotThisLog(t *testing.T) {
	l := newLog(t)
	write(t, l, "the build's own output\n")

	moved := l.Path() + ".moved"
	require.NoError(t, os.Rename(l.Path(), moved))
	require.NoError(t, os.WriteFile(l.Path(), []byte("somebody else's file\n"), 0o600))

	write(t, l, "and more of it\n")
	closing(t, l)

	whole, err := l.Fork(0)
	require.NoError(t, err)
	defer whole.Close()
	got, err := io.ReadAll(whole)
	require.NoError(t, err)
	require.Equal(t, "the build's own output\nand more of it\n", string(got),
		"a reader that resolved the path again would be reading a stranger's bytes")

	onDisk, err := os.ReadFile(moved)
	require.NoError(t, err)
	require.Equal(t, "the build's own output\nand more of it\n", string(onDisk),
		"and the appends went to the inode the writer holds, wherever its name went")
}

// THE CAP IS A BOUND ON A STRANGER'S OUTPUT, and it says where it stopped
// keeping bytes rather than just stopping.
func TestTheCapTruncatesAndTheLogSaysSo(t *testing.T) {
	l := newLog(t, WithMaxBytes(10))

	n, err := l.Write([]byte("12345"))
	require.NoError(t, err)
	require.Equal(t, 5, n)

	n, err = l.Write([]byte("6789012345"))
	require.ErrorIs(t, err, ErrFull)
	require.Equal(t, 5, n, "the bytes that fit are written")

	n, err = l.Write([]byte("more"))
	require.ErrorIs(t, err, ErrFull)
	require.Zero(t, n, "and everything after the cap is discarded")

	r, err := l.Fork(0)
	require.NoError(t, err)
	defer r.Close()
	got := readN(t, r, int(l.Size()))
	require.Equal(t, "1234567890"+string(truncationNotice), got,
		"the notice is written once and is over the cap, so a capped log explains itself")
}

func TestACapMustNotBeNegative(t *testing.T) {
	_, err := Create(filepath.Join(t.TempDir(), "x.log"), WithMaxBytes(-1))
	require.Error(t, err)
}

func TestWritingToAClosedLogIsRefused(t *testing.T) {
	l := newLog(t)
	require.NoError(t, l.Close())
	require.NoError(t, l.Close(), "closing twice is the same close")

	_, err := l.Write([]byte("late"))
	require.ErrorIs(t, err, ErrClosed)
}

func TestCreateReportsAPathItCannotOpen(t *testing.T) {
	_, err := Create(filepath.Join(t.TempDir(), "no-such-dir", "build.log"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// AN OFFSET IS WHAT A RECONNECTING CLIENT KEEPS. It reads half, loses its
// connection, and forks again where it was: nothing twice, nothing missed.
func TestAClientResumesWhereItsOffsetSays(t *testing.T) {
	l := newLog(t)
	write(t, l, "first half|second half")

	first, err := l.Fork(0)
	require.NoError(t, err)
	require.Equal(t, "first half|", readN(t, first, 11))
	resume := first.Offset()
	require.NoError(t, first.Close())

	write(t, l, "|and more")
	require.NoError(t, l.Close())

	second, err := l.Fork(resume)
	require.NoError(t, err)
	defer second.Close()

	rest, err := io.ReadAll(second)
	require.NoError(t, err)
	require.Equal(t, "second half|and more", string(rest))
}

// A WRITE FAILURE IS STICKY AND IT REACHES THE READERS. A log that lost bytes
// must not leave somebody blocked on a writer that will never write again.
func TestAFailedWriteIsReportedOnceAndToEveryone(t *testing.T) {
	l := newLog(t)
	write(t, l, "before\n")

	r, err := l.Fork(7)
	require.NoError(t, err)
	defer r.Close()
	pending := read(r, 16)
	pending.blocked(t)

	// Take the writer's descriptor away behind its back, which is the one way
	// to make write(2) fail on demand.
	require.NoError(t, l.file.Close())

	_, err = l.Write([]byte("after\n"))
	require.ErrorIs(t, err, os.ErrClosed)

	_, err = pending.wait(t)
	require.ErrorIs(t, err, os.ErrClosed, "a reader waiting at the end learns that there will be no more")

	_, err = l.Write([]byte("later\n"))
	require.ErrorIs(t, err, os.ErrClosed, "the failure is sticky")

	// WHAT IS LEFT TO READ IS NOT ASSERTED HERE, and that is the injection's
	// doing rather than the log's: taking the descriptor away is the one way to
	// make write(2) fail on demand, and every reader borrows that same
	// descriptor. A write that fails for the reason this stands in for — a full
	// disk — leaves the file perfectly readable, and it is the test below that
	// pins what a reader is then told.
}

// AND IT REACHES A READER OF A LOG THAT HAS ALREADY BEEN CLOSED, which is the
// one that matters and the one nothing could see.
//
// EVERY PRODUCTION READ OF A WHOLE LOG IS A READ OF A CLOSED ONE: the run ends,
// the writer closes, and only then is the log read back and handed to a byte
// store. A reader that asked "is this log closed" before "did this log fail"
// answered io.EOF on a log that had lost bytes — so the handoff could not tell
// that log from a complete one, and a job settled Success naming a log missing
// the lines that said why. Everything readable is still handed out first (the
// break above the two): what changed is which of them speaks when there is no
// more.
func TestALogThatLostBytesSaysSoAfterItIsClosed(t *testing.T) {
	l := newLog(t)
	write(t, l, "cloning\n")

	require.NoError(t, l.file.Close())
	_, err := l.Write([]byte("compiling\n"))
	require.ErrorIs(t, err, os.ErrClosed)

	// The run is over, so the log is closed and then read back whole.
	require.NoError(t, l.Close())

	r, err := l.Fork(l.Size())
	require.NoError(t, err)
	defer r.Close()

	_, err = r.Read(make([]byte, 16))
	require.ErrorIs(t, err, os.ErrClosed,
		"a closed log that lost bytes must say so, or nothing downstream can tell it from a whole one")
	require.NotErrorIs(t, err, io.EOF, "and io.EOF is what a complete log ends with")
}

// A FILE THAT GOT SHORTER THAN THE LOG SAYS IS A FAULT, not an end: something
// outside this process rewrote it, and reading whatever is at that offset now
// would hand a viewer somebody else's bytes.
func TestAFileTruncatedUnderAReaderStopsIt(t *testing.T) {
	l := newLog(t)
	write(t, l, "0123456789")

	r, err := l.Fork(0)
	require.NoError(t, err)
	defer r.Close()

	require.NoError(t, os.Truncate(l.Path(), 4))

	_, err = r.Read(make([]byte, 10))
	require.ErrorIs(t, err, ErrTruncated)
}

func TestReadIntoAnEmptyBufferAsksForNothing(t *testing.T) {
	l := newLog(t)
	r, err := l.Fork(0)
	require.NoError(t, err)
	defer r.Close()

	n, err := r.Read(nil)
	require.NoError(t, err, "a zero-length read must not block on an open log")
	require.Zero(t, n)
}

func TestAnEmptyLogThatIsClosedEndsImmediately(t *testing.T) {
	l := newLog(t)
	require.NoError(t, l.Close())

	r, err := l.Fork(0)
	require.NoError(t, err)
	defer r.Close()

	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Empty(t, got)
	require.Zero(t, l.Size())
}

// THE LAST HOLDER CLOSES THE FILE AND IS THE ONE THAT REPORTS IT. There is one
// descriptor per log, so which call closes it is whichever call lets go last —
// here the reader, because it outlived the log. A Close that cannot say it
// failed is a Close nobody can check.
func TestTheLastHolderClosesTheFileAndReportsIt(t *testing.T) {
	l := newLog(t)
	r, err := l.Fork(0)
	require.NoError(t, err)

	require.NoError(t, l.file.Close())
	require.NoError(t, l.Unlink(), "a log a reader is still on has not let go of the inode")
	require.ErrorIs(t, r.Close(), os.ErrClosed)
}

// AND A READER THAT IS NOT THE LAST ONE CLOSES NOTHING. Its Close costs the
// count and nothing else, so the writer and every other reader carry on.
func TestClosingOneReaderLeavesTheLogToTheRest(t *testing.T) {
	l := newLog(t)
	write(t, l, "one\n")

	first, err := l.Fork(0)
	require.NoError(t, err)
	second, err := l.Fork(0)
	require.NoError(t, err)
	defer second.Close()

	require.NoError(t, first.Close())

	write(t, l, "two\n")
	require.Equal(t, "one\ntwo\n", readN(t, second, 8))
	closing(t, l)
}

// And a log nobody is reading lets go of the inode in Unlink, so that is the
// call that reports a descriptor it could not close.
func TestUnlinkReportsADescriptorItCouldNotClose(t *testing.T) {
	l := newLog(t)
	require.NoError(t, l.file.Close())
	require.ErrorIs(t, l.Unlink(), os.ErrClosed)
}

// CLOSE ENDS THE WRITING AND NOTHING ELSE, so the read that matters most — the
// whole log, handed over once the build is done — happens after it.
func TestAClosedLogIsStillReadAndIsEndedByUnlink(t *testing.T) {
	l := newLog(t)
	write(t, l, "built\n")
	require.NoError(t, l.Close())
	require.NoError(t, l.Close(), "closing twice is the same close")

	whole, err := l.Fork(0)
	require.NoError(t, err)
	got, err := io.ReadAll(whole)
	require.NoError(t, err)
	require.Equal(t, "built\n", string(got))
	require.NoError(t, whole.Close())

	require.NoError(t, l.Unlink())
	_, err = l.Fork(0)
	require.ErrorIs(t, err, ErrClosed)
}

func TestUnlinkReportsAFailureThatIsNotAMissingFile(t *testing.T) {
	dir := t.TempDir()
	l, err := Create(filepath.Join(dir, "build.log"))
	require.NoError(t, err)
	defer l.Close()

	// A directory nobody may write is a removal that fails for a reason other
	// than the file not being there.
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory's write bit, so there is no failure to report")
	}
	require.ErrorIs(t, l.Unlink(), os.ErrPermission)
}

// A read that fails for any other reason is reported as itself. It is not
// ErrTruncated: only a file that got SHORTER is that, and calling every failure
// a truncation would tell a viewer the log was rewritten when the descriptor
// was simply gone.
func TestAReadThatFailsForAnotherReasonIsNotATruncation(t *testing.T) {
	l := newLog(t)
	write(t, l, "0123456789")

	r, err := l.Fork(0)
	require.NoError(t, err)
	require.NoError(t, l.file.Close())

	_, err = r.Read(make([]byte, 4))
	require.ErrorIs(t, err, os.ErrClosed)
	require.NotErrorIs(t, err, ErrTruncated)
}

// ONE READER, ONE CONSUMER, AND IT IS A REFUSAL. Two goroutines on one Reader
// both take the offset under the lock and both advance it, so bytes come out
// twice and others never come out at all — and every field they touch is behind
// the mutex, so -race sees nothing. Forking is what a second consumer does.
func TestOneReaderIsReadByOneGoroutine(t *testing.T) {
	l := newLog(t)
	write(t, l, "x")

	r, err := l.Fork(1)
	require.NoError(t, err)
	defer r.Close()

	// The first read is blocked at the end, which is where a second one would
	// otherwise queue up behind it.
	pending := read(r, 8)
	pending.blocked(t)

	_, err = r.Read(make([]byte, 8))
	require.ErrorIs(t, err, ErrConcurrentRead)

	write(t, l, "y")
	got, err := pending.wait(t)
	require.NoError(t, err)
	require.Equal(t, "y", got, "and the reader that was already there is unaffected")

	// And the refusal is not sticky: the reader works normally once the first
	// read has returned.
	write(t, l, "z")
	require.Equal(t, "z", readN(t, r, 1))
}
