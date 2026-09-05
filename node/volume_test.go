package node

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
)

// volume_test is about bytes that have to survive two things: the container
// they were written from, and the node it ran on.

func mountGrant(handle string, settings map[string]string) invoke.Grant {
	return invoke.Grant{
		Kind: invoke.MountKind, Label: "pgdata", Handle: handle,
		Ops:      []string{"mount-rw"},
		Settings: settings,
	}
}

// mounting is a node with a root and a store, and the volume backing over it.
func mounting(t *testing.T) (*Node, *volumes) {
	t.Helper()

	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRoot(t.TempDir()), WithAssets(&memAssets{blobs: map[string][]byte{}}))
	require.NoError(t, n.Start(t.Context()))
	<-n.work.forming

	return n, &volumes{node: n}
}

// TestAMountISADirectory, and the directory is under this node's own root
// rather than anywhere a handle could point.
func TestMountingMakesADirectory(t *testing.T) {
	n, v := mounting(t)

	dir, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(dir, n.opts.root), "%s is not under %s", dir, n.opts.root)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

// TestAHANDLEIsONEPathSegment. A handle carries slashes — project, environment,
// id — and a directory per segment would put one project's volumes inside
// another project's name.
func TestAHandleDoesNotBecomeAPathTree(t *testing.T) {
	n, v := mounting(t)

	dir, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(n.opts.root, "volumes"), filepath.Dir(dir))
	require.NotContains(t, filepath.Base(dir), "/")
}

// TestTWODeploymentsAreTWODirectories, which is the same property every other
// handle has.
func TestVolumesAreScopedByHandle(t *testing.T) {
	_, v := mounting(t)

	prod, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.NoError(t, err)
	staging, err := v.Mount(t.Context(), mountGrant("shop/staging/QmData", nil))
	require.NoError(t, err)
	other, err := v.Mount(t.Context(), mountGrant("other/prod/QmData", nil))
	require.NoError(t, err)

	require.NotEqual(t, prod, staging)
	require.NotEqual(t, prod, other)
}

// TestANodeWithNoRootMountsNothing, and says so — a container started without
// the filesystem it declared writes into its own layer.
func TestANodeWithNoRootRefuses(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}})

	_, err := (&volumes{node: n}).Mount(t.Context(), mountGrant("shop/prod/QmData", nil))
	require.ErrorIs(t, err, ErrNoRoot)
	require.Nil(t, n.disksOrNil(), "and the tier is handed nothing rather than something empty")
}

// TestTHEBYTESSurviveTheNode: a checkpoint is written, a second node with the
// same store and a different root restores it.
//
// NEITHER NODE IS SPECIAL. The pointer is a replicated kvdb row rather than a
// raft write, so the node holding the volume records it wherever placement put
// the workload — which is the whole point: a leader-only write meant every
// other node checkpointed nothing, silently.
func TestAVolumeIsRestoredOnAnotherNode(t *testing.T) {
	store := &memAssets{blobs: map[string][]byte{}}
	replicated := &memFactory{}

	first, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRoot(t.TempDir()), WithAssets(store), WithDatabases(replicated))
	require.NoError(t, first.Start(t.Context()))
	<-first.work.forming

	grant := mountGrant("shop/prod/QmData", nil)
	source := &volumes{node: first}
	dir, err := source.Mount(t.Context(), grant)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "PG_VERSION"), []byte("16"), 0o640))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "base", "1"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "base", "1", "rows"), []byte("alice"), 0o640))

	_, err = source.checkpoint(t.Context(), source.open[grant.Handle])
	require.NoError(t, err)

	// A SECOND NODE, sharing the store and the consensus and holding none of
	// the bytes — which is what placement moving a workload looks like.
	second, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRoot(t.TempDir()), WithAssets(store), WithDatabases(replicated))
	require.NoError(t, second.Start(t.Context()))
	<-second.work.forming

	restored, err := (&volumes{node: second}).Mount(t.Context(), grant)
	require.NoError(t, err)
	require.NotEqual(t, dir, restored)

	version, err := os.ReadFile(filepath.Join(restored, "PG_VERSION"))
	require.NoError(t, err)
	require.Equal(t, "16", string(version))

	rows, err := os.ReadFile(filepath.Join(restored, "base", "1", "rows"))
	require.NoError(t, err)
	require.Equal(t, "alice", string(rows), "a nested file came back too")
}

