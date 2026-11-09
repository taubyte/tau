package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/ipfs/go-cid"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	storagev2 "github.com/taubyte/tau/pkg/proto/gen/tau/storage/v2"
)

// A write is acknowledged once K of the nodes placement names hold it, and only then
// is it a version. A fleet with fewer storage nodes than K is its own bar, as for the
// database service's floor: every holder there is, which on one node is that node. The bytes are held first, by the root's holders fetching them from
// this node; the row second, appended by each of the shard's holders. Every holder
// appends the same row, keyed by this node as its writer, so they agree without
// coordination, and K of them having it is what survives one going away.

// written is one acknowledged version.
type written struct {
	version Version
	holders int
}

// put stores content as the next version of name.
func (s *server) put(ctx context.Context, a Address, name string, o Object, content io.Reader) (written, error) {
	replicas, barrier, err := s.promise(ctx)
	if err != nil {
		return written{}, err
	}

	root, size, blocks, err := s.bytes.Add(ctx, content)
	if err != nil {
		return written{}, fmt.Errorf("storage: storing the bytes here: %w", err)
	}
	stamp, err := s.stamp(ctx)
	if err != nil {
		return written{}, err
	}

	holders, err := blockHolders(root.String(), s.placement(), replicas)
	if err != nil {
		return written{}, err
	}
	// A WRITER PLACEMENT DOES NOT NAME KEEPS ITS COPY TOO, while the holders fetch
	// it, and until the repair loop finds them all holding it (repair.go): kept,
	// no sweep takes a block they still fetch from it, and the copy is a misplaced
	// one the loop drops rather than bytes nothing indexes.
	mine := make(chan error, 1)
	if slices.Contains(holders, s.self) {
		mine <- nil
	} else {
		go func() { mine <- s.keep(ctx, root, blocks, a, name, stamp) }()
	}
	need := max(1, min(barrier, len(holders)))
	held := s.acks(ctx, holders, need, func(ctx context.Context, holder string) error {
		if holder == s.self {
			return s.keep(ctx, root, blocks, a, name, stamp)
		}
		return s.askHold(ctx, holder, root, a, name, stamp)
	})
	if err := <-mine; err != nil {
		s.logf("storage: keeping the copy of %s this node wrote: %v", root, err)
	}
	if held < need {
		return written{}, fmt.Errorf("%w: %d of %d took %s, and the region promises %d", ErrUnderHeld, held, len(holders), root, need)
	}

	rec := VersionRecord{
		RootCID: root.String(), Size: size, ContentType: o.ContentType, Metadata: o.Metadata,
		CreatedMS: uint64(s.now().UnixMilli()), Writer: s.self, Kept: o.Versioned,
	}
	if o.Expire > 0 {
		rec.ExpiresMS = rec.CreatedMS + uint64(o.Expire.Milliseconds())
	}
	if err := s.appendEverywhere(ctx, a, name, stamp, rec, replicas, barrier); err != nil {
		return written{}, err
	}
	// The catalog is a hint: the shard's holders already Saw the instance.
	if r, ok := s.catalog.(catalogRecorder); ok {
		if err := r.Record(ctx, a); err != nil {
			s.logf("storage: cataloguing %s: %v", a, err)
		}
	}
	v, err := versionOf(a, name, stamp, rec)
	return written{version: v, holders: held}, err
}

// remove writes a tombstone, acknowledged like a put. It reads what it deletes first,
// so the tombstone is stamped after it: one stamped on a clock behind the version's
// would order first and delete nothing.
func (s *server) remove(ctx context.Context, a Address, name string) (Version, error) {
	replicas, barrier, err := s.promise(ctx)
	if err != nil {
		return Version{}, err
	}
	if _, err := s.current(ctx, a, name, false); err != nil && !errors.Is(err, ErrNotFound) {
		return Version{}, fmt.Errorf("storage: reading what the delete supersedes: %w", err)
	}
	stamp, err := s.stamp(ctx)
	if err != nil {
		return Version{}, err
	}
	rec := VersionRecord{CreatedMS: uint64(s.now().UnixMilli()), Writer: s.self, Tombstone: true}
	if err := s.appendEverywhere(ctx, a, name, stamp, rec, replicas, barrier); err != nil {
		return Version{}, err
	}
	return versionOf(a, name, stamp, rec)
}

// versionOf is the version a row is, read back the way a holder reads it.
func versionOf(a Address, name string, stamp uint64, rec VersionRecord) (Version, error) {
	key, err := a.versionKey(name, stamp, rec.Writer)
	if err != nil {
		return Version{}, err
	}
	raw, err := rec.encode()
	if err != nil {
		return Version{}, err
	}
	return parseVersion(key, raw)
}

