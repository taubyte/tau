package node

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/container"
)

// volume.go gives a container the filesystem its config declared.
//
// # It is the one thing a node keeps as a directory
//
// Everything else this node holds is content-addressed or replicated. A volume
// is POSIX — a database's data directory, with fsync and rename and locks — so
// it is a real directory on a real disk, and that is why a node has a root at
// all (see WithRoot).
//
// # Why serving it beats refusing it
//
// A container declaring `volume: {pgdata: {ops: [mount-rw], path:
// /var/lib/postgresql/data}}` and started WITHOUT one writes its database into
// the container's own layer and loses it when the container goes — silently.
// That is why the tier refused, and it was right to while there was nothing to
// give it. It is also the single most common shape a compose import produces,
// so refusing it meant an imported stack with a database could not run at all.
//
// # Every node that holds a volume checkpoints it
//
// The pointer used to be a raft write, which is LEADER-ONLY — and a volume is
// mounted wherever placement put the workload, not on the leader. So on every
// other node the checkpoint returned success without writing anything: nothing
// reached the store, no pointer was recorded, and the node counted the volume
// as checkpointed. The workload then moved and restored an empty directory.
// That is the exact silent loss the mount refusal existed to prevent, one level
// up.
//
// It is a replicated kvdb row now, which any node may write. That makes the key
// one SEVERAL nodes can write, so what stops the wrong one winning is that a
// node only writes a checkpoint whose content it has not already written.
//
// A NODE NEVER STOPS HOLDING A VOLUME — Mount has no opposite, and the tier's
// seam is one method. So when placement moves a workload, the node it left
// keeps the handle and keeps checkpointing a directory nothing writes to any
// more. Both nodes write one key in a CRDT, the old one writes last as often as
// its interval comes round, and the pointer ends up naming bytes from before
// the move. Everything written since is gone on the next restore, silently,
// which is the loss this file exists to prevent.
//
// Unchanged bytes are the same object, because the store is content-addressed
// and the walk is deterministic: same files, same tar, same cid. So a node whose
// workload has gone produces the id it produced last time and writes nothing,
// while the node actually running the workload produces a new one and does. The
// genuinely shared case — the vocabulary lets more than one workload mount a
// volume read-write — is still divergent data before any pointer is written,
// and needs a shared filesystem rather than a rule here.
//
// # A moved workload restores, and what it loses is stated
//
// The bytes are node-local, so placement moving a workload would find an empty
// directory. What closes that is the vocabulary's own answer: `checkpoint`. The
// directory is walked, tarred and put in the content-addressed store every
// `checkpoint/interval`, the cid is recorded in the same raft cluster a
// project's queues and crons use, and a node opening a volume it has no bytes
// for restores the last one.
//
// WHAT A RESTORE GIVES BACK IS CRASH-CONSISTENT AND NOT QUIESCED, which is the
// vocabulary's own sentence: the walk runs while writers keep writing, so it
// holds what a power cut leaves behind. Anything that recovers on its own — a
// journal, a WAL — recovers; nothing here promises a consistent moment across
// several writers and nothing here could without stopping them for the walk.
//
// # The quota is measured and is not a wall
//
// `quota` is Required and the vocabulary calls it hard. THIS IMPLEMENTATION
// MEASURES IT AT EACH CHECKPOINT AND DOES NOT ENFORCE IT: a portable hard limit
// on a directory needs either a filesystem that has project quotas or a
// loopback image, and neither is something a node can assume it has.
//
// It is served rather than refused because the failure is LOUD AND IS THE
// OPERATOR'S: a workload over its quota fills a disk the node can see, and the
// node says so on every checkpoint. That is the opposite of the mount refusal,
// where the failure was silent and the tenant's. The upgrade path is one
// method: a Volumes implementation that hands back a path inside a size-capped
// filesystem changes nothing above it.

