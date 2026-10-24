package volume_test

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node/volume"
	"github.com/taubyte/tau/pkg/invoke"
)

// volume_test is about bytes that have to survive two things: the container they
// were written from, and the node it ran on.
//
// EVERY ONE OF THESE USED TO NEED A NODE — a register, a worker, a mocknet host
// and a started composition — to assert things about a tar and a directory. The
// cases that matter most are the ones a node made hardest to reach: a store whose
// read fails, a pointer naming bytes nobody has, a second cold start arriving
// while the first is still unpacking. They are ordinary rows here.

// ---------------------------------------------------------------- doubles

// blobs is an Assets double: a content-addressed store over a map.
//
// THE ID IS A FUNCTION OF THE CONTENT, which is not a detail of the fake — it is
// the property the "a node whose workload left writes nothing" rule rests on. A
// double that minted a fresh id per write would hide exactly the behaviour this
// package relies on.
type blobs struct {
	mu   sync.Mutex
	held map[string][]byte

	putErr error
	getErr error

	// refuseUnread is a store that says no BEFORE reading a byte, which is what
	// a full disk or a closed DAG looks like from here.
	refuseUnread error

	// gate, when set, holds every Get until it is closed — a restore that is
	// still unpacking while a second cold start arrives.
	gate chan struct{}

	gets int
	puts int
}

func newBlobs() *blobs { return &blobs{held: map[string][]byte{}} }

func (b *blobs) Put(_ context.Context, content io.Reader) (string, error) {
	b.mu.Lock()
	refuse := b.refuseUnread
	b.mu.Unlock()
	if refuse != nil {
		return "", refuse
	}

	body, err := io.ReadAll(content)
	if err != nil {
		return "", err
	}
	b.mu.Lock()
	b.puts++
	err = b.putErr
	b.mu.Unlock()
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(body)
	id := "Qm" + hex.EncodeToString(sum[:])

	b.mu.Lock()
	b.held[id] = body
	b.mu.Unlock()
	return id, nil
}

func (b *blobs) Get(_ context.Context, id string) (io.ReadSeekCloser, error) {
	b.mu.Lock()
	b.gets++
	gate, err := b.gate, b.getErr
	b.mu.Unlock()

	if gate != nil {
		<-gate
	}
	if err != nil {
		return nil, err
	}

	b.mu.Lock()
	body, ok := b.held[id]
	b.mu.Unlock()
	if !ok {
		return nil, os.ErrNotExist
	}
	return nopSeekCloser{bytes.NewReader(body)}, nil
}

// put is a checkpoint somebody else wrote: arbitrary bytes under their own id.
func (b *blobs) put(t *testing.T, body []byte) string {
	t.Helper()
	id, err := b.Put(context.Background(), bytes.NewReader(body))
	require.NoError(t, err)
	return id
}

func (b *blobs) counts() (gets, puts int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.gets, b.puts
}

// lose drops every byte the store holds, keeping the ids nothing can read.
func (b *blobs) lose() {
	b.mu.Lock()
	b.held = map[string][]byte{}
	b.mu.Unlock()
}

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }

// rows is a Rows double: the replicated row a checkpoint's pointer lives in.
type rows struct {
	mu   sync.Mutex
	held map[string][]byte

	getErr error
	putErr error
	puts   int
}

func newRows() *rows { return &rows{held: map[string][]byte{}} }

func (r *rows) Get(_ context.Context, key string) ([]byte, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getErr != nil {
		return nil, false, r.getErr
	}
	value, held := r.held[key]
	return value, held, nil
}

func (r *rows) Put(_ context.Context, key string, value []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.puts++
	if r.putErr != nil {
		return r.putErr
	}
	r.held[key] = value
	return nil
}

func (r *rows) at(key string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, held := r.held[key]
	return string(value), held
}

func (r *rows) written() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.puts
}

// opening is a Stores double, and it counts: the database is opened once on the
// first checkpoint that needs one, not per pass.
type opening struct {
	rows  volume.Rows
	err   error
	mu    sync.Mutex
	asked int
}

func (o *opening) Store() (volume.Rows, error) {
	o.mu.Lock()
	o.asked++
	o.mu.Unlock()
	if o.err != nil {
		return nil, o.err
	}
	return o.rows, nil
}

func (o *opening) times() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.asked
}

// said records what an operator was told. The quota is MEASURED rather than
// enforced, so the warning is the whole of the enforcement — which makes it
// something to assert on rather than a line somebody hopes was printed.
type said struct {
	mu    sync.Mutex
	infos []string
	warns []string
}

func (s *said) Infof(format string, args ...any) {
	s.mu.Lock()
	s.infos = append(s.infos, fmt.Sprintf(format, args...))
	s.mu.Unlock()
}

func (s *said) Warnf(format string, args ...any) {
	s.mu.Lock()
	s.warns = append(s.warns, fmt.Sprintf(format, args...))
	s.mu.Unlock()
}

func (s *said) warned() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.warns...)
}

func (s *said) told() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.infos...)
}

// ---------------------------------------------------------------- fixtures

// recordedAt is where a checkpoint's pointer lives.
//
// IT IS SPELLED OUT HERE rather than read off the package: the key is a
// replicated row's layout, which every node in a cloud has to agree on, so a
// test that took it from the code under test could not notice it changing.
const recordedAt = "volume/at/"

// mountGrant is what services/worker hands a tier for one `volume:` entry.
func mountGrant(handle string, settings map[string]string) invoke.Grant {
	return invoke.Grant{
		Kind: invoke.MountKind, Label: "pgdata", Handle: handle,
		Ops:      []string{"mount-rw"},
		Settings: settings,
	}
}

// holding is a volume backing over a fresh root, a store and a row.
func holding(t *testing.T) (*volume.Volumes, *blobs, *rows, *said) {
	t.Helper()
	return holdingAt(t, t.TempDir())
}

func holdingAt(t *testing.T, root string) (*volume.Volumes, *blobs, *rows, *said) {
	t.Helper()

	store, index, log := newBlobs(), newRows(), &said{}
	v, err := volume.New(root, store, &opening{rows: index}, log)
	require.NoError(t, err)
	t.Cleanup(func() { v.Close() })
	return v, store, index, log
}

// tarOf builds an archive with the entries a test names.
func tarOf(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	archive := tar.NewWriter(&buf)
	for name, body := range files {
		require.NoError(t, archive.WriteHeader(&tar.Header{
			Name: name, Mode: 0o640, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}))
		_, err := archive.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, archive.Close())
	return buf.Bytes()
}

// headers is every entry of an archive, by name.
func headers(t *testing.T, archive []byte) map[string]tar.Header {
	t.Helper()

	out := map[string]tar.Header{}
	read := tar.NewReader(bytes.NewReader(archive))
	for {
		header, err := read.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		require.NoError(t, err)
		out[header.Name] = *header
	}
}

// ---------------------------------------------------------------- construction

// TestANodeWithNoRootHoldsNoVolumes, and says so by name — a container started
// without the filesystem it declared writes into its own layer and loses it.
func TestEveryDependencyAVolumeCannotDoWithoutIsRefused(t *testing.T) {
	store, index, log := newBlobs(), newRows(), &said{}

	_, err := volume.New("", store, &opening{rows: index}, log)
	require.ErrorIs(t, err, volume.ErrNoRoot)

	_, err = volume.New(t.TempDir(), store, nil, log)
	require.ErrorContains(t, err, "nowhere to record")

	_, err = volume.New(t.TempDir(), store, &opening{rows: index}, nil)
	require.ErrorContains(t, err, "nowhere to say")

	// AND THE BYTE STORE IS THE ONE THAT MAY BE ABSENT: a node with a disk and
	// no DAG mounts what it holds, and says so on every checkpoint.
	v, err := volume.New(t.TempDir(), nil, &opening{rows: index}, log)
	require.NoError(t, err)
	dir, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err)
	require.DirExists(t, dir)
	require.Equal(t, 0, v.CheckpointAll(t.Context()))
	require.Contains(t, strings.Join(log.warned(), "\n"), volume.ErrNoStore.Error())
}

// ---------------------------------------------------------------- a directory