// appendEverywhere appends the row on every holder of the instance's shard and
// returns once barrier of them have it.
func (s *server) appendEverywhere(ctx context.Context, a Address, name string, stamp uint64, rec VersionRecord, replicas, barrier int) error {
	shard, err := shardOf(a.Bucket())
	if err != nil {
		return err
	}
	holders := indexHolders(shard, s.placement(), replicas)
	need := max(1, min(barrier, len(holders)))
	appended := s.acks(ctx, holders, need, func(ctx context.Context, holder string) error {
		if holder == s.self {
			return s.appendHere(ctx, a, name, stamp, rec)
		}
		return s.askAppend(ctx, holder, a, name, stamp, rec)
	})
	if appended < need {
		return fmt.Errorf("%w: %d of %d index holders wrote %s's row, and the cloud promises %d", ErrUnderHeld, appended, len(holders), a, need)
	}
	return nil
}

// straggle is how long a write that has its barrier waits for the holders still
// answering: long enough for a healthy one, so a read right after the write finds
// it everywhere, and no longer, so a holder that stalls costs a write this much.
const straggle = time.Second

// acks asks every holder at once and returns how many took it, once every holder
// has answered, or need have and the rest had straggle to: a holder that stalls
// holds up neither the write nor the others. Each ask runs on the service's
// lifetime rather than the caller's, within holdWait, so the holders still asking
// when the write returns go on taking it.
func (s *server) acks(ctx context.Context, holders []string, need int, ask func(context.Context, string) error) int {
	life := s.life
	if life == nil {
		life = context.WithoutCancel(ctx)
	}
	answers := make(chan bool, len(holders))
	for _, h := range holders {
		go func() {
			actx, cancel := context.WithTimeout(life, holdWait)
			defer cancel()
			err := ask(actx, h)
			if err != nil {
				s.logf("storage: %s did not take it: %v", h, err)
			}
			answers <- err == nil
		}()
	}
	n := 0
	var late <-chan time.Time
	for range holders {
		select {
		case ok := <-answers:
			if ok {
				if n++; n == need {
					late = time.After(straggle)
				}
			}
		case <-late:
			return n
		case <-ctx.Done():
			return n
		}
	}
	return n
}

// keep records blocks this node holds for a version, durably before it says so:
// the writer counts this node toward the copies it promises, and nothing
// replicates a node's own blocks and rows or brings back what a crash took. It goes
// through the sweeper, which refuses it when a sweep took a block meanwhile.
func (s *server) keep(ctx context.Context, root cid.Cid, blocks []cid.Cid, a Address, name string, stamp uint64) error {
	if err := s.sweeper.Keep(ctx, root, blocks, a, name, stamp); err != nil {
		return err
	}
	if err := s.bytes.Sync(ctx); err != nil {
		return fmt.Errorf("storage: syncing %s's blocks: %w", root, err)
	}
	return s.local.Sync(ctx)
}

// hold fetches a root from the node that has it and keeps it.
func (s *server) hold(ctx context.Context, root cid.Cid, from libp2ppeer.ID, a Address, name string, stamp uint64) error {
	blocks, err := s.bytes.Fetch(ctx, root, from)
	if err != nil {
		return fmt.Errorf("storage: fetching %s from %s: %w", root, from, err)
	}
	return s.keep(ctx, root, blocks, a, name, stamp)
}

// appendHere writes one row into this node's copy of the instance's index.
func (s *server) appendHere(ctx context.Context, a Address, name string, stamp uint64, rec VersionRecord) error {
	i, release, ok, err := s.indexOf(ctx, a)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("storage: this node does not hold %s's index", a)
	}
	defer release()
	if err := s.local.Saw(ctx, a); err != nil {
		return err
	}
	if err := s.clock.Observe(stamp); err != nil {
		s.logf("storage: %v", err)
	}
	_, err = i.Append(ctx, a, name, stamp, rec.Writer, rec)
	return err
}

func (s *server) askHold(ctx context.Context, holder string, root cid.Cid, a Address, name string, stamp uint64) error {
	svc, err := s.peer(holder)
	if err != nil {
		return err
	}
	_, err = svc.Hold(ctx, connect.NewRequest(&storagev2.HoldRequest{
		RootCid: root.String(), From: s.self, At: wireAddress(a), Name: []byte(name), Stamp: stamp,
	}))
	return err
}

func (s *server) askAppend(ctx context.Context, holder string, a Address, name string, stamp uint64, rec VersionRecord) error {
	svc, err := s.peer(holder)
	if err != nil {
		return err
	}
	_, err = svc.Append(ctx, connect.NewRequest(&storagev2.AppendRequest{
		At: wireAddress(a), Name: []byte(name), Stamp: stamp, Record: wireRecord(rec),
	}))
	return err
}
