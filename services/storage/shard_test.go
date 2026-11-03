package storage

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/placement"
)

func members(n int) []placement.Member {
	out := make([]placement.Member, n)
	for i := range out {
		out[i] = placement.Member{ID: fmt.Sprintf("12D3KooW%04d", i), Accepts: true}
	}
	return out
}

func bucket(t *testing.T, project, env, name string) Bucket {
	t.Helper()
	b, err := NewBucket(project, env, name)
	require.NoError(t, err)
	return b
}

func instance(t *testing.T, project, env, name, inst string) Address {
	t.Helper()
	a, err := NewAddress(project, env, name, inst)
	require.NoError(t, err)
	return a
}

func TestShardOfIsDeterministicAndInRange(t *testing.T) {
	b := bucket(t, "proj", "prod", "receipts")
	first, err := shardOf(b)
	require.NoError(t, err)
	for i := 0; i < 100; i++ {
		again, err := shardOf(bucket(t, "proj", "prod", "receipts"))
		require.NoError(t, err)
		require.Equal(t, first, again)
	}
	require.Less(t, first, uint16(Shards))
}

func TestEveryInstanceOfABucketSharesItsShard(t *testing.T) {
	want, err := shardOf(bucket(t, "proj", "prod", "receipts"))
	require.NoError(t, err)
	for i := 0; i < 50; i++ {
		a := instance(t, "proj", "prod", "receipts", fmt.Sprintf("shop-%d", i))
		got, err := shardOf(a.Bucket())
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

func TestAWhaleProjectSpreadsOverShards(t *testing.T) {
	seen := map[uint16]int{}
	for i := 0; i < 2000; i++ {
		s, err := shardOf(bucket(t, "whale", "prod", fmt.Sprintf("bucket-%d", i)))
		require.NoError(t, err)
		seen[s]++
	}
	require.Greater(t, len(seen), Shards*3/4, "a whale's buckets must not pile into a few shards: %d used", len(seen))

	// And a different environment of the same bucket is a different shard
	// unit, because the environment is in the key.
	prod, err := shardOf(bucket(t, "whale", "prod", "bucket-0"))
	require.NoError(t, err)
	staging, err := shardOf(bucket(t, "whale", "staging", "bucket-0"))
	require.NoError(t, err)
	require.NotEqual(t, prod, staging)
}

func TestADevCloudOpensAsManyShardsAsItHasBuckets(t *testing.T) {
	ms := members(3)
	var buckets []Address
	for i := 0; i < 5; i++ {
		buckets = append(buckets, instance(t, "proj", "prod", fmt.Sprintf("b%d", i), "main"))
	}

	total := 0
	for _, m := range ms {
		desired, err := desiredShards(buckets, ms, 3, m.ID)
		require.NoError(t, err)
		require.LessOrEqual(t, len(desired), 5, "%s wants %d shards for five buckets", m.ID, len(desired))
		total += len(desired)
	}
	// R=3 over a fleet of 3 means every node holds every occupied shard, and
	// there are at most five of them.
	occupied, err := occupiedShards(buckets)
	require.NoError(t, err)
	require.Equal(t, len(occupied)*3, total)
	require.LessOrEqual(t, len(occupied), 5)
}

func TestDesiredNeedsBothConditions(t *testing.T) {
	ms := members(30)
	var buckets []Address
	for i := 0; i < 400; i++ {
		buckets = append(buckets, instance(t, "proj", "prod", fmt.Sprintf("b%d", i), "main"))
	}
	occupied, err := occupiedShards(buckets)
	require.NoError(t, err)

	self := ms[7].ID
	desired, err := desiredShards(buckets, ms, 3, self)
	require.NoError(t, err)

	for s := range desired {
		_, isOccupied := occupied[s]
		require.True(t, isOccupied, "shard %d is empty and must not be opened", s)
		require.True(t, placement.Holds(indexResource(s), ms, 3, self), "shard %d is not this node's", s)
	}
	require.Less(t, len(desired), len(occupied), "a node in a fleet of thirty must not hold every occupied shard")
	require.Greater(t, len(desired), 0)

	// An empty catalog opens nothing at all, whatever HRW says.
	none, err := desiredShards(nil, ms, 3, self)
	require.NoError(t, err)
	require.Empty(t, none)
}

func TestOccupiedShardsRefusesAnInvalidAddress(t *testing.T) {
	_, err := occupiedShards([]Address{{}})
	require.ErrorIs(t, err, ErrNoAddress)
	_, err = desiredShards([]Address{{}}, members(3), 3, "x")
	require.ErrorIs(t, err, ErrNoAddress)
}

func TestIndexAndBytePlacementAreIndependent(t *testing.T) {
	ms := members(12)
	b := bucket(t, "proj", "prod", "receipts")
	shard, err := shardOf(b)
	require.NoError(t, err)
	idx := indexHolders(shard, ms, 3)
	require.Len(t, idx, 3)

	sameAsIndex, distinctSets := 0, map[string]struct{}{}
	for i := 0; i < 500; i++ {
		holders, err := blockHolders(fmt.Sprintf("bafyobject%d", i), ms, 3)
		require.NoError(t, err)
		require.Len(t, holders, 3)
		distinctSets[fmt.Sprint(holders)] = struct{}{}
		if fmt.Sprint(holders) == fmt.Sprint(idx) {
			sameAsIndex++
		}
	}
	require.Greater(t, len(distinctSets), 50, "an object's bytes must spread over the fleet: %d distinct sets", len(distinctSets))
	require.Less(t, sameAsIndex, 30, "byte placement tracks index placement: %d/500", sameAsIndex)
}

func TestBlockHoldersRefusesAnObjectWithNoRoot(t *testing.T) {
	_, err := blockHolders("", members(3), 3)
	require.ErrorIs(t, err, ErrNotThisServicesRow)
	_, err = blocksResource("")
	require.ErrorIs(t, err, ErrNotThisServicesRow)
}

func TestIndexResourceNamesOneShard(t *testing.T) {
	require.Equal(t, "index/s000", indexResource(0))
	require.Equal(t, "index/s042", indexResource(42))
	require.Equal(t, "index/s255", indexResource(255))

	seen := map[string]struct{}{}
	for s := 0; s < Shards; s++ {
		r := indexResource(uint16(s))
		_, dup := seen[r]
		require.False(t, dup)
		seen[r] = struct{}{}
	}
}

func TestShardSetOpensWhatIsDesiredAndClosesWhatIsNot(t *testing.T) {
	ctx := context.Background()
	opener := newCountingOpener()
	set, err := newShardSet(opener)
	require.NoError(t, err)

	opened, closed, err := set.reconcile(ctx, shardSetOf(1, 2, 3), set.mark())
	require.NoError(t, err)
	require.Equal(t, []uint16{1, 2, 3}, opened)
	require.Empty(t, closed)
	require.Equal(t, []uint16{1, 2, 3}, set.held())

	// Idempotent: reconciling to the same answer opens nothing.
	opened, closed, err = set.reconcile(ctx, shardSetOf(1, 2, 3), set.mark())
	require.NoError(t, err)
	require.Empty(t, opened)
	require.Empty(t, closed)

	opened, closed, err = set.reconcile(ctx, shardSetOf(2, 4), set.mark())
	require.NoError(t, err)
	require.Equal(t, []uint16{4}, opened)
	require.Equal(t, []uint16{1, 3}, closed)
	require.Equal(t, []uint16{2, 4}, set.held())

	require.NoError(t, set.closeAll(ctx))
	require.Empty(t, set.held())
}

func TestAFailedOpenClosesNothing(t *testing.T) {
	ctx := context.Background()
	opener := newCountingOpener()
	set, err := newShardSet(opener)
	require.NoError(t, err)

	_, _, err = set.reconcile(ctx, shardSetOf(1, 2), set.mark())
	require.NoError(t, err)

	opener.failOpen[9] = errInjected
	_, _, err = set.reconcile(ctx, shardSetOf(9), set.mark())
	require.ErrorIs(t, err, errInjected)
	require.Equal(t, []uint16{1, 2}, set.held(), "the shards this node holds are still open")
	require.Empty(t, opener.closed)
}

func TestAFailedCloseStillDropsTheShard(t *testing.T) {
	ctx := context.Background()
	opener := newCountingOpener()
	set, err := newShardSet(opener)
	require.NoError(t, err)

	_, _, err = set.reconcile(ctx, shardSetOf(1, 2), set.mark())
	require.NoError(t, err)

	opener.failShut[1] = errInjected
	_, closed, err := set.reconcile(ctx, shardSetOf(2), set.mark())
	require.ErrorIs(t, err, errInjected, "and the failure is reported rather than swallowed")
	require.Equal(t, []uint16{1}, closed)
	require.Equal(t, []uint16{2}, set.held())
}

func TestTheIndexForABucketIsOnlyThereWhenTheShardIs(t *testing.T) {
	ctx := context.Background()
	opener := newCountingOpener()
	set, err := newShardSet(opener)
	require.NoError(t, err)

	b := bucket(t, "proj", "prod", "receipts")
	shard, err := shardOf(b)
	require.NoError(t, err)

	_, _, ok, err := set.hold(ctx, b, false)
	require.NoError(t, err)
	require.False(t, ok, "a bucket whose shard is not open has no index here")

	_, _, err = set.reconcile(ctx, shardSetOf(shard), set.mark())
	require.NoError(t, err)
	idx, release, ok, err := set.hold(ctx, b, false)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, idx.store)
	release()

	_, _, _, err = set.hold(ctx, Bucket{}, true)
	require.ErrorIs(t, err, ErrNoAddress)

	next := (shard + 1) % Shards
	opener.failOpen[next] = errInjected
	other := bucketIn(t, next)
	_, _, _, err = set.hold(ctx, other, true)
	require.ErrorIs(t, err, errInjected)
}

// A WRITE RACING A SWEEP keeps the shard it is appending to: the sweep that read
// before the write opened it cannot close it, held or released, and the one after
// the write was recorded may, once nothing holds it.
func TestAShardAWriteOpenedIsNotClosedByASweepThatReadBeforeIt(t *testing.T) {
	ctx := context.Background()
	opener := newCountingOpener()
	set, err := newShardSet(opener)
	require.NoError(t, err)
	b := bucket(t, "proj", "prod", "logs")
	shard, err := shardOf(b)
	require.NoError(t, err)

	read := set.mark()
	_, release, ok, err := set.hold(ctx, b, true)
	require.NoError(t, err)
	require.True(t, ok)

	_, closed, err := set.reconcile(ctx, shardSetOf(), read)
	require.NoError(t, err)
	require.Empty(t, closed, "closed under the write appending to it")
	release()
	release()
	_, closed, err = set.reconcile(ctx, shardSetOf(), read)
	require.NoError(t, err)
	require.Empty(t, closed, "closed by a sweep that decided before the write was recorded")

	// HELD ACROSS A LATER SWEEP, still not closed under its user.
	_, again, ok, err := set.hold(ctx, b, false)
	require.NoError(t, err)
	require.True(t, ok)
	_, closed, err = set.reconcile(ctx, shardSetOf(), set.mark())
	require.NoError(t, err)
	require.Empty(t, closed)
	again()

	_, closed, err = set.reconcile(ctx, shardSetOf(), set.mark())
	require.NoError(t, err)
	require.Equal(t, []uint16{shard}, closed, "idle and undesired, it closes")
}

// bucketIn is a bucket whose index lives in shard.
func bucketIn(t *testing.T, shard uint16) Bucket {
	t.Helper()
	for i := 0; ; i++ {
		b := bucket(t, "proj", "prod", fmt.Sprintf("b%d", i))
		if got, err := shardOf(b); err == nil && got == shard {
			return b
		}
	}
}

func TestAShardSetNeedsAnOpener(t *testing.T) {
	_, err := newShardSet(nil)
	require.Error(t, err)
}

func shardSetOf(ids ...uint16) map[uint16]struct{} {
	out := make(map[uint16]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out
}