// TestAMountISADirectory, and the directory is under this node's own root rather
// than anywhere a handle could point.
func TestMountingMakesADirectory(t *testing.T) {
	root := t.TempDir()
	v, err := volume.New(root, newBlobs(), &opening{rows: newRows()}, &said{})
	require.NoError(t, err)

	dir, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(dir, root), "%s is not under %s", dir, root)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, info.IsDir())

	// AND A SECOND MOUNT OF ONE HANDLE IS THE SAME DIRECTORY, answered from what
	// this node already holds rather than walked again.
	again, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err)
	require.Equal(t, dir, again)
}

// TestAHANDLEIsONEPathSegment. A handle carries slashes — project, environment,
// id — and a directory per segment would put one project's volumes inside
// another project's name.
func TestAHandleDoesNotBecomeAPathTree(t *testing.T) {
	root := t.TempDir()
	v, err := volume.New(root, newBlobs(), &opening{rows: newRows()}, &said{})
	require.NoError(t, err)

	dir, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "volumes"), filepath.Dir(dir))
	require.NotContains(t, filepath.Base(dir), "/")
}

// TestTWODeploymentsAreTWODirectories, which is the same property every other
// handle has — and the escape escapes ITSELF, so a handle that spells the
// escape cannot be spelled two ways into one directory.
func TestVolumesAreScopedByHandle(t *testing.T) {
	v, _, _, _ := holding(t)

	held := map[string]string{}
	for _, handle := range []string{
		"shop/prod/QmData", "shop/staging/QmData", "other/prod/QmData",
		"shop/prod/QmOther", "shop%2Fprod%2FQmData",
	} {
		dir, err := v.Mount(t.Context(), mountGrant(handle, nil))
		require.NoError(t, err)
		require.NotContains(t, held, dir, "%s landed in the directory of %s", handle, held[dir])
		held[dir] = handle
	}
	require.Len(t, held, 5)
}

// TestADIRECTORYThatCannotBeMadeIsAnError rather than a mount that answers a
// path nothing is there.
func TestAVolumeDirectoryThatCannotBeMadeIsRefused(t *testing.T) {
	// A ROOT THAT IS A FILE. Every path under it is not a directory, which is
	// not "does not exist" — and reading it as one would be answering a mount
	// with a directory nothing made.
	file := filepath.Join(t.TempDir(), "root")
	require.NoError(t, os.WriteFile(file, []byte("not a directory"), 0o640))

	v, err := volume.New(file, newBlobs(), &opening{rows: newRows()}, &said{})
	require.NoError(t, err)
	_, err = v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.Error(t, err)

	// AND A PARENT THAT CANNOT BE CREATED. `volumes` here is a link to nowhere,
	// so the path does not exist and cannot be made either.
	root := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(root, "nowhere"), filepath.Join(root, "volumes")))

	v, err = volume.New(root, newBlobs(), &opening{rows: newRows()}, &said{})
	require.NoError(t, err)
	_, err = v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.ErrorContains(t, err, "making a volume directory")
}

// ---------------------------------------------------------------- the pointer

// TestTheCheckpointRecordsWHERE, in a replicated row any node may write.
func TestACheckpointIsRecorded(t *testing.T) {
	v, store, index, _ := holding(t)
	handle := "shop/prod/QmData"

	dir, err := v.Mount(t.Context(), mountGrant(handle, nil))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "PG_VERSION"), []byte("16"), 0o640))

	require.Equal(t, 1, v.CheckpointAll(t.Context()))

	recorded, held := index.at(recordedAt + handle)
	require.True(t, held, "nothing was recorded for %s", handle)

	store.mu.Lock()
	_, bytesHeld := store.held[recorded]
	store.mu.Unlock()
	require.True(t, bytesHeld, "the pointer names no object: %s", recorded)
}

// TestNOTHINGIsWrittenWhenNothingChanged. Unchanged bytes are the same object,
// and that is what keeps a node that no longer runs the workload from putting its
// own older bytes back over the pointer every interval until somebody notices a
// database that lost a day.
func TestAnUnchangedVolumeWritesNoPointer(t *testing.T) {
	v, _, index, _ := holding(t)
	handle := "shop/prod/QmData"

	dir, err := v.Mount(t.Context(), mountGrant(handle, nil))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows"), []byte("alice"), 0o640))

	require.Equal(t, 1, v.CheckpointAll(t.Context()))
	require.Equal(t, 1, index.written())

	// SAME FILES, SAME TAR, SAME CID — so the pass succeeds and writes nothing.
	require.Equal(t, 1, v.CheckpointAll(t.Context()))
	require.Equal(t, 1, index.written(), "an unchanged volume wrote its pointer again")

	// AND A CHANGED ONE DOES WRITE.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows"), []byte("alice\nbob"), 0o640))
	require.Equal(t, 1, v.CheckpointAll(t.Context()))
	require.Equal(t, 2, index.written())
}

// TestTHESTOREIsOpenedONCE, on the first checkpoint that needs it — a node that
// mounts nothing opens no database at all.
func TestTheStoreIsAskedForOnce(t *testing.T) {
	store, index, log := newBlobs(), newRows(), &said{}
	stores := &opening{rows: index}

	v, err := volume.New(t.TempDir(), store, stores, log)
	require.NoError(t, err)
	require.Zero(t, stores.times(), "a backing nobody has asked opened a database")

	for _, handle := range []string{"shop/prod/QmA", "shop/prod/QmB"} {
		_, err := v.Mount(t.Context(), mountGrant(handle, nil))
		require.NoError(t, err)
	}
	v.CheckpointAll(t.Context())
	v.CheckpointAll(t.Context())

	require.Equal(t, 1, stores.times(), "the database was opened per pass")
}

// TestANODEWithNowhereToRecordStillMounts. Having no store is not having an
// empty one: the directory is made and served, and every checkpoint says why it
// could not be recorded.
func TestAVolumeWithNoRowStillMounts(t *testing.T) {
	log := &said{}
	v, err := volume.New(t.TempDir(), newBlobs(),
		&opening{err: errors.New("no database factory to open a volume with")}, log)
	require.NoError(t, err)

	dir, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err)
	require.DirExists(t, dir)

	require.Equal(t, 0, v.CheckpointAll(t.Context()))
	require.Contains(t, strings.Join(log.warned(), "\n"), "no database factory")
}

// TestAPOINTERThatCouldNotBeWrittenIsSaidAndIsNotCounted. The bytes are in the
// store and nothing can find them, which is a checkpoint that did not happen.
func TestAPointerThatCannotBeWrittenIsNotACheckpoint(t *testing.T) {
	store, index, log := newBlobs(), newRows(), &said{}
	index.putErr = errors.New("the row was rejected")

	v, err := volume.New(t.TempDir(), store, &opening{rows: index}, log)
	require.NoError(t, err)
	_, err = v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err)

	require.Equal(t, 0, v.CheckpointAll(t.Context()))
	require.Contains(t, strings.Join(log.warned(), "\n"), "the row was rejected")
}

// TestBYTESThatCouldNotBeStoredAreSaid, naming the handle: a checkpoint is
// per-volume and an operator has to know which one.
func TestBytesThatCannotBeStoredAreSaid(t *testing.T) {
	store, index, log := newBlobs(), newRows(), &said{}
	store.putErr = errors.New("the disk is full")

	v, err := volume.New(t.TempDir(), store, &opening{rows: index}, log)
	require.NoError(t, err)
	_, err = v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err)

	require.Equal(t, 0, v.CheckpointAll(t.Context()))
	warned := strings.Join(log.warned(), "\n")
	require.Contains(t, warned, "checkpointing shop/prod/QmData")
	require.Contains(t, warned, "the disk is full")
}

// TestAFailingCheckpointDoesNotStopTheOthers. A pass over every due volume is
// one pass: the one whose walk failed is said and skipped, and the rest are
// still written.
func TestAFailingCheckpointDoesNotStopTheOthers(t *testing.T) {
	v, _, index, log := holding(t)

	gone, err := v.Mount(t.Context(), mountGrant("shop/prod/QmGone", nil))
	require.NoError(t, err)
	_, err = v.Mount(t.Context(), mountGrant("shop/prod/QmHere", nil))
	require.NoError(t, err)

	// THE DIRECTORY GOES from under one of them, which is the failure a pass has
	// to survive rather than the failure it reports.
	require.NoError(t, os.RemoveAll(gone))

	require.Equal(t, 1, v.CheckpointAll(t.Context()))
	require.Contains(t, strings.Join(log.warned(), "\n"), "QmGone")

	_, held := index.at(recordedAt + "shop/prod/QmHere")
	require.True(t, held, "the volume that was still there was not checkpointed")
}

