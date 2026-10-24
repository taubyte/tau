// Package volume gives a container the filesystem its config declared.
//
// # Why it is its own package
//
// It is an ADAPTER: it sits between the container tier, which knows a mount
// grant and a path, and two stores that know nothing about either — a
// content-addressed one the bytes go into, and a replicated row that remembers
// where. The composition root is the only place that may NAME all of those —
// but naming them and holding a directory are different jobs, and conflating
// them is how node/ grew to eight thousand lines with twenty-two of these in one
// namespace.
//
// So the root hands this four things: a directory to keep volumes under, a byte
// store, somewhere to record a pointer, and somewhere to say what it found.
// Everything this package needs is in its own signature, which is the property a
// type taking *Node does not have: that one can reach anything the node has, so
// nothing about it says what it actually depends on, and it cannot be built
// anywhere except beside the node.
//
// # It is the one thing a node keeps as a directory
//
// Everything else a node holds is content-addressed or replicated. A volume is
// POSIX — a database's data directory, with fsync and rename and locks — so it
// is a real directory on a real disk, and that is why a node has a root at all
// (see node.WithRoot).
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
// which is the loss this package exists to prevent.
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
// `checkpoint/interval`, the cid is recorded in a replicated row, and a node
// opening a volume it has no bytes for restores the last one.
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
//
// A RESTORE IS HELD TO THE NODE'S DISK AND NOT TO THE QUOTA. A volume that grew
// past its quota while it ran, which is allowed, has a checkpoint bigger than
// the quota, and refusing that on the node it moved to would be a mount that
// fails silently for the tenant until somebody raised the quota. What a restore
// must not do is fill the disk of the node it lands on, so it leaves that disk
// its reserve (see pkg/containers/reserve), fails without leaving anything when
// it would not, and says when it unpacked more than the quota, as a checkpoint
// does.
//
// # A restore is on disk before it is in place
//
// Every file and directory a restore makes is synced before the rename that
// puts it in place, and the directory it is renamed into after. A node that
// loses power during a restore comes back holding either no volume, which the
// next mount restores again, or all of it.
package volume

import (
	"archive/tar"
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

// Rows is where a checkpoint's pointer is recorded.
//
// TWO METHODS AND NOT A kvdb, whose New also takes a logger and a rebroadcast
// interval — a node's decisions about a store, which a volume holds no opinion
// about — and whose result has to be registered with the closers the root keeps.
// What is left is what a checkpoint needs: read one row, write one row.
//
// "NOT THERE" IS A bool AND NOT AN ERROR, because it is the ordinary answer
// here: a volume nobody has ever checkpointed is every volume's first mount, and
// a backing that reported it as an error would make a fresh directory
// indistinguishable from a store that could not be read.
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

// Log is where this adapter tells an operator what it found.
//
// TWO METHODS AND NOT A LOGGER, which carries Debug, Error, Fatal, Panic and a
// sugared variant of each: a volume backing has two things to say — that a
// restore happened, and that something an operator has to act on did not — and
// a parameter wide enough to end the process is wider than that.
//
// IT IS A DEPENDENCY AND NOT A PACKAGE-LEVEL LOGGER because the quota is
// MEASURED RATHER THAN ENFORCED, so the warning is the whole of the enforcement.
// A line nobody can assert on is a promise nobody keeps.
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
	return &Volumes{root: root, assets: assets, stores: stores, log: log, floor: reserve.Default, sync: (*os.File).Sync}, nil
}

// The setting keys services/worker writes for a mount, which are the
// vocabulary's own attribute paths.
const (
	settingQuota    = specs.VolumeKeyQuota
	settingInterval = specs.VolumeCheckpointKeyInterval
)

// Settings is every attribute path this adapter reads off a grant.
//
// IT IS EXPORTED SO THE ROOT CAN HOLD IT TO THE VOCABULARY. A renamed attribute
// does not fail here: the key is absent, the read is a zero value, and a
// declared interval silently becomes the default. node/settings_test.go walks
// this list against the schema, and its own doc carries the post-mortem that
// test came out of.
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

	// A READ THAT FAILED IS READ AS "NOTHING WAS RECORDED", and that is the one
	// place this package's own argument is not held: a store that cannot be
	// read yields an empty directory rather than a refusal, which is the shape
	// the rest of this file exists to prevent. It is stated rather than fixed
	// because the fix is a mount that fails on a transient read, and which of
	// those is worse is a call the operator's own retry policy makes.
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
	unpacked, err := untar(content, staging, v.floor, v.sync)
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
		n, err := tarInto(write, held.dir)
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

// tarInto walks a directory into a tar stream and reports the total content
// size — which is what the quota is measured against.
//
// SYMLINKS ARE WRITTEN AS SYMLINKS AND NOT FOLLOWED. Following one would copy
// whatever it points at into the checkpoint, and a link out of the directory is
// a link out of the tenant's data.
//
// EACH ENTRY IS OPENED ONCE, THROUGH AN os.Root ON THE DIRECTORY, AND DESCRIBED
// BY ITS OWN STAT. The workload keeps writing while this runs, so a name can
// change between being listed and being read: what the checkpoint holds is the
// entry that was opened, and nothing from outside the directory.
//
// A SOCKET IN THE DIRECTORY FAILS THE WHOLE CHECKPOINT, and that is stated
// rather than worked around: archive/tar refuses to make a header for one, and
// this walk asks it about every entry. A database whose socket lives in its data
// directory therefore has a volume that never checkpoints while it is running,
// and what says so is the warning the pass makes every interval. Skipping the
// entry instead would mean a checkpoint that quietly holds less than the
// directory did, which is the failure mode this package is here to prevent.
func tarInto(w io.Writer, dir string) (int64, error) {
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
		n, isDir, err := writeEntry(archive, root, name)
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
func writeEntry(archive *tar.Writer, root *os.Root, name string) (int64, bool, error) {
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
	n, err := io.CopyN(archive, opened.content, header.Size)
	return n, false, err
}

// untar unpacks a checkpoint into a directory, and answers how many bytes of
// files it unpacked.
//
// EVERY ENTRY IS CHECKED AGAINST THE DESTINATION. A tar is bytes from a store,
// and an entry named `../../etc` would write outside the volume — the oldest
// archive bug there is, and this one unpacks with the node's own privileges.
//
// AND EVERY ENTRY IS MADE THROUGH AN os.Root ON IT, because a name says nothing
// about what is already on disk along its path: a link the archive made earlier
// sits in the path of every entry after it. The root refuses to resolve a path
// out of the volume, whatever links lie along the way.
//
// AND THE DISK IT LANDS ON KEEPS ITS RESERVE, which is what bounds it. A
// checkpoint can hold more than the volume's quota, and an entry whose holes
// the reader fills with zeros can name terabytes in an archive of a few bytes.
// So the filesystem is read before each entry is made and each chunk written,
// and one that would leave less free than the floor fails the restore. A run of
// zeros is left as a hole, so a sparse file stays sparse and only what lands on
// disk is counted against the reserve.
//
// AND EVERYTHING IS SYNCED, each file once it is written and each directory
// once nothing more is made in it, with the mode it ends with.
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
