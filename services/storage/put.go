package storage

import (
	"context"
	"fmt"
	"io"
	"sync"

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
	replicas, barrier, known := s.fleet.read(ctx)
	if !known {
		return written{}, fmt.Errorf("%w: this node has never learned how many copies the cloud promises", ErrUnderHeld)
	}

	root, size, blocks, err := s.bytes.Add(ctx, content)
	if err != nil {
		return written{}, fmt.Errorf("storage: storing the bytes here: %w", err)
	}
	stamp, err := s.clock.Now()
	if err != nil {
		return written{}, err
	}

	holders, err := blockHolders(root.String(), s.placement(), replicas)
	if err != nil {
		return written{}, err
	}
	held := s.acks(ctx, holders, func(ctx context.Context, holder string) error {
		if holder == s.self {
			return s.keep(ctx, root, blocks, a, name, stamp)
		}
		return s.askHold(ctx, holder, root, a, name, stamp)
	})
	if need := min(barrier, len(holders)); held < need {
		return written{}, fmt.Errorf("%w: %d of %d took %s, and the cloud promises %d", ErrUnderHeld, held, len(holders), root, need)
	}

	rec := VersionRecord{
		RootCID: root.String(), Size: size, ContentType: o.ContentType, Metadata: o.Metadata,
		CreatedMS: uint64(s.now().UnixMilli()), Writer: s.self,
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

// remove writes a tombstone, acknowledged like a put.
func (s *server) remove(ctx context.Context, a Address, name string) (Version, error) {
	replicas, barrier, known := s.fleet.read(ctx)
	if !known {
		return Version{}, fmt.Errorf("%w: this node has never learned how many copies the cloud promises", ErrUnderHeld)
	}
	stamp, err := s.clock.Now()
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
	appended := s.acks(ctx, holders, func(ctx context.Context, holder string) error {
		if holder == s.self {
			return s.appendHere(ctx, a, name, stamp, rec)
		}
		return s.askAppend(ctx, holder, a, name, stamp, rec)
	})
	if need := min(barrier, len(holders)); appended < need {
		return fmt.Errorf("%w: %d of %d index holders wrote %s's row, and the cloud promises %d", ErrUnderHeld, appended, len(holders), a, need)
	}
	return nil
}

// acks asks every holder at once and counts the ones that answered yes.
func (s *server) acks(ctx context.Context, holders []string, ask func(context.Context, string) error) int {
	var (
		wg sync.WaitGroup
		mu sync.Mutex
		n  int
	)
	for _, h := range holders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ask(ctx, h); err != nil {
				s.logf("storage: %s did not take it: %v", h, err)
				return
			}
			mu.Lock()
			n++
			mu.Unlock()
		}()
	}
	wg.Wait()
	return n
}

// keep records blocks this node holds for a version.
func (s *server) keep(ctx context.Context, root cid.Cid, blocks []cid.Cid, a Address, name string, stamp uint64) error {
	for _, b := range blocks {
		if err := s.local.Imported(ctx, b); err != nil {
			return err
		}
	}
	return s.local.Reference(ctx, root, a, name, stamp)
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
	i, ok, err := s.indexOf(ctx, a)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("storage: this node does not hold %s's index", a)
	}
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