// ---------------------------------------------------------------- the interval

// TestTHEDECLAREDIntervalReachesTheVolume, which it did not: the composer never
// carried `checkpoint/interval`, so a volume asking to be checkpointed every ten
// seconds was checkpointed every minute and a restore lost fifty seconds more
// than its author budgeted for.
//
// AND ONLY WHAT IS DUE IS WALKED, so a volume asking for an hour is not tarred
// every tick.
func TestOnlyVolumesWhoseIntervalHasElapsedAreWalked(t *testing.T) {
	for _, c := range []struct {
		name     string
		declared string
		every    time.Duration
	}{
		{"a declared interval", "10s", 10 * time.Second},
		{"an hour", "1h0m0s", time.Hour},
		// A COMPILED OBJECT THAT CARRIED NONE, and one that carried something
		// that is not a duration, both take the vocabulary's own default.
		{"nothing declared", "", time.Minute},
		{"not a duration", "every so often", time.Minute},
		{"not a positive one", "-10s", time.Minute},
	} {
		t.Run(c.name, func(t *testing.T) {
			v, _, _, _ := holding(t)
			var settings map[string]string
			if c.declared != "" {
				settings = map[string]string{"checkpoint/interval": c.declared}
			}

			_, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", settings))
			require.NoError(t, err)

			now := time.Now()
			require.Equal(t, 1, v.Checkpoint(t.Context(), now), "a volume nobody has written is due")
			require.Equal(t, 0, v.Checkpoint(t.Context(), now.Add(c.every-time.Millisecond)),
				"it was walked before its interval came round")
			require.Equal(t, 1, v.Checkpoint(t.Context(), now.Add(c.every)))

			// AND THE WAY OUT DOES NOT ASK. A planned shutdown that skipped a
			// volume because its hour had not come round would throw away
			// writes inside the budget its author declared.
			require.Equal(t, 1, v.CheckpointAll(t.Context()))
		})
	}
}

// TestTHEQUOTAIsMeasuredAndIsSAID. It is not a wall — see the package doc for
// why — and what it must not be is silent: the warning IS the enforcement, so it
// carries what the walk found and what the author declared.
func TestGoingOverTheQuotaIsSaid(t *testing.T) {
	v, _, _, log := holding(t)
	grant := mountGrant("shop/prod/QmData", map[string]string{"quota": "8"})

	dir, err := v.Mount(t.Context(), grant)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows"), bytes.Repeat([]byte("x"), 64), 0o640))

	require.Equal(t, 1, v.CheckpointAll(t.Context()), "and it is served rather than refused")
	require.Equal(t, []string{"volume: shop/prod/QmData holds 64 bytes and declared a quota of 8"},
		log.warned())
}

// TestAVolumeWithinItsQuotaSaysNothing, and neither does one that declared none
// — a warning on every checkpoint of every volume is a warning nobody reads.
func TestStayingUnderTheQuotaIsSilent(t *testing.T) {
	for _, declared := range []string{"1024", "", "a lot"} {
		v, _, _, log := holding(t)
		var settings map[string]string
		if declared != "" {
			settings = map[string]string{"quota": declared}
		}

		dir, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", settings))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "rows"), []byte("small"), 0o640))

		require.Equal(t, 1, v.CheckpointAll(t.Context()))
		require.Empty(t, log.warned(), "quota %q", declared)
	}
}

// ---------------------------------------------------------------- the move

// TestTHEBYTESSurviveTheNode: a checkpoint is written, a second node with the
// same store and a different root restores it.
//
// NEITHER NODE IS SPECIAL. The pointer is a replicated row rather than a raft
// write, so the node holding the volume records it wherever placement put the
// workload — which is the whole point: a leader-only write meant every other
// node checkpointed nothing, silently.
func TestAVolumeIsRestoredOnAnotherNode(t *testing.T) {
	store, index := newBlobs(), newRows()
	node := func() *volume.Volumes {
		v, err := volume.New(t.TempDir(), store, &opening{rows: index}, &said{})
		require.NoError(t, err)
		return v
	}

	grant := mountGrant("shop/prod/QmData", nil)
	source := node()
	dir, err := source.Mount(t.Context(), grant)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "PG_VERSION"), []byte("16"), 0o640))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "base", "1"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "base", "1", "rows"), []byte("alice"), 0o640))
	require.Equal(t, 1, source.CheckpointAll(t.Context()))

	// A SECOND NODE, sharing the store and the row and holding none of the bytes
	// — which is what placement moving a workload looks like.
	restored, err := node().Mount(t.Context(), grant)
	require.NoError(t, err)
	require.NotEqual(t, dir, restored)

	version, err := os.ReadFile(filepath.Join(restored, "PG_VERSION"))
	require.NoError(t, err)
	require.Equal(t, "16", string(version))

	rows, err := os.ReadFile(filepath.Join(restored, "base", "1", "rows"))
	require.NoError(t, err)
	require.Equal(t, "alice", string(rows), "a nested file came back too")
}

// TestALIVEDirectoryIsNotOVERWRITTENByAnOlderCheckpoint. The checkpoint is behind
// by up to an interval by construction, so restoring over bytes this node already
// holds would lose exactly the writes it exists to bound.
func TestAnExistingDirectoryIsNotRestoredOver(t *testing.T) {
	root := t.TempDir()
	store, index := newBlobs(), newRows()
	node := func() *volume.Volumes {
		v, err := volume.New(root, store, &opening{rows: index}, &said{})
		require.NoError(t, err)
		return v
	}

	grant := mountGrant("shop/prod/QmData", nil)
	first := node()
	dir, err := first.Mount(t.Context(), grant)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows"), []byte("old"), 0o640))
	require.Equal(t, 1, first.CheckpointAll(t.Context()))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows"), []byte("new"), 0o640))

	// A FRESH BACKING OVER THE SAME ROOT: the directory is already there, so
	// nothing is unpacked into it.
	again, err := node().Mount(t.Context(), grant)
	require.NoError(t, err)
	require.Equal(t, dir, again)

	rows, err := os.ReadFile(filepath.Join(dir, "rows"))
	require.NoError(t, err)
	require.Equal(t, "new", string(rows), "the checkpoint overwrote live data")
}

// TestTheNodeAWorkloadLeftDoesNotPutItsOldBytesBack.
//
// A node never stops holding a volume: Mount has no opposite, and the tier's
// seam is one method. So when placement moves a workload, the node it left keeps
// checkpointing a directory nothing writes to any more — into the same replicated
// key the node now running it writes. The old one writes last as often as its
// interval comes round, and the pointer ends up naming bytes from before the
// move.
func TestTheNodeAWorkloadLeftDoesNotPutItsOldBytesBack(t *testing.T) {
	store, index := newBlobs(), newRows()
	grant := mountGrant("shop/prod/QmData", nil)
	node := func() *volume.Volumes {
		v, err := volume.New(t.TempDir(), store, &opening{rows: index}, &said{})
		require.NoError(t, err)
		return v
	}

	// THE NODE IT STARTED ON writes a row and checkpoints it.
	left := node()
	dir, err := left.Mount(t.Context(), grant)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows"), []byte("alice"), 0o640))
	require.Equal(t, 1, left.CheckpointAll(t.Context()))

	// THE WORKLOAD MOVES. The new node restores what the old one wrote, the
	// workload writes more, and that is what the pointer should name.
	arrived := node()
	moved, err := arrived.Mount(t.Context(), grant)
	require.NoError(t, err)
	require.NotEqual(t, dir, moved)

	alice, err := os.ReadFile(filepath.Join(moved, "rows"))
	require.NoError(t, err)
	require.Equal(t, "alice", string(alice), "the move restored what was there")

	require.NoError(t, os.WriteFile(filepath.Join(moved, "rows"), []byte("alice\nbob"), 0o640))
	require.Equal(t, 1, arrived.CheckpointAll(t.Context()))

	// AND THE NODE IT LEFT KEEPS TICKING. Its directory still holds the row from
	// before the move, and its interval comes round like any other.
	require.Equal(t, 1, left.Checkpoint(t.Context(), time.Now().Add(time.Hour)),
		"the node it left still walks the volume")

	// The pointer must still name what the workload actually wrote.
	back, err := node().Mount(t.Context(), grant)
	require.NoError(t, err)

	rows, err := os.ReadFile(filepath.Join(back, "rows"))
	require.NoError(t, err)
	require.Equal(t, "alice\nbob", string(rows),
		"the node the workload left put its own older bytes back over the pointer")
}