// volumes is the node's container.Volumes: a directory per handle under this
// node's root, restored from the store and checkpointed back into it.
type volumes struct {
	node *Node

	mu   sync.Mutex
	db   kvdb.KVDB
	open map[string]*volume

	// opening is the handles a restore is in flight for, so a second cold
	// start of one workload waits rather than mounting a half-unpacked
	// directory.
	opening map[string]chan struct{}
}

var _ container.Volumes = (*volumes)(nil)

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
	// Guarded by volumes.mu.
	wrote string
}

var (
	// ErrNoRoot is a node asked to mount a volume with nowhere to put one.
	ErrNoRoot = errors.New("node: this node has no root directory, and a volume is a directory")

	// ErrNoStore is a node that can make the directory and cannot make it
	// survive being moved.
	ErrNoStore = errors.New("node: a volume is checkpointed into the asset store, and this node has none")
)

const (
	// checkpointPrefix is where a volume's last checkpoint is recorded.
	checkpointPrefix = "volume/at/"

	// volumeDatabase is the one database's name, for cacheDatabase's reason.
	volumeDatabase = "volumes"

	// defaultCheckpoint is what the vocabulary defaults `checkpoint/interval`
	// to. It is here for a compiled object that carried none.
	defaultCheckpoint = 60 * time.Second

	// The setting keys services/worker writes for a mount, which are the
	// vocabulary's own attribute paths.
	settingVolumeQuota    = "quota"
	settingCheckpointFreq = "checkpoint/interval"
)

// Mount gives back the host directory this grant names, restoring it from the
// last checkpoint if this node has never held it.
func (v *volumes) Mount(ctx context.Context, grant invoke.Grant) (string, error) {
	if v.node.opts.root == "" {
		return "", ErrNoRoot
	}

	// ONE OPENER PER HANDLE. Two cold starts of one workload race here, and the
	// loser used to get the directory back while the winner was still unpacking
	// into it — a container mounting a half-restored database. The second
	// caller waits for the first rather than repeating its work.
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

	dir := filepath.Join(v.node.opts.root, "volumes", escapeHandle(grant.Handle))
	fresh, err := ensureDir(dir)
	if err != nil {
		return "", err
	}

	// RESTORED ONLY WHEN THE DIRECTORY IS NEW. A node that already holds bytes
	// holds the newer ones: the checkpoint is behind by up to an interval by
	// construction, so overwriting live data with it would be losing the very
	// writes the checkpoint exists to bound.
	if fresh {
		if err := v.restore(ctx, grant.Handle, dir); err != nil {
			return "", err
		}
	}

	opened := &volume{
		handle: grant.Handle,
		dir:    dir,
		quota:  bytesOf(grant.Settings[settingVolumeQuota]),
		every:  durationOr(grant.Settings[settingCheckpointFreq], defaultCheckpoint),
	}

	v.mu.Lock()
	if v.open == nil {
		v.open = map[string]*volume{}
	}
	v.open[grant.Handle] = opened
	v.mu.Unlock()

	return dir, nil
}

// restore unpacks the last checkpoint, or does nothing when there is not one.
func (v *volumes) restore(ctx context.Context, handle, dir string) error {
	db, err := v.store()
	if err != nil {
		// Nowhere to have recorded one, so there is nothing to read back and an
		// empty directory is the honest answer rather than a failure.
		return nil
	}

	recorded, err := db.Get(ctx, checkpointPrefix+handle)
	if err != nil || len(recorded) == 0 {
		return nil
	}
	if v.node.opts.assets == nil {
		return ErrNoStore
	}

	content, err := v.node.opts.assets.Get(ctx, string(recorded))
	if err != nil {
		return fmt.Errorf("node: reading the checkpoint for %s: %w", handle, err)
	}
	defer content.Close()

	if err := untar(content, dir); err != nil {
		return fmt.Errorf("node: restoring %s: %w", handle, err)
	}
	logger.Infof("volume: restored %s from %s", handle, string(recorded))
	return nil
}

