package raft

import (
	"context"
	"errors"
	"fmt"
	"math"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	"github.com/libp2p/go-libp2p/core/peer"
	etcdraft "go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
)

// engineLog is one group's raft log as the engine keeps it: in memory, where raft
// reads it, and in the node's datastore, so a restart finds what it had. The
// engine writes a cycle's worth of every group into one batch, committed before any
// message of that cycle is sent.
type engineLog struct {
	*etcdraft.MemoryStorage
	store  ds.Batching
	prefix string

	// persisted is the highest index on disk, so entries raft replaced with a
	// shorter tail do not survive a restart.
	persisted uint64

	// formation is the formation the group is bound to, nil while it is bound to
	// none. The loop alone touches it.
	formation []byte

	// abstaining is a group that joined a formation holding nothing and sits its
	// elections out until a leader has caught it up; pair is the one voter it votes
	// for meanwhile, in a group of two. The loop alone touches them.
	abstaining bool
	pair       peer.ID
}

func (l *engineLog) hardStateKey() ds.Key { return ds.NewKey(l.prefix + "/hardstate") }
func (l *engineLog) snapshotKey() ds.Key  { return ds.NewKey(l.prefix + "/snapshot") }
func (l *engineLog) formationKey() ds.Key { return ds.NewKey(l.prefix + "/formation") }
func (l *engineLog) abstainKey() ds.Key   { return ds.NewKey(l.prefix + "/abstain") }

// entryKey pads the index to 20 digits: byte order is the only order a scan has.
func (l *engineLog) entryKey(index uint64) ds.Key {
	return ds.NewKey(fmt.Sprintf("%s/log/%020d", l.prefix, index))
}

// openEngineLog reads back what a group kept under prefix: its snapshot, its hard
// state, and the entries after the snapshot, in index order.
func openEngineLog(ctx context.Context, store ds.Batching, prefix string) (*engineLog, error) {
	l := &engineLog{MemoryStorage: etcdraft.NewMemoryStorage(), store: store, prefix: prefix}

	if raw, err := store.Get(ctx, l.formationKey()); err == nil {
		l.formation = raw
	} else if !errors.Is(err, ds.ErrNotFound) {
		return nil, err
	}

	if raw, err := store.Get(ctx, l.abstainKey()); err == nil {
		l.abstaining, l.pair = true, peer.ID(raw)
	} else if !errors.Is(err, ds.ErrNotFound) {
		return nil, err
	}

	if raw, err := store.Get(ctx, l.snapshotKey()); err == nil {
		snap := &pb.Snapshot{}
		if err := proto.Unmarshal(raw, snap); err != nil {
			return nil, fmt.Errorf("raft: %s: reading its snapshot: %w", prefix, err)
		}
		if err := l.ApplySnapshot(snap); err != nil {
			return nil, fmt.Errorf("raft: %s: restoring its snapshot: %w", prefix, err)
		}
		l.persisted = snap.GetMetadata().GetIndex()
	} else if !errors.Is(err, ds.ErrNotFound) {
		return nil, err
	}

	if raw, err := store.Get(ctx, l.hardStateKey()); err == nil {
		hs := &pb.HardState{}
		if err := proto.Unmarshal(raw, hs); err != nil {
			return nil, fmt.Errorf("raft: %s: reading its hard state: %w", prefix, err)
		}
		if err := l.SetHardState(hs); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, ds.ErrNotFound) {
		return nil, err
	}

	results, err := store.Query(ctx, query.Query{Prefix: l.prefix + "/log", Orders: []query.Order{query.OrderByKey{}}})
	if err != nil {
		return nil, err
	}
	defer results.Close()
	first, _ := l.FirstIndex()
	var entries []*pb.Entry
	for r := range results.Next() {
		if r.Error != nil {
			return nil, r.Error
		}
		e := &pb.Entry{}
		if err := proto.Unmarshal(r.Value, e); err != nil {
			return nil, fmt.Errorf("raft: %s: reading entry %s: %w", prefix, r.Key, err)
		}
		if e.GetIndex() >= first {
			entries = append(entries, e)
		}
		if e.GetIndex() > l.persisted {
			l.persisted = e.GetIndex()
		}
	}
	if len(entries) > 0 {
		if err := l.Append(entries); err != nil {
			return nil, fmt.Errorf("raft: %s: replaying its log: %w", prefix, err)
		}
	}
	return l, nil
}

