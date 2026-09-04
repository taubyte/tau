//go:build unix

package livelog

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// mkfifo makes a named pipe, and reports whether the platform let it. A fifo is
// the refusal that matters most and the one with the least portable
// constructor, so the test that needs one asks rather than assumes.
func mkfifo(t *testing.T, path string) bool {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Logf("no fifo on this platform: %v", err)
		return false
	}
	return true
}

// stat is a FileInfo this test made rather than one the filesystem answered
// with, for the two states an unprivileged process cannot arrange on a real
// disk: a directory owned by somebody else, and an answer that carries no unix
// metadata at all.
type stat struct {
	mode fs.FileMode
	sys  any
}

func (s stat) Name() string       { return "made-up" }
func (s stat) Size() int64        { return 0 }
func (s stat) Mode() fs.FileMode  { return s.mode }
func (s stat) ModTime() time.Time { return time.Time{} }
func (s stat) IsDir() bool        { return s.mode.IsDir() }
func (s stat) Sys() any           { return s.sys }

// owned is the unix half of a made-up FileInfo. Nlink is set as an untyped
// constant because its width is the platform's — uint64 on linux/amd64, uint16
// on darwin — and a literal that named either would not compile on the other.
func owned(uid int) *syscall.Stat_t {
	var st syscall.Stat_t
	st.Uid = uint32(uid)
	st.Nlink = 1
	return &st
}

// A DIRECTORY BELONGING TO SOMEBODY ELSE IS REFUSED AND NOT TIGHTENED. It is
// the answer that has no repair in it: a chmod would work as root and would
// leave the OWNER — who is not us — still able to create names in it, which is
// the whole precondition of the attack Create refuses one level down.
//
// It is asked of a FileInfo because arranging it on a real filesystem needs a
// second account.
func TestADirectoryBelongingToSomebodyElseIsRefused(t *testing.T) {
	foreign := stat{mode: fs.ModeDir | 0o700, sys: owned(os.Geteuid() + 1)}
	require.ErrorIs(t, suitable("/somewhere", foreign), ErrNotALog)

	ours := stat{mode: fs.ModeDir | 0o700, sys: owned(os.Geteuid())}
	require.NoError(t, suitable("/somewhere", ours))

	loose := stat{mode: fs.ModeDir | 0o777, sys: owned(os.Geteuid())}
	require.ErrorIs(t, suitable("/somewhere", loose), errLoose,
		"ours and loose is the one answer that is a repair rather than a refusal")

	// AND IT IS THE WRITE BITS. A directory somebody else may LIST is not one
	// they may plant a name in, and tightening it would take away something an
	// operator set on purpose for a property this package does not need.
	readable := stat{mode: fs.ModeDir | 0o750, sys: owned(os.Geteuid())}
	require.NoError(t, suitable("/somewhere", readable))
}

// AND A FileInfo THAT IS NOT THE OPERATING SYSTEM'S ANSWERS PERMISSIVELY. These
// two read unix metadata off an interface, so the type assertion has a failing
// branch by construction; what it must not do is panic or refuse a log for a
// question it could not ask.
func TestUnixMetadataOffSomethingThatCarriesNoneIsNotAFailure(t *testing.T) {
	blank := stat{mode: fs.ModeDir | 0o700, sys: nil}
	require.Equal(t, uint64(1), links(blank))
	require.True(t, ownedByThisProcess(blank))
	require.NoError(t, suitable("/somewhere", blank))
}

// THE FILESYSTEM ERRORS BOTH HALVES REPORT, asked of the private steps because
// a working disk does not produce them between one call and the next.
func TestTheStepsReportAFilesystemThatAnsweredAnError(t *testing.T) {
	require.ErrorIs(t, ownDir(filepath.Join(t.TempDir(), "never-made")), os.ErrNotExist,
		"a directory that went between being made and being looked at")
	require.ErrorIs(t, tighten(filepath.Join(t.TempDir(), "never-made")), os.ErrNotExist,
		"and one that went between being found loose and being closed")

	// A DESCRIPTOR THAT MAY NOT BE WRITTEN CANNOT BE EMPTIED, and a log that
	// starts with somebody else's bytes still in it is not a log this build
	// wrote. Create never opens read-only; own is asked directly because
	// nothing on a working disk makes ftruncate fail on a writable file.
	path := filepath.Join(t.TempDir(), "build.log")
	require.NoError(t, os.WriteFile(path, []byte("what was there before"), 0o600))

	readOnly, err := os.Open(path)
	require.NoError(t, err)
	defer readOnly.Close()
	require.ErrorContains(t, own(readOnly, path), "emptying")

	kept, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "what was there before", string(kept))

	// And a descriptor that is gone answers nothing at all.
	closed, err := os.Open(path)
	require.NoError(t, err)
	require.NoError(t, closed.Close())
	require.ErrorIs(t, own(closed, path), os.ErrClosed)
}