// checkpoint walks one volume, puts it in the store and records where. It
// answers how many bytes it walked, which is what the quota is measured
// against — returned rather than only logged so that the measurement is
// something a test can assert on rather than a line somebody hopes was
// printed.
func (v *volumes) checkpoint(ctx context.Context, held *volume) (int64, error) {
	if v.node.opts.assets == nil {
		return 0, ErrNoStore
	}
	db, err := v.store()
	if err != nil {
		return 0, err
	}

	// STREAMED RATHER THAN BUFFERED, because the thing being tarred is a
	// database's data directory and holding a copy of one in memory to hand it
	// to the store is how a node dies checkpointing.
	read, write := io.Pipe()
	walked := make(chan int64, 1)
	go func() {
		n, err := tarInto(write, held.dir)
		walked <- n
		write.CloseWithError(err)
	}()

	id, err := v.node.opts.assets.Put(ctx, read)
	read.Close()
	size := <-walked
	if err != nil {
		return 0, fmt.Errorf("node: checkpointing %s: %w", held.handle, err)
	}

	// THE QUOTA IS MEASURED HERE AND NOWHERE ELSE, because this is the one pass
	// that already walks every file. See the file doc for why it is a
	// measurement rather than a wall.
	if held.quota > 0 && size > held.quota {
		logger.Warnf("volume: %s holds %d bytes and declared a quota of %d", held.handle, size, held.quota)
	}

	// NOTHING CHANGED, SO NOTHING IS WRITTEN. See the file doc: this is what
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

	return size, db.Put(ctx, checkpointPrefix+held.handle, []byte(id))
}

// store opens the one database this node records checkpoints in, once.
func (v *volumes) store() (kvdb.KVDB, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.db != nil {
		return v.db, nil
	}
	if v.node.opts.databases == nil {
		return nil, ErrNoCacheStore
	}

	db, err := v.node.opts.databases.New(logger, volumeDatabase, rebroadcastSeconds)
	if err != nil {
		return nil, fmt.Errorf("node: opening the volumes database: %w", err)
	}
	v.db = db
	v.node.addCloser(closerFunc(db.Close))
	return db, nil
}

// due is every volume whose interval has elapsed.
func (v *volumes) due(now time.Time, last map[string]time.Time) []*volume {
	v.mu.Lock()
	defer v.mu.Unlock()

	var out []*volume
	for handle, held := range v.open {
		if at, ok := last[handle]; ok && now.Sub(at) < held.every {
			continue
		}
		out = append(out, held)
	}
	return out
}

// step checkpoints every volume that is due and reports how many it wrote.
func (v *volumes) step(ctx context.Context, now time.Time, last map[string]time.Time) int {
	var written int
	for _, held := range v.due(now, last) {
		if _, err := v.checkpoint(ctx, held); err != nil {
			logger.Warnf("volume: %s: %s", held.handle, err)
			continue
		}
		last[held.handle] = now
		written++
	}
	return written
}

// loop checkpoints until the node closes.
func (v *volumes) loop(ctx context.Context, done chan<- struct{}) {
	defer close(done)

	// THE TICK IS FINER THAN ANY INTERVAL because each volume has its own, and
	// what this decides per tick is which of them are due.
	tick := time.NewTicker(checkpointTick)
	defer tick.Stop()

	last := map[string]time.Time{}
	for {
		select {
		case <-ctx.Done():
			// A LAST CHECKPOINT ON THE WAY OUT, so a planned shutdown does not
			// throw away up to an interval of writes. It is best effort: the
			// context is already done, so it runs against a fresh one bounded
			// by the apply timeout.
			final, stop := context.WithTimeout(context.WithoutCancel(ctx), applyTimeout)
			v.step(final, time.Now(), map[string]time.Time{})
			stop()
			return
		case now := <-tick.C:
			v.step(ctx, now, last)
		}
	}
}

// checkpointTick is how often the loop asks which volumes are due.
const checkpointTick = 5 * time.Second

