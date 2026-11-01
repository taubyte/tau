package reserve

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// fixed is a filesystem that always reads the same.
func fixed(usage Usage) func(string) (Usage, error) {
	return func(string) (Usage, error) { return usage, nil }
}

func TestTheFloorIsWhatIsLeftAfterTheWrite(t *testing.T) {
	floor := Floor{Space: 0.05, Inodes: 0.05, Measure: fixed(Usage{Size: 1000, Free: 100, Files: 100, FreeFiles: 10})}

	require.NoError(t, floor.Take("/v", 50, 0))
	require.NoError(t, floor.Take("/v", 0, 5))

	var refused *Error
	require.ErrorAs(t, floor.Take("/v", 51, 0), &refused)
	require.Equal(t, Error{Path: "/v", Unit: "bytes", Free: 100, Want: 51, Reserve: 50}, *refused)
	require.EqualError(t, refused, "reserve: the filesystem holding /v has 100 bytes free, and 51 more would leave less than its reserve of 50")

	require.ErrorAs(t, floor.Take("/v", 0, 6), &refused)
	require.Equal(t, Error{Path: "/v", Unit: "inodes", Free: 10, Want: 6, Reserve: 5}, *refused)

	require.ErrorAs(t, floor.Take("/v", 101, 0), &refused, "more than is free at all")
}

func TestOnlyWhatIsAskedForIsChecked(t *testing.T) {
	short := Floor{Space: 0.05, Inodes: 0.05, Measure: fixed(Usage{Size: 1000, Free: 900, Files: 100, FreeFiles: 1})}
	require.NoError(t, short.Take("/v", 10, 0))
	require.Error(t, short.Take("/v", 0, 1))

	unread := Floor{Measure: func(string) (Usage, error) { return Usage{}, errors.New("read") }}
	require.NoError(t, unread.Take("/v", 0, 0))
	require.ErrorContains(t, unread.Take("/v", 1, 0), "reading the filesystem holding /v: read")
}

func TestATotalThatIsNotReportedHoldsNothingBack(t *testing.T) {
	floor := Floor{Space: 0.5, Inodes: 0.5, Measure: fixed(Usage{})}
	require.NoError(t, floor.Take("/v", 1<<40, 1<<20))
}