// TestALIVEDirectoryIsNotOVERWRITTENByAnOlderCheckpoint. The checkpoint is
// behind by up to an interval by construction, so restoring over bytes this
// node already holds would lose exactly the writes it exists to bound.
func TestAnExistingDirectoryIsNotRestoredOver(t *testing.T) {
	n, v := mounting(t)
	grant := mountGrant("shop/prod/QmData", nil)

	dir, err := v.Mount(t.Context(), grant)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows"), []byte("old"), 0o640))
	_, err = v.checkpoint(t.Context(), v.open[grant.Handle])
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows"), []byte("new"), 0o640))

	// A fresh backing over the same node: the directory is already there.
	again, err := (&volumes{node: n}).Mount(t.Context(), grant)
	require.NoError(t, err)
	require.Equal(t, dir, again)

	rows, err := os.ReadFile(filepath.Join(dir, "rows"))
	require.NoError(t, err)
	require.Equal(t, "new", string(rows), "the checkpoint overwrote live data")
}

// TestTheCheckpointRecordsWHERE, in a replicated row any node may write.
func TestACheckpointIsRecorded(t *testing.T) {
	_, v := mounting(t)
	ctx := t.Context()
	grant := mountGrant("shop/prod/QmData", nil)

	_, err := v.Mount(ctx, grant)
	require.NoError(t, err)
	_, err = v.checkpoint(ctx, v.open[grant.Handle])
	require.NoError(t, err)

	db, err := v.store()
	require.NoError(t, err)
	recorded, err := db.Get(ctx, checkpointPrefix+grant.Handle)
	require.NoError(t, err)
	require.NotEmpty(t, recorded)
}

// TestTHEDECLAREDIntervalReachesTheVolume, which it did not: the composer never
// carried `checkpoint/interval`, so a volume asking to be checkpointed every
// ten seconds was checkpointed every minute and a restore lost fifty seconds
// more than its author budgeted for.
func TestTheDeclaredIntervalIsUsed(t *testing.T) {
	_, v := mounting(t)

	_, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData",
		map[string]string{settingCheckpointFreq: "10s"}))
	require.NoError(t, err)
	require.Equal(t, 10*time.Second, v.open["shop/prod/QmData"].every)

	// And a volume that declared none takes the vocabulary's own default.
	_, err = v.Mount(t.Context(), mountGrant("shop/prod/QmOther", nil))
	require.NoError(t, err)
	require.Equal(t, defaultCheckpoint, v.open["shop/prod/QmOther"].every)
}

// TestONLYWhatIsDueIsWalked, so a volume asking for a checkpoint an hour apart
// is not tarred every five seconds.
func TestOnlyDueVolumesAreCheckpointed(t *testing.T) {
	_, v := mounting(t)

	_, err := v.Mount(t.Context(), mountGrant("shop/prod/QmData",
		map[string]string{settingCheckpointFreq: "1h0m0s"}))
	require.NoError(t, err)

	now := time.Now()
	last := map[string]time.Time{}
	require.Equal(t, 1, v.step(t.Context(), now, last), "the first pass writes one")
	require.Equal(t, 0, v.step(t.Context(), now.Add(time.Minute), last), "and an hour has not passed")
	require.Equal(t, 1, v.step(t.Context(), now.Add(2*time.Hour), last))
}

// TestTHEQUOTAIsMeasuredAndSaid. It is not a wall — see volume.go for why — and
// what it must not be is silent: what the walk found is answered, so the
// measurement is a value rather than a log line somebody hopes was printed.
func TestGoingOverTheQuotaIsMeasured(t *testing.T) {
	_, v := mounting(t)
	grant := mountGrant("shop/prod/QmData", map[string]string{settingVolumeQuota: "8"})

	dir, err := v.Mount(t.Context(), grant)
	require.NoError(t, err)
	require.EqualValues(t, 8, v.open[grant.Handle].quota, "the author's number reached the volume")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows"), bytes.Repeat([]byte("x"), 64), 0o640))

	size, err := v.checkpoint(t.Context(), v.open[grant.Handle])
	require.NoError(t, err)
	require.EqualValues(t, 64, size, "the walk measured what is there")
	require.Greater(t, size, v.open[grant.Handle].quota, "and it is over the declared ceiling")
}

