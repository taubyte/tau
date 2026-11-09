package storage

import (
	"context"
	"slices"
	"time"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/pkg/placement"
	storagev2 "github.com/taubyte/tau/pkg/proto/gen/tau/storage/v2"
)

// A SHARD PLACEMENT MOVES OFF A NODE STAYS OPEN THERE UNTIL IT IS HANDED OFF: until
// as many of the holders placement now names as a write is acknowledged on have
// merged everything this node's copy holds. Closed before, the copy stops
// broadcasting what it holds, and a holder that never heard it has nothing to fetch:
// rows only this node had would be served by nobody. A copy that cannot say what it
// holds is kept, never assumed handed off.

// replica is a shard store that can say what it merged, and whether it covers what
// another merged. A pkg/kvdb store is one.
type replica interface {
	Merged() []kvdb.Span
	Covers(spans []kvdb.Span) bool
}

// coversWait bounds asking one holder whether it covers a copy.
const coversWait = 10 * time.Second

// handedOff says enough of the holders placement names for shard have merged this
// node's copy of it.
func (s *server) handedOff(ctx context.Context, shard uint16, members []placement.Member, replicas, barrier int) bool {
	store, open := s.shards.storeOf(shard)
	if !open {
		return true
	}
	r, ok := store.(replica)
	if !ok {
		return false
	}
	spans := r.Merged()
	holders := indexHolders(shard, members, replicas)
	need := max(1, min(barrier, len(holders)))
	covered := 0
	for _, h := range holders {
		if h == s.self || !s.covers(ctx, h, shard, spans) {
			continue
		}
		if covered++; covered >= need {
			return true
		}
	}
	return false
}

// covers asks holder whether its copy of shard covers spans. One that cannot be
// asked does not.
func (s *server) covers(ctx context.Context, holder string, shard uint16, spans []kvdb.Span) bool {
	svc, err := s.peer(holder)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, coversWait)
	defer cancel()
	res, err := svc.Covers(ctx, connect.NewRequest(&storagev2.CoversRequest{Shard: uint32(shard), Spans: wireSpans(spans)}))
	return err == nil && res.Msg.GetCovered()
}

// answerCovers says this node's copy of shard covers spans: false when it has none.
//
// A SHARD PLACEMENT PUTS HERE IS ADOPTED WHEN ITS OLD HOLDER ASKS, open or not: the
// catalog may never name a bucket in it here, and a node every write reached before
// placement moved the shard is the only one that knows it holds anything. Adopted, it
// stays open while placement puts it here (reconcile), and merges what it holds.
func (s *server) answerCovers(ctx context.Context, shard uint16, spans []kvdb.Span) bool {
	store, open := s.shards.storeOf(shard)
	if !open {
		var err error
		if store, open, err = s.adoptShard(ctx, shard); err != nil {
			s.logf("storage: adopting shard %d: %v", shard, err)
		}
	}
	r, ok := store.(replica)
	return open && ok && r.Covers(spans)
}

// adoptShard opens shard here and records it, when placement puts it here.
func (s *server) adoptShard(ctx context.Context, shard uint16) (Datastore, bool, error) {
	replicas, _, err := s.promise(ctx)
	if err != nil {
		return nil, false, err
	}
	if !slices.Contains(indexHolders(shard, s.placement(), replicas), s.self) {
		return nil, false, nil
	}
	if err := s.local.Adopt(ctx, shard); err != nil {
		return nil, false, err
	}
	store, err := s.shards.adopt(ctx, shard)
	return store, err == nil, err
}

func wireSpans(spans []kvdb.Span) []*storagev2.Span {
	out := make([]*storagev2.Span, 0, len(spans))
	for _, sp := range spans {
		out = append(out, &storagev2.Span{Replica: sp.Replica, Lo: sp.Lo, Hi: sp.Hi})
	}
	return out
}

func spansOf(wire []*storagev2.Span) []kvdb.Span {
	out := make([]kvdb.Span, 0, len(wire))
	for _, sp := range wire {
		out = append(out, kvdb.Span{Replica: sp.GetReplica(), Lo: sp.GetLo(), Hi: sp.GetHi()})
	}
	return out
}