// ---------------------------------------------------------------- a restore

// TestARESTOREIsSaid, because a directory that arrived from somewhere else is the
// one thing an operator reading a log about a moved workload is looking for.
func TestARestoreSaysWhereItCameFrom(t *testing.T) {
	v, store, index, log := holding(t)
	handle := "shop/prod/QmData"

	id := store.put(t, tarOf(t, map[string]string{"PG_VERSION": "16"}))
	require.NoError(t, index.Put(t.Context(), recordedAt+handle, []byte(id)))

	dir, err := v.Mount(t.Context(), mountGrant(handle, nil))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(dir, "PG_VERSION"))
	require.Equal(t, []string{"volume: restored " + handle + " from " + id}, log.told())
}

// TestARESTOREThatCannotBeTrustedRefusesTheMount. Every one of these is a
// workload about to be told it has a database it does not have, so the mount
// fails rather than answering an empty directory.
func TestARestoreThatCannotBeCompletedRefusesTheMount(t *testing.T) {
	handle := "shop/prod/QmData"

	for _, c := range []struct {
		name  string
		setup func(t *testing.T, store *blobs, index *rows) string
		says  string
	}{
		{
			name: "the object is not an archive",
			setup: func(t *testing.T, store *blobs, index *rows) string {
				return store.put(t, []byte("this is not a tar"))
			},
			says: "restoring " + handle,
		},
		{
			name: "the store lost the object",
			setup: func(t *testing.T, store *blobs, index *rows) string {
				id := store.put(t, tarOf(t, map[string]string{"rows": "alice"}))
				store.lose()
				return id
			},
			says: "reading the checkpoint for " + handle,
		},
		{
			name: "the archive stops halfway through an entry",
			setup: func(t *testing.T, store *blobs, index *rows) string {
				// A header that says 64 bytes and a stream that stops after 32,
				// which is what a half-written object looks like from here. A
				// restore that stopped there is a database with some of its
				// files.
				var buf bytes.Buffer
				archive := tar.NewWriter(&buf)
				require.NoError(t, archive.WriteHeader(&tar.Header{
					Name: "PG_VERSION", Typeflag: tar.TypeReg, Mode: 0o640, Size: 64,
				}))
				_, err := archive.Write(make([]byte, 64))
				require.NoError(t, err)
				require.NoError(t, archive.Close())
				return store.put(t, buf.Bytes()[:512+32])
			},
			says: "restoring " + handle,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			v, store, index, _ := holding(t)
			id := c.setup(t, store, index)
			require.NoError(t, index.Put(t.Context(), recordedAt+handle, []byte(id)))

			_, err := v.Mount(t.Context(), mountGrant(handle, nil))
			require.ErrorContains(t, err, c.says)
		})
	}
}

// TestARESTOREThatFailsLeavesNothingALaterMountTakes. The directory existing is
// what tells a mount the bytes are there, so a restore that failed or was
// refused leaves none: the next mount would otherwise hand the workload what
// the first one stopped halfway through, as though it were the checkpoint.
func TestARestoreThatFailsLeavesNothingALaterMountTakes(t *testing.T) {
	handle := "shop/prod/QmData"

	for _, c := range []struct {
		name string
		node func(t *testing.T, root string) *volume.Volumes
	}{
		{
			name: "the archive stops halfway",
			node: func(t *testing.T, root string) *volume.Volumes {
				whole := tarOf(t, map[string]string{"PG_VERSION": "16"})
				var buf bytes.Buffer
				archive := tar.NewWriter(&buf)
				require.NoError(t, archive.WriteHeader(&tar.Header{Name: "base", Typeflag: tar.TypeDir, Mode: 0o750}))
				require.NoError(t, archive.WriteHeader(&tar.Header{Name: "base/rows", Typeflag: tar.TypeReg, Mode: 0o640, Size: int64(len(whole))}))
				_, err := archive.Write(whole)
				require.NoError(t, err)
				require.NoError(t, archive.Close())
				return node(t, root, buf.Bytes()[:3*512])
			},
		},
		{
			name: "a link in it leads out",
			node: func(t *testing.T, root string) *volume.Volumes {
				return node(t, root, entries(t,
					tar.Header{Name: "PG_VERSION", Typeflag: tar.TypeReg, Mode: 0o640},
					linkEntry("x", "."), linkEntry("x/y", ".."),
				))
			},
		},
		{
			name: "there is no store to read it from",
			node: func(t *testing.T, root string) *volume.Volumes {
				index := newRows()
				require.NoError(t, index.Put(t.Context(), recordedAt+handle, []byte("QmWhatever")))
				v, err := volume.New(root, nil, &opening{rows: index}, &said{})
				require.NoError(t, err)
				return v
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			v := c.node(t, root)

			_, err := v.Mount(t.Context(), mountGrant(handle, nil))
			require.Error(t, err)
			_, err = v.Mount(t.Context(), mountGrant(handle, nil))
			require.Error(t, err, "a later mount took what the failed restore left")

			left, err := os.ReadDir(filepath.Join(root, "volumes"))
			if !errors.Is(err, os.ErrNotExist) {
				require.NoError(t, err)
			}
			require.Empty(t, left, "the failed restore left something behind")
		})
	}
}

// node is a fresh backing over root whose store holds archive as the
// volume's last checkpoint.
func node(t *testing.T, root string, archive []byte) *volume.Volumes {
	t.Helper()

	store, index := newBlobs(), newRows()
	id := store.put(t, archive)
	require.NoError(t, index.Put(t.Context(), recordedAt+"shop/prod/QmData", []byte(id)))

	v, err := volume.New(root, store, &opening{rows: index}, &said{})
	require.NoError(t, err)
	t.Cleanup(func() { v.Close() })
	return v
}

// TestARESTORELeftUnfinishedIsClearedByTheNext. A node that stops midway leaves
// its restore where it was being unpacked, beside the volume's directory, and
// possibly with directories in it already closed.
func TestARestoreLeftUnfinishedIsClearedByTheNext(t *testing.T) {
	root := t.TempDir()
	unfinished := filepath.Join(root, "volumes", "shop%2Fprod%2FQmData%restoring")
	require.NoError(t, os.MkdirAll(filepath.Join(unfinished, "base", "1"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(unfinished, "base", "1", "rows"), []byte("half"), 0o640))
	require.NoError(t, os.Chmod(filepath.Join(unfinished, "base"), 0o100))
	t.Cleanup(func() { os.Chmod(filepath.Join(unfinished, "base"), 0o750) })

	dir, err := restoring(t, root, tarOf(t, map[string]string{"PG_VERSION": "16"}))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(dir, "PG_VERSION"))

	_, err = os.Lstat(unfinished)
	require.ErrorIs(t, err, os.ErrNotExist, "the unfinished restore is still there")
}

// TestARECORDEDCheckpointWithNoStoreIsRefused. Having recorded one and having
// nowhere to read it from is not the same as never having had one — the first is
// a node missing its store, and answering an empty directory would hand a
// workload an empty database.
func TestARecordedCheckpointWithNoStoreRefusesTheMount(t *testing.T) {
	index := newRows()
	handle := "shop/prod/QmData"
	require.NoError(t, index.Put(t.Context(), recordedAt+handle, []byte("QmWhatever")))

	v, err := volume.New(t.TempDir(), nil, &opening{rows: index}, &said{})
	require.NoError(t, err)

	_, err = v.Mount(t.Context(), mountGrant(handle, nil))
	require.ErrorIs(t, err, volume.ErrNoStore)
}

// TestAROWThatCannotBeREADIsReadAsNoCheckpoint, WHICH IS A PROPERTY THIS PACKAGE
// DOES NOT HOLD and is written down rather than fixed.
//
// A store that cannot be read is indistinguishable here from one that never had
// a pointer, so the mount succeeds with an empty directory — the exact shape the
// rest of this package exists to prevent. What the fix costs is a mount that
// fails on a transient read of a replicated row, and which of the two is worse is
// the operator's call rather than this file's.
func TestAPointerThatCannotBeReadIsReadAsNoCheckpoint(t *testing.T) {
	index := newRows()
	index.getErr = errors.New("the row could not be read")

	v, err := volume.New(t.TempDir(), newBlobs(), &opening{rows: index}, &said{})
	require.NoError(t, err)

	dir, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err, "this is the gap, not a fix")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

// TestANEMPTYPointerIsNoPointer. A row somebody wrote nothing into names no
// object, and reading it as one would ask the store for "".
func TestAnEmptyPointerIsNoCheckpoint(t *testing.T) {
	v, store, index, _ := holding(t)
	handle := "shop/prod/QmData"
	require.NoError(t, index.Put(t.Context(), recordedAt+handle, nil))

	_, err := v.Mount(t.Context(), mountGrant(handle, nil))
	require.NoError(t, err)

	gets, _ := store.counts()
	require.Zero(t, gets, "the store was asked for an object nobody named")
}

// ---------------------------------------------------------------- one opener

// TestONEOPENERPerHandle. Two cold starts of one workload race here, and the
// loser used to get the directory back while the winner was still unpacking into
// it — a container mounting a half-restored database.
func TestASecondColdStartWaitsForTheRestore(t *testing.T) {
	v, store, index, _ := holding(t)
	handle := "shop/prod/QmData"

	id := store.put(t, tarOf(t, map[string]string{"PG_VERSION": "16"}))
	require.NoError(t, index.Put(t.Context(), recordedAt+handle, []byte(id)))

	// THE RESTORE IS HELD OPEN, so the second mount arrives while the first is
	// still unpacking.
	gate := make(chan struct{})
	store.mu.Lock()
	store.gate = gate
	store.mu.Unlock()

	type mounted struct {
		dir string
		err error
	}
	first := make(chan mounted, 1)
	go func() {
		dir, err := v.Mount(context.Background(), mountGrant(handle, nil))
		first <- mounted{dir, err}
	}()

	require.Eventually(t, func() bool {
		gets, _ := store.counts()
		return gets == 1
	}, 5*time.Second, time.Millisecond, "the first mount never reached the store")

	second := make(chan mounted, 1)
	go func() {
		dir, err := v.Mount(context.Background(), mountGrant(handle, nil))
		second <- mounted{dir, err}
	}()

	// AND IT IS GIVEN A MOMENT TO PARK, because a goroutine blocked on a channel
	// is not observable from outside it. If this is ever too short the second
	// mount takes the already-held path instead — the same answer by the other
	// route, so a slow machine costs this test a case rather than failing it.
	time.Sleep(25 * time.Millisecond)

	// AND A CALLER THAT GIVES UP WAITING SAYS SO, rather than taking the
	// directory while it is half unpacked.
	ctx, stop := context.WithCancel(context.Background())
	gave := make(chan mounted, 1)
	go func() {
		dir, err := v.Mount(ctx, mountGrant(handle, nil))
		gave <- mounted{dir, err}
	}()
	stop()
	impatient := <-gave
	require.ErrorIs(t, impatient.err, context.Canceled)

	close(gate)

	won, waited := <-first, <-second
	require.NoError(t, won.err)
	require.NoError(t, waited.err)
	require.Equal(t, won.dir, waited.dir)

	gets, _ := store.counts()
	require.Equal(t, 1, gets, "the second mount unpacked the checkpoint again")

	version, err := os.ReadFile(filepath.Join(won.dir, "PG_VERSION"))
	require.NoError(t, err)
	require.Equal(t, "16", string(version))
}

// ---------------------------------------------------------------- the loop

// TestTheLoopCheckpointsOnItsTick, which is what makes a checkpoint something an
// author declares rather than something somebody remembers to ask for.
func TestTheLoopCheckpointsOnItsTick(t *testing.T) {
	v, _, index, _ := holding(t)
	handle := "shop/prod/QmData"

	dir, err := v.Mount(t.Context(), mountGrant(handle, nil))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows"), []byte("alice"), 0o640))

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		v.Run(ctx, time.Millisecond)
	}()

	require.Eventually(t, func() bool {
		_, held := index.at(recordedAt + handle)
		return held
	}, 5*time.Second, time.Millisecond, "the loop never checkpointed")

	stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not finish")
	}
}

// TestTheLoopWritesOnTheWayOut. A planned shutdown is the one moment the interval
// is wrong: whatever was written since the last tick is inside the budget the
// author declared, and throwing it away turns a clean restart into the loss a
// crash was supposed to bound.
func TestTheLoopWritesOnTheWayOut(t *testing.T) {
	v, _, index, _ := holding(t)
	handle := "shop/prod/QmData"

	dir, err := v.Mount(t.Context(), mountGrant(handle, map[string]string{"checkpoint/interval": "1h0m0s"}))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "PG_VERSION"), []byte("16"), 0o640))

	// An hour's interval and a tick that will not fire, so anything recorded
	// below was written by the way out and not by an ordinary pass.
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		v.Run(ctx, time.Hour)
	}()
	stop()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not finish")
	}

	_, held := index.at(recordedAt + handle)
	require.True(t, held, "the final checkpoint was not written")
}

// ---------------------------------------------------------------- closing

// TestClosingStopsHoldingVolumesAndKeepsTheDirectories. The bytes are what a
// restart finds; re-reading them from the store would be slower and older.
func TestClosingStopsHoldingVolumesAndKeepsTheDirectories(t *testing.T) {
	v, _, _, _ := holding(t)
	grant := mountGrant("shop/prod/QmData", nil)

	dir, err := v.Mount(t.Context(), grant)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "PG_VERSION"), []byte("16"), 0o640))

	require.NoError(t, v.Close())
	require.Equal(t, 0, v.CheckpointAll(t.Context()), "a closed backing still holds volumes")

	info, err := os.Stat(filepath.Join(dir, "PG_VERSION"))
	require.NoError(t, err)
	require.Equal(t, int64(2), info.Size())

	// AND MOUNTING AGAIN FINDS THEM, without a restore: the directory is already
	// there, and an existing one is never unpacked over.
	again, err := v.Mount(t.Context(), grant)
	require.NoError(t, err)
	require.Equal(t, dir, again)
}

// ---------------------------------------------------------------- the archive

// TestWHATACHECKPOINTHOLDS: every file, every empty directory, and a symlink as
// a LINK rather than a copy of whatever it pointed at — following one would pull
// the other end into the tenant's checkpoint.
func TestACheckpointHoldsWhatTheDirectoryHeld(t *testing.T) {
	v, store, _, _ := holding(t)

	dir, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "real"), []byte("bytes"), 0o640))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "pg_wal"), 0o750))
	require.NoError(t, os.Symlink("real", filepath.Join(dir, "link")))

	require.Equal(t, 1, v.CheckpointAll(t.Context()))

	store.mu.Lock()
	require.Len(t, store.held, 1)
	var archive []byte
	for _, body := range store.held {
		archive = body
	}
	store.mu.Unlock()

	held := headers(t, archive)
	require.Len(t, held, 3)
	require.Equal(t, byte(tar.TypeReg), held["real"].Typeflag)
	require.Equal(t, byte(tar.TypeDir), held["pg_wal"].Typeflag)
	require.Equal(t, byte(tar.TypeSymlink), held["link"].Typeflag)
	require.Equal(t, "real", held["link"].Linkname, "the link was followed")
}

// pausing is a store that stops reading a checkpoint after so many bytes, runs
// meanwhile, then reads the rest. The walk writes into a pipe, so it is held
// exactly there.
type pausing struct {
	*blobs
	after     int
	meanwhile func()
}

func (p *pausing) Put(ctx context.Context, content io.Reader) (string, error) {
	head := make([]byte, p.after)
	if _, err := io.ReadFull(content, head); err != nil {
		return "", err
	}
	p.meanwhile()
	return p.blobs.Put(ctx, io.MultiReader(bytes.NewReader(head), content))
}

// TestACHECKPOINTHoldsWhatItOpened. The walk runs while the workload writes, so
// a name can change between being listed and being read: here it changes while
// its header is being written. Nothing from outside the volume may reach the
// checkpoint either way.
func TestACheckpointHoldsOnlyWhatItOpened(t *testing.T) {
	for _, c := range []struct {
		name    string
		prepare func(t *testing.T, dir, outside string)
		swap    func(t *testing.T, dir, outside string)
		kept    bool
	}{
		{
			name: "a file that becomes a link",
			prepare: func(t *testing.T, dir, outside string) {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "rows"), []byte("volume-data"), 0o640))
				require.NoError(t, os.WriteFile(filepath.Join(outside, "rows"), []byte("host-secret"), 0o640))
			},
			swap: func(t *testing.T, dir, outside string) {
				require.NoError(t, os.Symlink(filepath.Join(outside, "rows"), filepath.Join(dir, "rows.new")))
				require.NoError(t, os.Rename(filepath.Join(dir, "rows.new"), filepath.Join(dir, "rows")))
			},
			kept: true,
		},
		{
			name: "a directory that becomes a link",
			prepare: func(t *testing.T, dir, outside string) {
				require.NoError(t, os.MkdirAll(filepath.Join(dir, "base"), 0o750))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "base", "rows"), []byte("volume-data"), 0o640))
				require.NoError(t, os.WriteFile(filepath.Join(outside, "rows"), []byte("host-secret"), 0o640))
			},
			swap: func(t *testing.T, dir, outside string) {
				require.NoError(t, os.Rename(filepath.Join(dir, "base"), filepath.Join(dir, "moved")))
				require.NoError(t, os.Symlink(outside, filepath.Join(dir, "base")))
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			outside := t.TempDir()
			store := newBlobs()
			paused := &pausing{blobs: store, after: 511}
			v, err := volume.New(t.TempDir(), paused, &opening{rows: newRows()}, &said{})
			require.NoError(t, err)

			dir, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
			require.NoError(t, err)
			c.prepare(t, dir, outside)

			// ONE BYTE SHORT OF THE FIRST HEADER: the walk has looked at the
			// first entry and is still writing what it found.
			paused.meanwhile = func() { c.swap(t, dir, outside) }
			written := v.CheckpointAll(t.Context())

			store.mu.Lock()
			defer store.mu.Unlock()
			for _, archive := range store.held {
				require.False(t, bytes.Contains(archive, []byte("host-secret")), "a checkpoint holds bytes from outside the volume")
			}
			if c.kept {
				require.Equal(t, 1, written)
				require.Len(t, store.held, 1)
				for _, archive := range store.held {
					require.True(t, bytes.Contains(archive, []byte("volume-data")), "the checkpoint lost the file it opened")
				}
			}
		})
	}
}

// TestARESTOREPutsBackWhatAWalkTook, which is the round trip the two halves are
// only useful as: a file, an empty directory a workload expects to find, and a
// link that is still a link.
func TestARestorePutsBackWhatTheWalkTook(t *testing.T) {
	store, index := newBlobs(), newRows()
	grant := mountGrant("shop/prod/QmData", nil)
	node := func() *volume.Volumes {
		v, err := volume.New(t.TempDir(), store, &opening{rows: index}, &said{})
		require.NoError(t, err)
		return v
	}

	source := node()
	dir, err := source.Mount(t.Context(), grant)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "PG_VERSION"), []byte("16"), 0o640))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "pg_wal"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "conf"), 0o750))
	require.NoError(t, os.Symlink("../PG_VERSION", filepath.Join(dir, "conf", "current")))
	require.Equal(t, 1, source.CheckpointAll(t.Context()))

	restored, err := node().Mount(t.Context(), grant)
	require.NoError(t, err)

	info, err := os.Stat(filepath.Join(restored, "pg_wal"))
	require.NoError(t, err)
	require.True(t, info.IsDir(), "an empty directory came back as one")

	link, err := os.Lstat(filepath.Join(restored, "conf", "current"))
	require.NoError(t, err)
	require.NotZero(t, link.Mode()&os.ModeSymlink, "the link came back as a copy")

	target, err := os.Readlink(filepath.Join(restored, "conf", "current"))
	require.NoError(t, err)
	require.Equal(t, "../PG_VERSION", target)
}

// TestWHATACHECKPOINTMUSTNOTBEAbleToDo.
//
// A tar is bytes from a store and this unpacks it with the node's own
// privileges, so every entry is checked against the destination — the oldest
// archive bug there is. It REFUSES rather than clamping: joining a cleaned
// "/"+name onto the destination does keep the write inside, and it silently
// rewrites `../etc/passwd` into `etc/passwd`, so an archive that tried to escape
// unpacks somewhere else and nothing says it tried.
func TestACheckpointCannotWriteOutsideTheVolume(t *testing.T) {
	handle := "shop/prod/QmData"

	for _, c := range []struct {
		name    string
		archive func(t *testing.T) []byte
		says    string
	}{
		{
			name:    "a name that climbs out",
			archive: func(t *testing.T) []byte { return tarOf(t, map[string]string{"../../stolen": "x"}) },
			says:    "outside the volume",
		},
		{
			name:    "an absolute name",
			archive: func(t *testing.T) []byte { return tarOf(t, map[string]string{"/etc/stolen": "x"}) },
			says:    "outside the volume",
		},
		{
			name:    "a name that climbs out after going in",
			archive: func(t *testing.T) []byte { return tarOf(t, map[string]string{"a/../../../stolen": "x"}) },
			says:    "outside the volume",
		},
		{
			name:    "a parent named on its own",
			archive: func(t *testing.T) []byte { return tarOf(t, map[string]string{"..": "x"}) },
			says:    "outside the volume",
		},
		{
			name: "a link pointing out",
			archive: func(t *testing.T) []byte {
				return link(t, "escape", "../../../etc")
			},
			says: "outside the volume",
		},
		{
			// AN ABSOLUTE TARGET IS CHECKED FIRST, because joining it onto the
			// entry's directory discards the leading separator and turns
			// `/etc/passwd` into a path inside the volume that passes — while
			// the link written on disk still points at /etc/passwd.
			name: "a link pointing at an absolute path",
			archive: func(t *testing.T) []byte {
				return link(t, "conf/shadow", "/etc/passwd")
			},
			says: "outside the volume",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			v, store, index, _ := holding(t)
			id := store.put(t, c.archive(t))
			require.NoError(t, index.Put(t.Context(), recordedAt+handle, []byte(id)))

			_, err := v.Mount(t.Context(), mountGrant(handle, nil))
			require.Error(t, err, "the archive was accepted")
			require.ErrorContains(t, err, c.says)
		})
	}
}

// TestNOTHINGIsWrittenOutsideTheVolumeEitherWay: the refusal is checked, and so
// is the filesystem it was protecting.
func TestAnEscapingEntryWritesNothing(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.MkdirAll(outside, 0o750))

	store, index := newBlobs(), newRows()
	handle := "shop/prod/QmData"
	id := store.put(t, tarOf(t, map[string]string{"../../outside/stolen": "x"}))
	require.NoError(t, index.Put(t.Context(), recordedAt+handle, []byte(id)))

	v, err := volume.New(filepath.Join(root, "node"), store, &opening{rows: index}, &said{})
	require.NoError(t, err)
	_, err = v.Mount(t.Context(), mountGrant(handle, nil))
	require.ErrorContains(t, err, "outside the volume")

	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	require.Empty(t, entries, "something was written outside the volume")
}

// TestNOENTRYLandsOutsideTheVolumeWhateverLinksPrecedeIt. Every name is checked
// against the destination, and the links an archive has already made sit in the
// path of every entry after them: neither may carry an entry out.
func TestNoEntryLandsOutsideTheVolumeWhateverLinksPrecedeIt(t *testing.T) {
	for _, last := range []struct {
		name  string
		entry tar.Header
	}{
		{"a file", tar.Header{Name: "y/escaped", Typeflag: tar.TypeReg, Mode: 0o640}},
		{"a directory", tar.Header{Name: "y/escaped", Typeflag: tar.TypeDir, Mode: 0o750}},
		{"a link", linkEntry("y/escaped", "anything")},
	} {
		t.Run(last.name, func(t *testing.T) {
			root := t.TempDir()
			_, err := restoring(t, root, entries(t, linkEntry("x", "."), linkEntry("x/y", ".."), last.entry))
			require.Error(t, err, "the archive was accepted")

			beside, err := os.ReadDir(filepath.Join(root, "volumes"))
			require.NoError(t, err)
			require.Empty(t, beside, "something was left beside the volume")
		})
	}
}

// TestNOTHINGIsWrittenThroughALinkThatLeadsOut, onto a file beside the volume.
func TestNothingIsWrittenThroughALinkThatLeadsOut(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(root, "volumes", "victim")
	require.NoError(t, os.MkdirAll(filepath.Dir(victim), 0o750))
	require.NoError(t, os.WriteFile(victim, []byte("untouched"), 0o640))

	_, err := restoring(t, root, entries(t,
		linkEntry("x", "."),
		linkEntry("x/y", "../victim"),
		tar.Header{Name: "y", Typeflag: tar.TypeReg, Mode: 0o640},
	))
	require.Error(t, err, "the archive was accepted")

	content, err := os.ReadFile(victim)
	require.NoError(t, err)
	require.Equal(t, "untouched", string(content), "a file beside the volume was written")
}

// TestALINKIsJudgedFromWhereItLands, once every entry is on disk, and not from
// the name the archive gave it.
func TestALinkThatLeadsOutFromWhereItLandsIsRefused(t *testing.T) {
	for _, c := range []struct {
		name  string
		links []tar.Header
	}{
		{"under a link to the volume", []tar.Header{linkEntry("x", "."), linkEntry("x/y", "..")}},
		{"through a link made after it", []tar.Header{linkEntry("l", "x/.."), linkEntry("x", ".")}},
		{"in a loop", []tar.Header{linkEntry("a", "b"), linkEntry("b", "a")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := restoring(t, t.TempDir(), entries(t, c.links...))
			require.ErrorContains(t, err, "does not resolve inside the volume")
		})
	}
}

// TestALINKThatLeadsNowhereIsKept: to a name that is not there, or through a
// file. Neither leads anywhere, let alone out.
func TestALinkThatLeadsNowhereIsKept(t *testing.T) {
	links := map[string]string{"missing": "not-yet", "through": "PG_VERSION/child"}
	headers := []tar.Header{{Name: "PG_VERSION", Typeflag: tar.TypeReg, Mode: 0o640}}
	for name, target := range links {
		headers = append(headers, linkEntry(name, target))
	}

	dir, err := restoring(t, t.TempDir(), entries(t, headers...))
	require.NoError(t, err)

	for name, want := range links {
		target, err := os.Readlink(filepath.Join(dir, name))
		require.NoError(t, err, name)
		require.Equal(t, want, target, name)
	}
}

// TestADIRECTORYItsOwnerCannotListOrWriteIsStillRestored, with what is inside
// it, and ends with the mode its entry carried.
func TestADirectoryItsOwnerCannotListOrWriteIsStillRestored(t *testing.T) {
	for _, mode := range []int64{0o300, 0o500, 0o100} {
		t.Run(strconv.FormatInt(mode, 8), func(t *testing.T) {
			dir, err := restoring(t, t.TempDir(), entries(t,
				tar.Header{Name: "drop", Typeflag: tar.TypeDir, Mode: mode},
				tar.Header{Name: "drop/inner", Typeflag: tar.TypeDir, Mode: mode},
				linkEntry("drop/inner/current", "../../PG_VERSION"),
				tar.Header{Name: "drop/inner/rows", Typeflag: tar.TypeReg, Mode: 0o640},
				tar.Header{Name: "PG_VERSION", Typeflag: tar.TypeReg, Mode: 0o640},
			))
			require.NoError(t, err)

			for _, name := range []string{"drop", "drop/inner"} {
				info, err := os.Stat(filepath.Join(dir, name))
				require.NoError(t, err, name)
				require.Equal(t, os.FileMode(mode), info.Mode().Perm()&0o700, "%s does not have the owner's rights its entry gave", name)
			}

			// Opened back up to look inside, and so the test can clean up.
			require.NoError(t, os.Chmod(filepath.Join(dir, "drop"), 0o750))
			require.NoError(t, os.Chmod(filepath.Join(dir, "drop", "inner"), 0o750))

			target, err := os.Readlink(filepath.Join(dir, "drop", "inner", "current"))
			require.NoError(t, err)
			require.Equal(t, "../../PG_VERSION", target)
			require.FileExists(t, filepath.Join(dir, "drop", "inner", "rows"))
		})
	}
}

// TestADIRECTORYIsClosedWhereItsNameLeadsOnDisk, deepest there first. A name's
// own depth says nothing once a link lies along it.
func TestADirectoryIsClosedWhereItsNameLeadsOnDisk(t *testing.T) {
	t.Run("deeper on disk than its name", func(t *testing.T) {
		dir, err := restoring(t, t.TempDir(), entries(t,
			tar.Header{Name: "a/b/c", Typeflag: tar.TypeDir, Mode: 0o600},
			linkEntry("l", "a/b/c"),
			tar.Header{Name: "l/d", Typeflag: tar.TypeDir, Mode: 0o600},
		))
		require.NoError(t, err)

		closed := filepath.Join(dir, "a", "b", "c")
		t.Cleanup(func() {
			os.Chmod(closed, 0o750)
			os.Chmod(filepath.Join(closed, "d"), 0o750)
		})
		info, err := os.Stat(closed)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm()&0o700)

		// Opened back up to look inside.
		require.NoError(t, os.Chmod(closed, 0o700))
		info, err = os.Stat(filepath.Join(closed, "d"))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm()&0o700)
	})

	t.Run("the volume itself, through a link", func(t *testing.T) {
		dir, err := restoring(t, t.TempDir(), entries(t,
			linkEntry("x", "."),
			tar.Header{Name: "x", Typeflag: tar.TypeDir, Mode: 0o500},
		))
		require.NoError(t, err)
		t.Cleanup(func() { os.Chmod(dir, 0o750) })

		info, err := os.Stat(dir)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o700), info.Mode().Perm()&0o700, "the volume itself was closed")
	})
}

// TestSETUIDSetgidAndStickyDoNotComeBack. Only the permission bits of a mode
// are restored.
func TestSetuidSetgidAndStickyDoNotComeBack(t *testing.T) {
	dir, err := restoring(t, t.TempDir(), entries(t,
		tar.Header{Name: "tools", Typeflag: tar.TypeDir, Mode: 0o750 | 0o7000},
		tar.Header{Name: "tools/su", Typeflag: tar.TypeReg, Mode: 0o750 | 0o7000},
	))
	require.NoError(t, err)

	for _, name := range []string{"tools", "tools/su"} {
		info, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err, name)
		require.Zero(t, info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky), "%s kept a special bit", name)
	}
}

// restoring mounts a volume on a fresh node over root whose store holds archive
// as the volume's last checkpoint.
func restoring(t *testing.T, root string, archive []byte) (string, error) {
	t.Helper()

	handle := "shop/prod/QmData"
	store, index := newBlobs(), newRows()
	id := store.put(t, archive)
	require.NoError(t, index.Put(t.Context(), recordedAt+handle, []byte(id)))

	v, err := volume.New(root, store, &opening{rows: index}, &said{})
	require.NoError(t, err)
	t.Cleanup(func() { v.Close() })
	return v.Mount(t.Context(), mountGrant(handle, nil))
}

// entries is an archive of the given headers, each regular file holding its
// own name.
func entries(t *testing.T, headers ...tar.Header) []byte {
	t.Helper()

	var buf bytes.Buffer
	archive := tar.NewWriter(&buf)
	for _, header := range headers {
		body := ""
		if header.Typeflag == tar.TypeReg {
			body = header.Name
			header.Size = int64(len(body))
		}
		require.NoError(t, archive.WriteHeader(&header))
		_, err := archive.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, archive.Close())
	return buf.Bytes()
}

func linkEntry(name, target string) tar.Header {
	return tar.Header{Name: name, Typeflag: tar.TypeSymlink, Linkname: target, Mode: 0o777}
}

// TestADEVICEIsNotCreatedFromBytesInAStore. Nothing writes one into a checkpoint,
// and making one is a privilege this has no reason to exercise.
func TestACheckpointDoesNotCreateDevices(t *testing.T) {
	v, store, index, _ := holding(t)
	handle := "shop/prod/QmData"

	var buf bytes.Buffer
	archive := tar.NewWriter(&buf)
	require.NoError(t, archive.WriteHeader(&tar.Header{
		Name: "sda", Typeflag: tar.TypeChar, Mode: 0o666, Devmajor: 8, Devminor: 0,
	}))
	require.NoError(t, archive.WriteHeader(&tar.Header{
		Name: "PG_VERSION", Typeflag: tar.TypeReg, Mode: 0o640, Size: 2,
	}))
	_, err := archive.Write([]byte("16"))
	require.NoError(t, err)
	require.NoError(t, archive.Close())

	id := store.put(t, buf.Bytes())
	require.NoError(t, index.Put(t.Context(), recordedAt+handle, []byte(id)))

	// IT IS SKIPPED AND THE REST OF THE ARCHIVE IS NOT: a device nothing wrote
	// is not a corrupt checkpoint, it is an entry with nothing to do.
	dir, err := v.Mount(t.Context(), mountGrant(handle, nil))
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(dir, "sda"))
	require.Error(t, err, "a device node was created")
	require.FileExists(t, filepath.Join(dir, "PG_VERSION"))
}

// TestAFILEComesBackWithTheModeItWentInWith, which for a database's data
// directory is not cosmetic: postgres refuses to start on a data directory
// anything else can read.
func TestARestoredFileKeepsItsMode(t *testing.T) {
	v, store, index, _ := holding(t)
	handle := "shop/prod/QmData"

	var buf bytes.Buffer
	archive := tar.NewWriter(&buf)
	require.NoError(t, archive.WriteHeader(&tar.Header{
		Name: "secret", Typeflag: tar.TypeReg, Mode: 0o600, Size: 1,
	}))
	_, err := archive.Write([]byte("x"))
	require.NoError(t, err)
	require.NoError(t, archive.Close())

	id := store.put(t, buf.Bytes())
	require.NoError(t, index.Put(t.Context(), recordedAt+handle, []byte(id)))

	dir, err := v.Mount(t.Context(), mountGrant(handle, nil))
	require.NoError(t, err)

	info, err := os.Stat(filepath.Join(dir, "secret"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// TestANARCHIVEWhoseEntriesCollideIsRefused. A checkpoint this node wrote has a
// file or a directory at each name and never both; bytes from a store are not
// that by assumption, and half-unpacking one would mount a directory nobody can
// explain.
func TestAnArchiveThatCannotBeUnpackedIsRefused(t *testing.T) {
	handle := "shop/prod/QmData"

	for _, c := range []struct {
		name    string
		entries []tar.Header
		body    string
	}{
		{
			name: "a directory under a file",
			entries: []tar.Header{
				{Name: "a", Typeflag: tar.TypeReg, Mode: 0o640, Size: 1},
				{Name: "a/b", Typeflag: tar.TypeDir, Mode: 0o750},
			},
			body: "x",
		},
		{
			name: "a link under a file",
			entries: []tar.Header{
				{Name: "a", Typeflag: tar.TypeReg, Mode: 0o640, Size: 1},
				{Name: "a/b", Typeflag: tar.TypeSymlink, Linkname: "elsewhere", Mode: 0o777},
			},
			body: "x",
		},
		{
			name: "a file under a file",
			entries: []tar.Header{
				{Name: "a", Typeflag: tar.TypeReg, Mode: 0o640, Size: 1},
				{Name: "a/b", Typeflag: tar.TypeReg, Mode: 0o640},
			},
			body: "x",
		},
		{
			name: "a file over a directory",
			entries: []tar.Header{
				{Name: "a", Typeflag: tar.TypeDir, Mode: 0o750},
				{Name: "a", Typeflag: tar.TypeReg, Mode: 0o640},
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			archive := tar.NewWriter(&buf)
			for i := range c.entries {
				require.NoError(t, archive.WriteHeader(&c.entries[i]))
				if c.entries[i].Size > 0 {
					_, err := archive.Write([]byte(c.body))
					require.NoError(t, err)
				}
			}
			require.NoError(t, archive.Close())

			v, store, index, _ := holding(t)
			id := store.put(t, buf.Bytes())
			require.NoError(t, index.Put(t.Context(), recordedAt+handle, []byte(id)))

			_, err := v.Mount(t.Context(), mountGrant(handle, nil))
			require.ErrorContains(t, err, "restoring "+handle)
		})
	}
}

// TestAVOLUMEHoldingASocketCannotBeCheckpointed, WHICH IS A PROPERTY THIS
// PACKAGE DOES NOT HOLD and is written down rather than fixed.
//
// archive/tar refuses to make a header for a socket, and the walk asks it about
// every entry — so ONE socket in a data directory fails the whole checkpoint,
// every interval, and the volume stops surviving its node. It is a realistic
// shape: an image whose `unix_socket_directories` is the data directory puts one
// there while the database runs.
//
// What it is NOT is silent: the pass says so with the handle, every interval,
// which is the same loudness the quota gets and for the same reason.
func TestAVolumeHoldingASocketIsNotCheckpointed(t *testing.T) {
	// A socket's path holds 107 bytes, and this test's t.TempDir and the
	// volume's own layout already take 100 of them below TMPDIR.
	root, err := os.MkdirTemp("", "v")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(root) })
	v, _, index, log := holdingAt(t, root)
	handle := "shop/prod/QmData"

	dir, err := v.Mount(t.Context(), mountGrant(handle, nil))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "PG_VERSION"), []byte("16"), 0o640))

	listener, err := net.Listen("unix", filepath.Join(dir, ".s.PGSQL.5432"))
	require.NoError(t, err)
	defer listener.Close()

	require.Equal(t, 0, v.CheckpointAll(t.Context()))
	require.Contains(t, strings.Join(log.warned(), "\n"), handle)

	_, held := index.at(recordedAt + handle)
	require.False(t, held, "a checkpoint that could not be walked was recorded anyway")

	// AND THE SOCKET GOING IS ENOUGH: nothing about the volume is poisoned, so
	// the next pass after whatever put it there stops writes it. Closing the
	// listener unlinks it, which is what a database shutting down does.
	require.NoError(t, listener.Close())
	require.Equal(t, 1, v.CheckpointAll(t.Context()))
}

// TestASTOREThatRefusesBeforeReadingDoesNotWedgeTheWalk. The tar is streamed
// into the store through a pipe, so a store that answers without draining leaves
// a goroutine blocked on a write nobody will read — and that goroutine holds a
// directory handle and the volume's turn in the pass.
func TestAStoreThatRefusesWithoutReadingIsStillAnError(t *testing.T) {
	store, index, log := newBlobs(), newRows(), &said{}
	store.refuseUnread = errors.New("the dag is closed")

	v, err := volume.New(t.TempDir(), store, &opening{rows: index}, log)
	require.NoError(t, err)

	dir, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows"), []byte("alice"), 0o640))

	done := make(chan int, 1)
	go func() { done <- v.CheckpointAll(t.Context()) }()
	select {
	case written := <-done:
		require.Equal(t, 0, written)
	case <-time.After(5 * time.Second):
		t.Fatal("the pass never finished: the walk is blocked writing into a pipe nobody reads")
	}
	require.Contains(t, strings.Join(log.warned(), "\n"), "the dag is closed")
}

// link is an archive holding one symlink.
func link(t *testing.T, name, target string) []byte {
	t.Helper()

	var buf bytes.Buffer
	archive := tar.NewWriter(&buf)
	require.NoError(t, archive.WriteHeader(&tar.Header{
		Name: name, Typeflag: tar.TypeSymlink, Linkname: target, Mode: 0o777,
	}))
	require.NoError(t, archive.Close())
	return buf.Bytes()
}

// TestTHEQUOTAIsTheAuthorsNumberAndNotAParse. `quota` arrives as a string off a
// compiled object, and every backing that reads a count off a grant reads it the
// same way — see invoke.Grant.Number.
func TestTheQuotaIsReadAsANumber(t *testing.T) {
	v, _, _, log := holding(t)

	dir, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData",
		map[string]string{"quota": strconv.Itoa(4)}))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows"), []byte("over"), 0o640))

	// FOUR BYTES IS NOT OVER FOUR BYTES, which is the boundary a ceiling read as
	// a ceiling has.
	require.Equal(t, 1, v.CheckpointAll(t.Context()))
	require.Empty(t, log.warned())

	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows"), []byte("over it"), 0o640))
	require.Equal(t, 1, v.CheckpointAll(t.Context()))
	require.Contains(t, strings.Join(log.warned(), "\n"), "holds 7 bytes and declared a quota of 4")
}