// ── what a checkpoint must not be able to do ─────────────────────────────────

// TestATARENTRYCannotWriteOutsideTheVolume, which is the oldest archive bug
// there is — and this one unpacks with the node's own privileges.
func TestACheckpointCannotEscapeItsDirectory(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside")
	require.NoError(t, os.MkdirAll(outside, 0o750))

	into := filepath.Join(dir, "volume")
	require.NoError(t, os.MkdirAll(into, 0o750))

	for _, name := range []string{"../outside/stolen", "/etc/stolen", "a/../../outside/stolen"} {
		err := untar(tarOf(t, map[string]string{name: "x"}), into)
		require.Error(t, err, "%q was accepted", name)
		require.ErrorContains(t, err, "outside the volume")
	}

	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	require.Empty(t, entries, "something was written outside the volume")
}

// TestASYMLINKIsCheckedWhereItPoints, not only where it is written: a relative
// target climbing out is the same escape, and it is followed by whatever mounts
// this next.
func TestACheckpointSymlinkCannotPointOutside(t *testing.T) {
	dir := t.TempDir()
	into := filepath.Join(dir, "volume")
	require.NoError(t, os.MkdirAll(into, 0o750))

	var buf bytes.Buffer
	archive := tar.NewWriter(&buf)
	require.NoError(t, archive.WriteHeader(&tar.Header{
		Name: "escape", Typeflag: tar.TypeSymlink, Linkname: "../../../etc", Mode: 0o777,
	}))
	require.NoError(t, archive.Close())

	err := untar(&buf, into)
	require.Error(t, err)
	require.ErrorContains(t, err, "outside the volume")
}

// TestADEVICEIsNotCreatedFromBytesInAStore. Nothing writes one into a
// checkpoint, and making one is a privilege this has no reason to exercise.
func TestACheckpointDoesNotCreateDevices(t *testing.T) {
	into := t.TempDir()

	var buf bytes.Buffer
	archive := tar.NewWriter(&buf)
	require.NoError(t, archive.WriteHeader(&tar.Header{
		Name: "sda", Typeflag: tar.TypeChar, Mode: 0o666, Devmajor: 8, Devminor: 0,
	}))
	require.NoError(t, archive.Close())

	require.NoError(t, untar(&buf, into))
	_, err := os.Stat(filepath.Join(into, "sda"))
	require.Error(t, err, "a device node was created")
}

// TestASYMLINKInsideIsKeptAsALink and is not followed — copying what it points
// at would pull whatever is on the other end into the checkpoint.
func TestASymlinkIsArchivedAsALink(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "real"), []byte("bytes"), 0o640))
	require.NoError(t, os.Symlink("real", filepath.Join(dir, "link")))

	var buf bytes.Buffer
	_, err := tarInto(&buf, dir)
	require.NoError(t, err)

	names := map[string]byte{}
	archive := tar.NewReader(&buf)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		names[header.Name] = header.Typeflag
	}
	require.Equal(t, byte(tar.TypeSymlink), names["link"])
	require.Equal(t, byte(tar.TypeReg), names["real"])
}

// tarOf builds an archive with the entries a test names.
func tarOf(t *testing.T, files map[string]string) io.Reader {
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
	return &buf
}

// memAssets is a content-addressed store over a map. What pkg/assets does with
// a DAG is its own test's; what matters here is the property every consumer
// depends on — THE ID IS A FUNCTION OF THE CONTENT, so identical bytes are one
// object. A fake that minted a fresh id per write would hide exactly the
// behaviour storage.go relies on.
type memAssets struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func (m *memAssets) Put(_ context.Context, content io.Reader) (string, error) {
	body, err := io.ReadAll(content)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(body)
	id := "Qm" + hex.EncodeToString(sum[:])

	m.mu.Lock()
	m.blobs[id] = body
	m.mu.Unlock()
	return id, nil
}

func (m *memAssets) Get(_ context.Context, id string) (io.ReadSeekCloser, error) {
	m.mu.Lock()
	body, ok := m.blobs[id]
	m.mu.Unlock()
	if !ok {
		return nil, os.ErrNotExist
	}
	return nopSeekCloser{bytes.NewReader(body)}, nil
}

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }
