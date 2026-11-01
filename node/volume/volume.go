// Package volume gives a container the filesystem its config declared: an adapter
// over a root directory, a content-addressed store for checkpoint bytes, a
// replicated row for each pointer, and a log. A volume is POSIX (fsync, rename,
// locks), the one thing a node keeps as a real directory. Serving it beats refusing
// it: without one a container's database would vanish with its layer.
//
// Any node holding a volume checkpoints it into a kvdb row any node may write. A node
// whose workload moved keeps the handle, so it writes only a checkpoint whose content
// differs from what it last wrote: the walk is deterministic and the store
// content-addressed, so an idle copy produces the same cid and writes nothing.
// Several workloads mounting one volume read-write would need a shared filesystem.
//
// Every `checkpoint/interval` the directory is tarred into the store and the cid
// recorded; a node with no bytes restores the last one. Restores are crash-consistent,
// not quiesced. The quota is measured at each checkpoint and warned about, not
// enforced (that needs project quotas or a loopback image). A restore is held to the
// node's disk reserve (pkg/containers/reserve), not the quota, and synced before its
// final rename, so power loss leaves no volume or all of it.
//
// The walk reads only a file's data extents and writes holes as zeros into a pinned
// compressor, so same files are still the same cid. A restore refuses a frame whose
// window is wider than a checkpoint's before allocating it.
package volume

import (
	"archive/tar"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/taubyte/tau/pkg/containers/reserve"
	"github.com/taubyte/tau/pkg/specs"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/container"
)

// Assets is the content-addressed store a checkpoint's bytes go into.
//
// TWO METHODS, WHICH IS ALL A CHECKPOINT IS: put a tar, get it back. The id is
// a function of the content, which is not a detail — see the package doc on why
// a node whose workload has moved away writes nothing.
type Assets interface {
	Put(ctx context.Context, content io.Reader) (string, error)
	Get(ctx context.Context, id string) (io.ReadSeekCloser, error)
}

// Rows is where a checkpoint's pointer is recorded: read one row, write one row. "Not
// there" is a bool: a never-checkpointed volume is every first mount.
type Rows interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, value []byte) error
}

// Stores opens the one place every checkpoint on this node is recorded.
//
// ONE METHOD, ASKED LAZILY, so a node that mounts nothing opens no database —
// and a node whose operator gave it no store at all refuses by name on the first
// checkpoint rather than at start, for a resource it may never touch.
type Stores interface {
	Store() (Rows, error)
}

// Log is where this adapter tells an operator what it found: two methods, and a
// dependency rather than a package logger, since the quota warning is the whole of
// its enforcement and must be assertable.
type Log interface {
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
}

// Volumes is the adapter: a directory per handle under this node's root,
// restored from the store and checkpointed back into it. Construct it with New.
type Volumes struct {
	root   string
	assets Assets
	stores Stores
	log    Log

	// floor is what a restore leaves free on the disk it writes to, and sync
	// is how it makes what it wrote durable.
	floor reserve.Floor
	sync  func(*os.File) error

	// read is how a checkpoint reads a file's data, which is all of a file it
	// reads: its holes go into the archive as zeros nothing read.
	read func(*os.File, []byte, int64) (int, error)

	mu   sync.Mutex
	rows Rows
	open map[string]*volume

	// opening is the handles a restore is in flight for, so a second cold
	// start of one workload waits rather than mounting a half-unpacked
	// directory.
	opening map[string]chan struct{}
}

var _ container.Volumes = (*Volumes)(nil)

// volume is one mounted directory.
type volume struct {
	handle string
	dir    string

	// quota is what the author declared, in bytes. Zero means the compiled
	// resource carried none, which the vocabulary does not allow.
	quota int64

	// every is how often it is checkpointed.
	every time.Duration

	// wrote is the checkpoint this node last put in the store for this handle.
	// Guarded by Volumes.mu.
	wrote string

	// at is when this node last wrote one, zero for never. Guarded by
	// Volumes.mu.
	at time.Time
}

var (
	// ErrNoRoot is a node asked to hold volumes with nowhere to put one.
	ErrNoRoot = errors.New("volume: this node has no root directory, and a volume is a directory")

	// ErrNoStore is a node that can make the directory and cannot make it
	// survive being moved.
	ErrNoStore = errors.New("volume: a volume is checkpointed into the asset store, and this node has none")
)

// New wires the adapter.
//
// assets MAY BE NIL, and that is a node with a disk and no DAG: it mounts, and
// every checkpoint refuses by name — and a handle that HAS a recorded checkpoint
// refuses to mount at all rather than handing a workload an empty database.
//
// root, stores and log may not be. Without a root there is nowhere to put a
// directory; without somewhere to record a pointer a checkpoint is bytes nothing
// can find again; and without somewhere to say so, a quota that is measured
// rather than enforced is measured into nothing.
func New(root string, assets Assets, stores Stores, log Log) (*Volumes, error) {
	if root == "" {
		return nil, ErrNoRoot
	}
	if stores == nil {
		return nil, errors.New("volume: nowhere to record where a checkpoint went")
	}
	if log == nil {
		return nil, errors.New("volume: nowhere to say that a volume is over its quota")
	}
	return &Volumes{
		root: root, assets: assets, stores: stores, log: log,
		floor: reserve.Default, sync: (*os.File).Sync, read: (*os.File).ReadAt,
	}, nil
}

// The setting keys services/worker writes for a mount, which are the
// vocabulary's own attribute paths.
const (
	settingQuota    = specs.VolumeKeyQuota
	settingInterval = specs.VolumeCheckpointKeyInterval
)

// Settings is every attribute path this adapter reads off a grant, exported so
// node/settings_test.go can hold it to the vocabulary.
var Settings = []string{settingQuota, settingInterval}

const (
	// checkpointPrefix is where a volume's last checkpoint is recorded.
	checkpointPrefix = "volume/at/"

	// defaultCheckpoint is what the vocabulary defaults `checkpoint/interval`
	// to. It is here for a compiled object that carried none.
	defaultCheckpoint = 60 * time.Second
)

// Mount gives back the host directory this grant names, restoring it from the
// last checkpoint if this node has never held it.
func (v *Volumes) Mount(ctx context.Context, grant invoke.Grant) (string, error) {
	// One opener per handle: a second cold start waits rather than mounting a
	// half-restored directory.
	for {
		v.mu.Lock()
		if held, ok := v.open[grant.Handle]; ok {
			v.mu.Unlock()
			return held.dir, nil
		}
		if opening, busy := v.opening[grant.Handle]; busy {
			v.mu.Unlock()
			select {
			case <-opening:
				continue
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		if v.opening == nil {
			v.opening = map[string]chan struct{}{}
		}
		v.opening[grant.Handle] = make(chan struct{})
		v.mu.Unlock()
		break
	}

	// WHOEVER CLAIMED IT RELEASES IT, on every path out — a failed restore that
	// left the channel open would wedge every later mount of this volume.
	defer func() {
		v.mu.Lock()
		if opening, held := v.opening[grant.Handle]; held {
			close(opening)
			delete(v.opening, grant.Handle)
		}
		v.mu.Unlock()
	}()

	dir := filepath.Join(v.root, "volumes", escapeHandle(grant.Handle))
	quota := grant.Number(settingQuota)

	// RESTORED ONLY WHEN THE DIRECTORY IS NEW. A node that already holds bytes
	// holds the newer ones: the checkpoint is behind by up to an interval by
	// construction, so overwriting live data with it would be losing the very
	// writes the checkpoint exists to bound.
	switch _, err := os.Stat(dir); {
	case errors.Is(err, fs.ErrNotExist):
		if err := v.restore(ctx, grant.Handle, dir, quota); err != nil {
			return "", err
		}
	case err != nil:
		return "", err
	}

	opened := &volume{
		handle: grant.Handle,
		dir:    dir,
		quota:  quota,
		every:  durationOr(grant.Settings[settingInterval], defaultCheckpoint),
	}

	v.mu.Lock()
	if v.open == nil {
		v.open = map[string]*volume{}
	}
	v.open[grant.Handle] = opened
	v.mu.Unlock()

	return dir, nil
}

// restore makes the volume's directory, holding the last checkpoint when there
// is one.
//
// A CHECKPOINT IS UNPACKED BESIDE THE DIRECTORY AND RENAMED INTO PLACE WHOLE,
// because the directory existing is what tells a later mount that its bytes are
// here. One that a failed or refused restore left behind would be mounted as
// though it had been restored.
func (v *Volumes) restore(ctx context.Context, handle, dir string, quota int64) error {
	staging := dir + restoring
	if err := discard(staging); err != nil {
		return fmt.Errorf("volume: clearing an unfinished restore of %s: %w", handle, err)
	}

	rows, err := v.store()
	if err != nil {
		// Nowhere to have recorded one, so there is nothing to read back and an
		// empty directory is the honest answer rather than a failure.
		return makeDir(dir)
	}

	// A failed read is treated as "nothing recorded", giving an empty directory: the
	// alternative is a mount failing on a transient read.
	recorded, found, err := rows.Get(ctx, checkpointPrefix+handle)
	if err != nil || !found || len(recorded) == 0 {
		return makeDir(dir)
	}
	if v.assets == nil {
		return ErrNoStore
	}

	content, err := v.assets.Get(ctx, string(recorded))
	if err != nil {
		return fmt.Errorf("volume: reading the checkpoint for %s: %w", handle, err)
	}
	defer content.Close()

	if err := makeDir(staging); err != nil {
		return err
	}
	unpacked, err := unpack(content, staging, v.floor, v.sync)
	if err == nil {
		err = os.Rename(staging, dir)
	}
	if err == nil {
		err = syncDir(filepath.Dir(dir), v.sync)
	}
	if err != nil {
		// What this leaves, the next restore of the volume clears first.
		discard(staging)
		return fmt.Errorf("volume: restoring %s: %w", handle, err)
	}

	// SAID AND SERVED, as at a checkpoint: see the package doc for why the
	// quota is not what a restore is held to.
	if quota > 0 && unpacked > quota {
		v.log.Warnf("volume: %s restored %d bytes and declared a quota of %d", handle, unpacked, quota)
	}
	v.log.Infof("volume: restored %s from %s", handle, string(recorded))
	return nil
}

// checkpoint walks one volume, puts it in the store and records where. It
// answers how many bytes it walked, which is what the quota is measured
// against.
func (v *Volumes) checkpoint(ctx context.Context, held *volume) (int64, error) {
	if v.assets == nil {
		return 0, ErrNoStore
	}
	rows, err := v.store()
	if err != nil {
		return 0, err
	}

	// STREAMED RATHER THAN BUFFERED, because the thing being tarred is a
	// database's data directory and holding a copy of one in memory to hand it
	// to the store is how a node dies checkpointing.
	read, write := io.Pipe()
	walked := make(chan int64, 1)
	go func() {
		n, err := pack(write, held.dir, v.read)
		walked <- n
		write.CloseWithError(err)
	}()

	id, err := v.assets.Put(ctx, read)
	read.Close()
	size := <-walked
	if err != nil {
		return 0, fmt.Errorf("volume: checkpointing %s: %w", held.handle, err)
	}

	// THE QUOTA IS MEASURED HERE AND NOWHERE ELSE, because this is the one pass
	// that already walks every file. See the package doc for why it is a
	// measurement rather than a wall — and why the warning IS the enforcement,
	// which is why it carries both numbers.
	if held.quota > 0 && size > held.quota {
		v.log.Warnf("volume: %s holds %d bytes and declared a quota of %d", held.handle, size, held.quota)
	}

	// NOTHING CHANGED, SO NOTHING IS WRITTEN. See the package doc: this is what
	// keeps a node that no longer runs the workload from putting its own older
	// bytes back over the pointer, every interval, until somebody notices a
	// database that lost a day.
	v.mu.Lock()
	unchanged := held.wrote == id
	held.wrote = id
	v.mu.Unlock()
	if unchanged {
		return size, nil
	}

	return size, rows.Put(ctx, checkpointPrefix+held.handle, []byte(id))
}

// store asks for the one place checkpoints are recorded, once. It runs under the
// same lock as the volumes so two concurrent first-mounts cannot open two.
func (v *Volumes) store() (Rows, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.rows != nil {
		return v.rows, nil
	}
	rows, err := v.stores.Store()
	if err != nil {
		return nil, err
	}
	v.rows = rows
	return rows, nil
}

// Checkpoint walks every volume whose interval has elapsed as of now and
// answers how many it wrote.
//
// NOW IS A PARAMETER because each volume has its own interval and what this
// decides is which of them are due — so the caller's tick can be finer than any
// of them without this package owning a clock.
func (v *Volumes) Checkpoint(ctx context.Context, now time.Time) int {
	return v.write(ctx, v.due(now), now)
}

// CheckpointAll walks every volume this node holds, whatever its interval. It is
// the way out: a planned shutdown that skipped a volume because its interval had
// not come round would throw away writes inside the budget its author declared.
func (v *Volumes) CheckpointAll(ctx context.Context) int {
	return v.write(ctx, v.held(), time.Now())
}

// due is every volume whose interval has elapsed.
func (v *Volumes) due(now time.Time) []*volume {
	v.mu.Lock()
	defer v.mu.Unlock()

	var out []*volume
	for _, held := range v.open {
		if !held.at.IsZero() && now.Sub(held.at) < held.every {
			continue
		}
		out = append(out, held)
	}
	return out
}

// held is every volume this node has mounted.
func (v *Volumes) held() []*volume {
	v.mu.Lock()
	defer v.mu.Unlock()

	out := make([]*volume, 0, len(v.open))
	for _, held := range v.open {
		out = append(out, held)
	}
	return out
}

// write checkpoints each of them and answers how many it wrote. One that failed
// is said and skipped: a pass over every volume is one pass, and the rest are
// still written.
func (v *Volumes) write(ctx context.Context, walk []*volume, now time.Time) int {
	var written int
	for _, held := range walk {
		if _, err := v.checkpoint(ctx, held); err != nil {
			v.log.Warnf("volume: %s: %s", held.handle, err)
			continue
		}
		v.mu.Lock()
		held.at = now
		v.mu.Unlock()
		written++
	}
	return written
}

// Run checkpoints what is due every tick, until the context is done.
//
// THE TICK IS THE CALLER'S AND IS FINER THAN ANY INTERVAL, because each volume
// has its own and what a tick decides is which of them are due. How fine is the
// node's own decision, like the name of the database the pointers go in.
func (v *Volumes) Run(ctx context.Context, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			// A LAST CHECKPOINT ON THE WAY OUT, so a planned shutdown does not
			// throw away up to an interval of writes. It is best effort: the
			// context is already done, so it runs against a fresh one bounded
			// by the apply timeout.
			final, stop := context.WithTimeout(context.WithoutCancel(ctx), applyTimeout)
			v.CheckpointAll(final)
			stop()
			return
		case now := <-tick.C:
			v.Checkpoint(ctx, now)
		}
	}
}