// bind writes formation down before the group takes part in it: a binding a crash
// could take back would let the group take another formation's entries.
func (l *engineLog) bind(ctx context.Context, formation []byte) error {
	if err := l.store.Put(ctx, l.formationKey(), formation); err != nil {
		return err
	}
	l.formation = formation
	return nil
}

// bindStanding binds a group that holds nothing to formation, at the term floor
// with a vote for no member, sitting its elections out if s says so: all of it in
// one batch, synced before the group takes any message.
func (l *engineLog) bindStanding(ctx context.Context, formation []byte, s standing) error {
	hs := &pb.HardState{Term: proto.Uint64(s.floor), Vote: proto.Uint64(noVote), Commit: proto.Uint64(0)}
	raw, err := proto.Marshal(hs)
	if err != nil {
		return err
	}
	b, err := l.store.Batch(ctx)
	if err != nil {
		return err
	}
	if err := b.Put(ctx, l.formationKey(), formation); err != nil {
		return err
	}
	if err := b.Put(ctx, l.hardStateKey(), raw); err != nil {
		return err
	}
	if s.abstain {
		if err := b.Put(ctx, l.abstainKey(), []byte(s.pair)); err != nil {
			return err
		}
	}
	if err := b.Commit(ctx); err != nil {
		return err
	}
	if err := l.store.Sync(ctx, ds.NewKey(l.prefix)); err != nil {
		return err
	}
	if err := l.SetHardState(hs); err != nil {
		return err
	}
	l.formation, l.abstaining, l.pair = formation, s.abstain, s.pair
	return nil
}

// caughtUp ends the group's abstention. Losing the deletion to a crash only has it
// abstain again until the next leader catches it up.
func (l *engineLog) caughtUp(ctx context.Context) error {
	if err := l.store.Delete(ctx, l.abstainKey()); err != nil {
		return err
	}
	l.abstaining, l.pair = false, ""
	return nil
}

// abstainsFrom says the group takes no part in an election from: it is abstaining,
// and from is not the one voter it votes for.
func (l *engineLog) abstainsFrom(from peer.ID) bool {
	return l.abstaining && from != l.pair
}

// pristine says the group holds nothing of any formation: no entry and no
// snapshot. What it voted for is not something it holds.
func (l *engineLog) pristine() bool {
	last, _ := l.LastIndex()
	snap, _ := l.Snapshot()
	return last == 0 && etcdraft.IsEmptySnap(snap)
}

// holdsNoData says the log holds no write: no entry but membership changes and the
// empty entries leaders open their terms with, behind no snapshot or one taken of
// such a log. Who was a member is all such a group would lose.
func (l *engineLog) holdsNoData() bool {
	snap, _ := l.Snapshot()
	if !etcdraft.IsEmptySnap(snap) {
		data := &raftv2.SnapshotData{}
		if proto.Unmarshal(snap.GetData(), data) != nil || !data.GetMembershipOnly() {
			return false
		}
	}
	first, _ := l.FirstIndex()
	last, _ := l.LastIndex()
	if last < first {
		return true
	}
	entries, err := l.Entries(first, last+1, math.MaxUint64)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.GetType() == pb.EntryType_EntryNormal && len(e.GetData()) > 0 {
			return false
		}
	}
	return true
}

// save writes what raft asked to keep from one Ready into b. Memory follows only
// once b commits (kept), so a write the store refused can be made again.
func (l *engineLog) save(ctx context.Context, b ds.Batch, snap *pb.Snapshot, hs *pb.HardState, entries []*pb.Entry) error {
	if snap != nil && !etcdraft.IsEmptySnap(snap) {
		raw, err := proto.Marshal(snap)
		if err != nil {
			return err
		}
		if err := b.Put(ctx, l.snapshotKey(), raw); err != nil {
			return err
		}
	}
	if len(entries) > 0 {
		last := entries[len(entries)-1].GetIndex()
		for _, e := range entries {
			raw, err := proto.Marshal(e)
			if err != nil {
				return err
			}
			if err := b.Put(ctx, l.entryKey(e.GetIndex()), raw); err != nil {
				return err
			}
		}
		// A SHORTER TAIL replaced a longer one: what it replaced is gone from disk
		// too, or a restart would read it back as the log.
		for i := last + 1; i <= l.persisted; i++ {
			if err := b.Delete(ctx, l.entryKey(i)); err != nil {
				return err
			}
		}
	}
	if hs != nil && !etcdraft.IsEmptyHardState(hs) {
		raw, err := proto.Marshal(hs)
		if err != nil {
			return err
		}
		if err := b.Put(ctx, l.hardStateKey(), raw); err != nil {
			return err
		}
	}
	return nil
}

// kept brings memory to what a committed save wrote.
func (l *engineLog) kept(snap *pb.Snapshot, hs *pb.HardState, entries []*pb.Entry) error {
	if snap != nil && !etcdraft.IsEmptySnap(snap) {
		if err := l.ApplySnapshot(snap); err != nil && !errors.Is(err, etcdraft.ErrSnapOutOfDate) {
			return err
		}
	}
	if len(entries) > 0 {
		if err := l.Append(entries); err != nil {
			return err
		}
		l.persisted = entries[len(entries)-1].GetIndex()
	}
	if hs != nil && !etcdraft.IsEmptyHardState(hs) {
		if err := l.SetHardState(hs); err != nil {
			return err
		}
	}
	return nil
}

// compact keeps a snapshot at index, holding data, and drops the log before it but
// for trailing entries a slow follower may still need.
func (l *engineLog) compact(ctx context.Context, index uint64, cs *pb.ConfState, data []byte, trailing uint64) error {
	snap, err := l.CreateSnapshot(index, cs, data)
	if err != nil {
		if errors.Is(err, etcdraft.ErrSnapOutOfDate) {
			return nil
		}
		return err
	}
	raw, err := proto.Marshal(snap)
	if err != nil {
		return err
	}
	if err := l.store.Put(ctx, l.snapshotKey(), raw); err != nil {
		return err
	}
	if index <= trailing {
		return nil
	}
	upto := index - trailing
	first, _ := l.FirstIndex()
	if upto <= first {
		return nil
	}
	if err := l.Compact(upto); err != nil && !errors.Is(err, etcdraft.ErrCompacted) {
		return err
	}
	b, err := l.store.Batch(ctx)
	if err != nil {
		return err
	}
	// THROUGH upto: memory keeps its term as the compacted log's base, and the disk
	// keeps nothing at or before it, or each compaction would leave one behind.
	for i := max(first-1, 1); i <= upto; i++ {
		if err := b.Delete(ctx, l.entryKey(i)); err != nil {
			return err
		}
	}
	return b.Commit(ctx)
}

// wipe deletes everything the group kept, so it starts again as a node that never
// ran it.
func (l *engineLog) wipe(ctx context.Context) error {
	return wipePrefixes(ctx, l.store, l.prefix)
}

// wipePrefixes deletes every key under prefixes in one batch: all of them go, or
// none does, so a crash or a refusal never leaves part of what they held.
func wipePrefixes(ctx context.Context, store ds.Batching, prefixes ...string) error {
	var keys []ds.Key
	for _, prefix := range prefixes {
		results, err := store.Query(ctx, query.Query{Prefix: prefix, KeysOnly: true})
		if err != nil {
			return err
		}
		for r := range results.Next() {
			if r.Error != nil {
				results.Close()
				return r.Error
			}
			keys = append(keys, ds.NewKey(r.Key))
		}
		results.Close()
	}
	b, err := store.Batch(ctx)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := b.Delete(ctx, k); err != nil {
			return err
		}
	}
	return b.Commit(ctx)
}