// Close stops holding volumes. The directories stay: they are what a restart
// finds, and re-reading them from the store would be slower and older.
func (v *volumes) Close() error {
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

// ensureDir makes the directory and says whether it had to.
func ensureDir(dir string) (fresh bool, err error) {
	switch _, err := os.Stat(dir); {
	case err == nil:
		return false, nil
	case !errors.Is(err, fs.ErrNotExist):
		return false, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return false, fmt.Errorf("node: making a volume directory: %w", err)
	}
	return true, nil
}

// bytesOf reads a byte count the compiler wrote.
func bytesOf(value string) int64 {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// durationOr reads a duration, falling back when the compiled object carried
// none.
func durationOr(value string, fallback time.Duration) time.Duration {
	if d := duration(value); d > 0 {
		return d
	}
	return fallback
}

// tarInto walks a directory into a tar stream and reports the total content
// size — which is what the quota is measured against.
//
// SYMLINKS ARE WRITTEN AS SYMLINKS AND NOT FOLLOWED. Following one would copy
// whatever it points at into the checkpoint, and a link out of the directory is
// a link out of the tenant's data.
func tarInto(w io.Writer, dir string) (int64, error) {
	archive := tar.NewWriter(w)
	defer archive.Close()

	var total int64
	err := filepath.Walk(dir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}

		link := ""
		if info.Mode()&fs.ModeSymlink != 0 {
			if link, err = os.Readlink(path); err != nil {
				return err
			}
		}

		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if err := archive.WriteHeader(header); err != nil {
			return err
		}

		if !info.Mode().IsRegular() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		n, err := io.Copy(archive, file)
		total += n
		return err
	})
	return total, err
}

// untar unpacks a checkpoint into a directory.
//
// EVERY ENTRY IS CHECKED AGAINST THE DESTINATION. A tar is bytes from a store,
// and an entry named `../../etc` would write outside the volume — the oldest
// archive bug there is, and this one unpacks with the node's own privileges.
func untar(r io.Reader, dir string) error {
	archive := tar.NewReader(r)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		target, err := within(dir, header.Name)
		if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, fs.FileMode(header.Mode).Perm()); err != nil {
				return err
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
				return fmt.Errorf("node: a checkpoint entry links to %q, which is outside the volume", header.Linkname)
			}
			if _, err := within(dir, filepath.Join(filepath.Dir(header.Name), header.Linkname)); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			if err := os.Symlink(header.Linkname, target); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fs.FileMode(header.Mode).Perm())
			if err != nil {
				return err
			}
			_, err = io.Copy(file, archive)
			file.Close()
			if err != nil {
				return err
			}
		default:
			// A device, a fifo, a hard link. Nothing writes one into a
			// checkpoint, and creating one from bytes in a store is a
			// privilege this has no reason to exercise.
			continue
		}
	}
}

// within is the path an entry names inside dir, or an error when it names
// somewhere else.
//
// IT REFUSES RATHER THAN CLAMPING. Joining a cleaned "/"+name onto the
// destination is the usual trick and it does keep the write inside — but it
// SILENTLY REWRITES `../etc/passwd` into `etc/passwd`, so an archive that tried
// to escape unpacks successfully somewhere else and nothing says it tried.
// Nothing this node writes names a parent, so an entry that does is not one of
// ours and is the thing worth failing on.
func within(dir, name string) (string, error) {
	outside := func() (string, error) {
		return "", fmt.Errorf("node: a checkpoint entry names %q, which is outside the volume", name)
	}

	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return outside()
	}

	// AND THE JOIN IS CHECKED TOO, because the two are not the same question on
	// every platform: a volume name or a drive letter is absolute to the
	// filesystem and not to filepath.IsAbs everywhere.
	target := filepath.Join(dir, clean)
	if target != dir && !strings.HasPrefix(target, dir+string(os.PathSeparator)) {
		return outside()
	}
	return target, nil
}