// applyTimeout bounds the last checkpoint a closing node takes.
//
// IT IS THE BOUND ON A REQUEST AND NOT ON A CLUSTER, which is the reading it
// has everywhere else in this tree: work that has not committed in five seconds
// is work the caller should stop waiting for.
const applyTimeout = 5 * time.Second

// Close stops holding volumes. The directories stay: they are what a restart
// finds, and re-reading them from the store would be slower and older.
func (v *Volumes) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.open = nil
	return nil
}

// escapeHandle turns a handle into one path segment. A handle carries slashes —
// `project/environment/id` — and a directory per segment would put one
// project's volumes inside another's name.
func escapeHandle(handle string) string {
	return strings.ReplaceAll(strings.ReplaceAll(handle, "%", "%25"), "/", "%2F")
}

// restoring is the suffix a restore in progress is unpacked under, beside the
// directory it becomes. escapeHandle never makes a name holding it, so no
// volume's directory is ever taken for another's unfinished restore.
const restoring = "%restoring"

// makeDir makes a volume's directory, and the directory volumes live in.
func makeDir(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("volume: making a volume directory: %w", err)
	}
	return nil
}

// discard removes what an unfinished restore left. Each directory is opened up
// as it is reached, since the restore may already have closed it; whatever
// still cannot be removed, RemoveAll says.
func discard(dir string) error {
	filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return nil
		}
		return os.Chmod(path, 0o700)
	})
	return os.RemoveAll(dir)
}

// durationOr reads a duration, falling back when the compiled object carried
// none.
func durationOr(value string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(value); err == nil && d > 0 {
		return d
	}
	return fallback
}

// window is the history a checkpoint is compressed with, and the most a
// restore holds to read one back.
//
// IT IS A QUARTER OF WHAT THE FASTEST LEVEL WOULD TAKE. A decoder moves its
// whole history down each time its buffer fills, so a wider window is a slower
// restore, and the archives it was measured on came out no smaller for it.
const window = 1 << 20

// encoding is how a checkpoint is compressed. Every setting is pinned, because
// the same files must be the same bytes: one encoder, so nothing depends on how
// its blocks were scheduled, at the fastest level, since a checkpoint runs every
// interval beside the workload it is taken from.
var encoding = []zstd.EOption{
	zstd.WithEncoderConcurrency(1),
	zstd.WithEncoderLevel(zstd.SpeedFastest),
	zstd.WithWindowSize(window),
}

// decoding is how one is read back: one decoder, which decodes no frame whose
// window is wider than a checkpoint's, so a crafted one cannot have it allocate
// more.
var decoding = []zstd.DOption{
	zstd.WithDecoderConcurrency(1),
	zstd.WithDecoderMaxWindow(window),
}

// pack walks a directory into a compressed tar stream, and reports the total
// content size, as tarInto does.
//
// WHAT IT WRITES IS BUFFERED, because a block of zeros compresses to a few
// bytes, and handing the store each of those on its own is a wakeup of its
// reader per block.
func pack(w io.Writer, dir string, read func(*os.File, []byte, int64) (int, error)) (int64, error) {
	buffered := bufio.NewWriterSize(w, 64<<10)
	compressed, err := zstd.NewWriter(buffered, encoding...)
	if err != nil {
		return 0, err
	}
	n, err := tarInto(compressed, dir, read)
	if err == nil {
		err = compressed.Close()
	}
	if err == nil {
		err = buffered.Flush()
	}
	return n, err
}

// unpack decompresses a checkpoint and unpacks the archive it holds into a
// directory, answering what untar does.
func unpack(r io.Reader, dir string, floor reserve.Floor, sync func(*os.File) error) (int64, error) {
	decompressed, err := zstd.NewReader(r, decoding...)
	if err != nil {
		return 0, err
	}
	defer decompressed.Close()
	return untar(decompressed, dir, floor, sync)
}

// tarInto walks a directory into a tar stream and reports the total content size
// (what the quota is measured against). Symlinks are written, not followed. Each
// entry is opened once through an os.Root and described by its own stat, so a name
// changing mid-walk cannot pull in anything outside. A socket fails the whole
// checkpoint (archive/tar refuses it) and is warned about each interval; skipping it
// would silently checkpoint less than the directory holds.
func tarInto(w io.Writer, dir string, read func(*os.File, []byte, int64) (int, error)) (int64, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, err
	}
	defer root.Close()

	archive := tar.NewWriter(w)
	defer archive.Close()

	var total int64
	err = fs.WalkDir(root.FS(), ".", func(name string, listed fs.DirEntry, err error) error {
		if err != nil || name == "." {
			return err
		}
		n, isDir, err := writeEntry(archive, root, name, read)
		total += n
		if err == nil && listed.IsDir() && !isDir {
			return fs.SkipDir
		}
		return err
	})
	return total, err
}

// entry is one name in a volume as it was when it was opened: its own stat,
// where it points when it is a link, and its bytes when it is a regular file.
type entry struct {
	info    fs.FileInfo
	link    string
	content *os.File
}

// writeEntry puts one name in the archive, and answers how many bytes of
// content it carried and whether it was a directory when it was opened.
//
// THE CONTENT IS AS LONG AS THE HEADER SAYS, which is the size the file had
// when it was opened: bytes a writer appends after that are the next
// checkpoint's.
//
// AND ONLY ITS DATA IS READ. A hole is in the archive as the zeros it reads as,
// which archive/tar has no other way to write, but they are not read off the
// disk to put them there.
func writeEntry(archive *tar.Writer, root *os.Root, name string, read func(*os.File, []byte, int64) (int, error)) (int64, bool, error) {
	opened, err := openEntry(root, name)
	if err != nil {
		return 0, false, err
	}
	if opened.content != nil {
		defer opened.content.Close()
	}

	header, err := tar.FileInfoHeader(opened.info, opened.link)
	if err != nil {
		return 0, false, err
	}
	header.Name = name
	if err := archive.WriteHeader(header); err != nil {
		return 0, false, err
	}
	if opened.content == nil || !opened.info.Mode().IsRegular() {
		return 0, opened.info.IsDir(), nil
	}
	n, err := io.CopyN(archive, &holes{file: opened.content, read: read, size: header.Size}, header.Size)
	return n, false, err
}

// holes reads the first size bytes of a file the way its tar entry carries
// them: its data as read, and its holes as zeros that are not.
type holes struct {
	file *os.File
	read func(*os.File, []byte, int64) (int, error)
	size int64

	// at is how far it has read, and the data at or after it is from data
	// up to end.
	at, data, end int64
}

func (h *holes) Read(p []byte) (int, error) {
	if h.at >= h.size {
		return 0, io.EOF
	}
	if h.at >= h.end {
		var err error
		if h.data, h.end, err = extent(h.file, h.at, h.size); err != nil {
			return 0, err
		}
	}
	if h.at < h.data {
		n := int(min(int64(len(p)), h.data-h.at))
		clear(p[:n])
		h.at += int64(n)
		return n, nil
	}
	n, err := h.read(h.file, p[:min(int64(len(p)), h.end-h.at)], h.at)
	h.at += int64(n)
	return n, err
}

// untar unpacks a checkpoint's archive into a directory and answers the bytes it
// unpacked. Every entry is confined to the destination and created through an
// os.Root, since earlier links lie along later paths. The disk keeps its reserve,
// checked before each entry and chunk, with zero runs left as holes. Everything is
// synced, files when written and directories when complete.
func untar(r io.Reader, dir string, floor reserve.Floor, sync func(*os.File) error) (int64, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, err
	}
	defer root.Close()

	var (
		links    []string
		unpacked int64
	)
	shut := map[string]fs.FileMode{}
	archive := tar.NewReader(r)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			if err := checkLinks(root, links); err != nil {
				return unpacked, err
			}
			if err := syncDirs(root, sync); err != nil {
				return unpacked, err
			}
			return unpacked, closeDirs(root, shut, sync)
		}
		if err != nil {
			return unpacked, err
		}

		name, err := within(header.Name)
		if err != nil {
			return unpacked, err
		}

		switch header.Typeflag {
		case tar.TypeDir, tar.TypeSymlink, tar.TypeReg:
			if err := floor.Take(dir, 0, 1); err != nil {
				return unpacked, err
			}
		}

		switch header.Typeflag {
		case tar.TypeDir:
			// A DIRECTORY ITS OWNER CANNOT LIST, ENTER OR WRITE IS CLOSED LAST.
			// Made so at once, it would refuse the entries that go inside it,
			// and the root, which opens every directory it passes through,
			// could not pass through it.
			perm := fs.FileMode(header.Mode).Perm()
			if err := root.MkdirAll(name, perm|0o700); err != nil {
				return unpacked, err
			}
			if closed := 0o700 &^ perm; closed != 0 {
				shut[name] = closed
			}
		case tar.TypeSymlink:
			// A LINK IS CHECKED WHERE IT POINTS, not only where it is written:
			// a relative target climbing out is the same escape as a name that
			// does, and it is followed by whatever mounts this next.
			//
			// AN ABSOLUTE TARGET IS CHECKED FIRST, because joining it onto the
			// entry's directory discards the leading separator and turns
			// `/etc/passwd` into a path inside the volume that passes — while
			// the link written on disk still points at /etc/passwd.
			if filepath.IsAbs(filepath.FromSlash(header.Linkname)) {
				return unpacked, fmt.Errorf("volume: a checkpoint entry links to %q, which is outside the volume", header.Linkname)
			}
			if _, err := within(filepath.Join(filepath.Dir(header.Name), header.Linkname)); err != nil {
				return unpacked, err
			}
			if err := root.MkdirAll(filepath.Dir(name), 0o750); err != nil {
				return unpacked, err
			}
			if err := root.Symlink(header.Linkname, name); err != nil && !errors.Is(err, fs.ErrExist) {
				return unpacked, err
			}
			links = append(links, name)
		case tar.TypeReg:
			if err := root.MkdirAll(filepath.Dir(name), 0o750); err != nil {
				return unpacked, err
			}
			file, err := root.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fs.FileMode(header.Mode).Perm())
			if err != nil {
				return unpacked, err
			}
			n, err := floor.Copy(file, archive)
			unpacked += n
			if err == nil {
				err = sync(file)
			}
			file.Close()
			if err != nil {
				return unpacked, err
			}
		default:
			// A device, a fifo, a hard link. Nothing writes one into a
			// checkpoint, and creating one from bytes in a store is a
			// privilege this has no reason to exercise.
			continue
		}
	}
}

// checkLinks follows every link a checkpoint made, from where it sits.
//
// THE CHECK ON A TARGET READS IT FROM THE NAME THE ARCHIVE GAVE THE LINK, and
// that is not where it leads once other links are on disk: one made before or
// after it can sit along its target. So once all of them exist each is followed
// again through the root, and one that leads out of the volume, or that the
// root gives up following, refuses the restore. One that leads nowhere — to a
// name that does not exist, or through a file — is kept.
//
// THEY ARE THE LINKS THIS MADE, followed by name. An archive makes a link no
// other way here, so a data directory's every file need not be walked to find
// them.
func checkLinks(root *os.Root, links []string) error {
	for _, name := range links {
		_, err := root.Stat(name)
		if err == nil || errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			continue
		}
		return fmt.Errorf("volume: a checkpoint link %q does not resolve inside the volume: %w", name, err)
	}
	return nil
}

// closeDirs takes away what a directory's entry did not grant its owner, once
// everything is unpacked. The deepest go first, so that a parent its owner
// cannot pass through is closed after what is in it.
//
// DEEPEST ON DISK, AND EACH CLOSED WHERE ITS NAME LEADS. A name says nothing
// about either once a link lies along it: `l/d`, with `l` a link to `a/b/c`, is
// four directories down rather than two. So every name is followed to the path
// it reaches before anything is closed, and that path is what is closed. The
// volume itself is never closed, whatever name reaches it.
//
// EACH IS CLOSED THROUGH A HANDLE OPENED FIRST, and synced through it, since
// once closed its owner could not open it again to make the mode durable.
func closeDirs(root *os.Root, shut map[string]fs.FileMode, sync func(*os.File) error) error {
	onDisk := map[string]fs.FileMode{}
	for name, closed := range shut {
		at, err := reached(root, name)
		if err != nil {
			return err
		}
		if at != "." {
			onDisk[at] |= closed
		}
	}

	names := slices.Collect(maps.Keys(onDisk))
	slices.SortFunc(names, func(a, b string) int {
		return strings.Count(b, string(os.PathSeparator)) - strings.Count(a, string(os.PathSeparator))
	})
	for _, name := range names {
		dir, err := root.Open(name)
		if err != nil {
			return err
		}
		info, err := dir.Stat()
		if err == nil {
			err = dir.Chmod(info.Mode().Perm() &^ onDisk[name])
		}
		if err == nil {
			err = sync(dir)
		}
		dir.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// syncDirs syncs every directory under root, root's own among them, so the
// names made in each are on disk.
func syncDirs(root *os.Root, sync func(*os.File) error) error {
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return err
		}
		dir, err := root.Open(name)
		if err != nil {
			return err
		}
		defer dir.Close()
		return sync(dir)
	})
}

// syncDir syncs one directory, by its path on the host.
func syncDir(path string, sync func(*os.File) error) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return sync(dir)
}

// maxLinks is as many links as os.Root follows in one name, so a name it made
// a directory at never needs more.
const maxLinks = 8

// reached is the path name leads to inside root, with every link along it
// followed: a path with no link in it, for the directory name reaches.
func reached(root *os.Root, name string) (string, error) {
	var at []string
	rest := strings.Split(filepath.ToSlash(name), "/")
	for links := 0; len(rest) > 0; {
		part := rest[0]
		rest = rest[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			if len(at) == 0 {
				return "", fmt.Errorf("volume: %q leads out of the volume", name)
			}
			at = at[:len(at)-1]
			continue
		}

		next := filepath.Join(append(slices.Clone(at), part)...)
		info, err := root.Lstat(next)
		if err != nil {
			return "", err
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			at = append(at, part)
			continue
		}
		if links++; links > maxLinks {
			return "", fmt.Errorf("volume: %q passes through more than %d links", name, maxLinks)
		}
		target, err := root.Readlink(next)
		if err != nil {
			return "", err
		}
		rest = append(strings.Split(filepath.ToSlash(target), "/"), rest...)
	}
	return filepath.Join(append([]string{"."}, at...)...), nil
}

// within is the name an entry has inside the volume, or an error when it names
// somewhere else.
//
// IT REFUSES RATHER THAN CLAMPING. Joining a cleaned "/"+name onto the
// destination is the usual trick and it does keep the write inside — but it
// SILENTLY REWRITES `../etc/passwd` into `etc/passwd`, so an archive that tried
// to escape unpacks successfully somewhere else and nothing says it tried.
// Nothing this node writes names a parent, so an entry that does is not one of
// ours and is the thing worth failing on. What this does not anticipate, a
// drive letter say, the root the name is opened in refuses on its own.
func within(name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("volume: a checkpoint entry names %q, which is outside the volume", name)
	}
	return clean, nil
}