func TestTheFilesystemIsReadForReal(t *testing.T) {
	dir := t.TempDir()

	usage, err := measured(dir)
	require.NoError(t, err)
	require.NotZero(t, usage.Size)
	require.LessOrEqual(t, usage.Free, usage.Size)

	var refused *Error
	require.ErrorAs(t, Floor{Space: 1}.Take(dir, 1, 0), &refused)
	require.Equal(t, usage.Size, refused.Reserve)
	require.NoError(t, Floor{}.Take(dir, 1, 1))

	_, err = measured(filepath.Join(dir, "missing"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestTheDefaultIsJournaldsSpaceAndKubeletsInodes(t *testing.T) {
	require.Equal(t, Floor{Space: 0.15, Most: 4 << 30, Inodes: 0.05}, Default)

	small := Default
	small.Measure = fixed(Usage{Size: 10 << 30, Free: 2 << 30, Files: 1000, FreeFiles: 100})
	var refused *Error
	require.ErrorAs(t, small.Take("/v", 1<<29+1, 0), &refused)
	require.Equal(t, uint64(3<<30)/2, refused.Reserve, "15% of a 10GiB disk")

	large := Default
	large.Measure = fixed(Usage{Size: 800 << 30, Free: 20 << 30, Files: 1000, FreeFiles: 100})
	require.NoError(t, large.Take("/v", 16<<30, 0), "a disk with 20GiB free keeps 4GiB, not 120")
	require.ErrorAs(t, large.Take("/v", 16<<30+1, 0), &refused)
	require.Equal(t, uint64(4<<30), refused.Reserve)

	require.NoError(t, large.Take("/v", 0, 50))
	require.ErrorAs(t, large.Take("/v", 0, 51), &refused)
	require.Equal(t, uint64(50), refused.Reserve, "5% of the inodes, uncapped")
}

// made is an empty file to copy into.
func made(t *testing.T) *os.File {
	t.Helper()
	file, err := os.Create(filepath.Join(t.TempDir(), "file"))
	require.NoError(t, err)
	t.Cleanup(func() { file.Close() })
	return file
}

func TestACopyHoldsWhatItRead(t *testing.T) {
	data := bytes.Repeat([]byte("data"), block)
	for name, content := range map[string][]byte{
		"empty":                {},
		"short":                []byte("x"),
		"zeros then data":      append(make([]byte, 3*Chunk+5), data...),
		"data then zeros":      append(append([]byte{}, data...), make([]byte, 2*Chunk+7)...),
		"only zeros":           make([]byte, Chunk+1),
		"a zero block between": append(append(append([]byte{}, data[:block+1]...), make([]byte, 2*block)...), data[:3]...),
	} {
		t.Run(name, func(t *testing.T) {
			file := made(t)
			n, err := Floor{}.Copy(file, bytes.NewReader(content))
			require.NoError(t, err)
			require.Equal(t, int64(len(content)), n)

			got, err := os.ReadFile(file.Name())
			require.NoError(t, err)
			require.True(t, bytes.Equal(content, got), "the copy differs from what was read")
		})
	}
}

// counted is a filesystem whose free space is what an earlier reading said,
// less what has been written since, and that counts how often it is read.
type counted struct {
	free  uint64
	reads int
	file  *os.File
}

func (c *counted) measure(string) (Usage, error) {
	c.reads++
	return Usage{Size: 100 * Chunk, Free: c.free}, nil
}

func TestTheFilesystemIsReadBeforeEveryChunk(t *testing.T) {
	disk := &counted{free: 100 * Chunk}
	floor := Floor{Space: 0.5, Measure: disk.measure}
	content := bytes.Repeat([]byte{1}, 8*Chunk)

	reader := &taking{Reader: bytes.NewReader(content), after: 4 * Chunk, take: func() { disk.free = 50*Chunk + 1 }}
	n, err := floor.Copy(made(t), reader)
	var refused *Error
	require.ErrorAs(t, err, &refused)
	require.Equal(t, int64(3*Chunk), n, "the copy stopped somewhere other than where the space went")
	require.Equal(t, 4, disk.reads)
}

// taking is a reader that runs take once after has been read.
type taking struct {
	io.Reader
	after int
	take  func()
	read  int
}

func (r *taking) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += n
	if r.take != nil && r.read >= r.after {
		r.take()
		r.take = nil
	}
	return n, err
}

func TestAHoleReadsNothingAndAReaderThatFailsIsSaid(t *testing.T) {
	disk := &counted{}
	_, err := Floor{Space: 0.5, Measure: disk.measure}.Copy(made(t), bytes.NewReader(make([]byte, 3*Chunk)))
	require.NoError(t, err)
	require.Zero(t, disk.reads)

	_, err = Floor{}.Copy(made(t), io.MultiReader(bytes.NewReader([]byte("x")), failing{io.ErrUnexpectedEOF}))
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

type failing struct{ err error }

func (f failing) Read([]byte) (int, error) { return 0, f.err }

func TestAFileThatCannotBeWrittenIsSaid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	readOnly, err := os.Open(path)
	require.NoError(t, err)
	defer readOnly.Close()

	_, err = Floor{}.Copy(readOnly, bytes.NewReader([]byte("x")))
	require.Error(t, err)
	_, err = Floor{}.Copy(readOnly, bytes.NewReader(make([]byte, 10)))
	require.Error(t, err, "a hole at the end of a file that cannot be extended")

	closed := made(t)
	require.NoError(t, closed.Close())
	_, err = Floor{}.Copy(closed, bytes.NewReader(nil))
	require.Error(t, err)
}
