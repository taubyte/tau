package register

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// ctx is the tests' context. Nothing here exercises cancellation — the
// MemStore never blocks — so one background context reads better than the same
// two lines in thirty tests.
var ctx = context.Background()

func newRegister(t *testing.T) (*Register, *MemStore) {
	t.Helper()
	store := NewMemStore()
	r, err := New(WithStore(store))
	require.NoError(t, err)
	return r, store
}

// TestTheRuntimeReadsAPointerAndKeepsTheObject is the whole shape: publishing
// moves a small pointer, and the object it names never changes, so a reader that
// already has it is done.
func TestTheRuntimeReadsAPointerAndKeepsTheObject(t *testing.T) {
	r, _ := newRegister(t)
	compiled := []byte(`{"function":{"checkout":{"execution":{"memory":"32MB"}}}}`)

	id, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled})
	require.NoError(t, err)

	p, err := r.Resolve(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, id, p.Object)
	require.Equal(t, uint64(1), p.Revision)

	got, err := r.Object(ctx, p.Object)
	require.NoError(t, err)
	require.Equal(t, compiled, got)
}

// TestTheSameConfigurationIsTheSameObject. Two identical compilations produce
// one id, which is what makes "I already have this" answerable without asking.
func TestTheSameConfigurationIsTheSameObject(t *testing.T) {
	r, store := newRegister(t)
	compiled := []byte("the same bytes")

	first, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled})
	require.NoError(t, err)
	second, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 2, Compiled: compiled})
	require.NoError(t, err)
	require.Equal(t, first, second)

	// And it was stored once: an object's key is its content.
	objects := 0
	for _, k := range store.Keys() {
		if len(k) > 2 && k[:2] == "o/" {
			objects++
		}
	}
	require.Equal(t, 1, objects, "republishing the same configuration stores nothing new")

	require.Equal(t, IDOf(compiled), first, "the id is the content address, not a counter")
}

// TestDifferentConfigurationsAreDifferentObjects.
func TestDifferentConfigurationsAreDifferentObjects(t *testing.T) {
	r, _ := newRegister(t)

	a, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("memory: 32MB")})
	require.NoError(t, err)
	b, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 2, Compiled: []byte("memory: 64MB")})
	require.NoError(t, err)
	require.NotEqual(t, a, b)

	// The old object is still there, addressable, unchanged — which is what a
	// rollback needs and what a node still running the old revision reads.
	old, err := r.Object(ctx, a)
	require.NoError(t, err)
	require.Equal(t, []byte("memory: 32MB"), old)
}

// TestEnvironmentsAreIndependent. execution/memory is PerEnv() in the schema, so
// staging and prod resolving to the same object would defeat the point.
func TestEnvironmentsAreIndependent(t *testing.T) {
	r, _ := newRegister(t)

	prod, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 5, Compiled: []byte("memory: 64MB")})
	require.NoError(t, err)
	staging, err := r.Publish(ctx, Publication{Project: "shop", Environment: "staging", Revision: 1, Compiled: []byte("memory: 8MB")})
	require.NoError(t, err)
	require.NotEqual(t, prod, staging)

	p, err := r.Resolve(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, prod, p.Object)
	require.Equal(t, uint64(5), p.Revision)

	s, err := r.Resolve(ctx, "shop", "staging")
	require.NoError(t, err)
	require.Equal(t, staging, s.Object)
	require.Equal(t, uint64(1), s.Revision, "and each environment counts its own deploys")
}

func TestProjectsAreIndependent(t *testing.T) {
	r, _ := newRegister(t)

	_, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("shop")})
	require.NoError(t, err)
	_, err = r.Publish(ctx, Publication{Project: "blog", Environment: "prod", Revision: 1, Compiled: []byte("blog")})
	require.NoError(t, err)

	shop, err := r.Resolve(ctx, "shop", "prod")
	require.NoError(t, err)
	got, err := r.Object(ctx, shop.Object)
	require.NoError(t, err)
	require.Equal(t, []byte("shop"), got)
}

// TestALateArrivalCannotRollAnEnvironmentBack. A retry, a node catching up or a
// replayed message must not put back code somebody already replaced.
func TestALateArrivalCannotRollAnEnvironmentBack(t *testing.T) {
	r, _ := newRegister(t)

	_, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 7, Compiled: []byte("current")})
	require.NoError(t, err)

	_, err = r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 3, Compiled: []byte("stale, arriving late")})
	require.ErrorIs(t, err, ErrStaleRevision)

	_, err = r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 7, Compiled: []byte("the same revision again")})
	require.ErrorIs(t, err, ErrStaleRevision, "the same revision is not forward either")

	p, err := r.Resolve(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, uint64(7), p.Revision)
	got, _ := r.Object(ctx, p.Object)
	require.Equal(t, []byte("current"), got, "nothing moved")
}

// TestARollbackIsAForwardPublish. "Go back to Tuesday's build" is a new
// revision naming an old object, so the history says what happened rather than
// having a reversal erase it.
func TestARollbackIsAForwardPublish(t *testing.T) {
	r, _ := newRegister(t)
	tuesday := []byte("the good one")

	good, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: tuesday})
	require.NoError(t, err)
	_, err = r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 2, Compiled: []byte("the bad one")})
	require.NoError(t, err)

	back, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 3, Compiled: tuesday})
	require.NoError(t, err)
	require.Equal(t, good, back, "the same configuration is the same object, so nothing is re-stored")

	p, err := r.Resolve(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, uint64(3), p.Revision, "and the revision moved forward")
	require.Equal(t, good, p.Object)
}

func TestNothingPublishedIsSaidPlainly(t *testing.T) {
	r, _ := newRegister(t)

	_, err := r.Resolve(ctx, "shop", "prod")
	require.ErrorIs(t, err, ErrNotPublished)
	require.Contains(t, err.Error(), "shop/prod", "a caller needs to know which one")
}

func TestAnUnknownObjectIsSaidPlainly(t *testing.T) {
	r, _ := newRegister(t)

	_, err := r.Object(ctx, "")
	require.ErrorIs(t, err, ErrNoObject)

	_, err = r.Object(ctx, IDOf([]byte("never published")))
	require.ErrorIs(t, err, ErrNoObject)
}

// TestANameMayNotContainTheSeparator. Without this, project "a/prod" with
// environment "prod" and project "a" with environment "prod/prod" address the
// same row, and one deploy silently overwrites another project's.
func TestANameMayNotContainTheSeparator(t *testing.T) {
	r, _ := newRegister(t)

	_, err := r.Publish(ctx, Publication{Project: "a/prod", Environment: "prod", Revision: 1, Compiled: []byte("x")})
	require.ErrorIs(t, err, ErrSeparator)

	_, err = r.Publish(ctx, Publication{Project: "a", Environment: "prod/prod", Revision: 1, Compiled: []byte("x")})
	require.ErrorIs(t, err, ErrSeparator)

	_, err = r.Resolve(ctx, "a/prod", "prod")
	require.ErrorIs(t, err, ErrSeparator)

	// And the collision it prevents, demonstrated: the two legal spellings do
	// not meet.
	_, err = r.Publish(ctx, Publication{Project: "a", Environment: "prod", Revision: 1, Compiled: []byte("first")})
	require.NoError(t, err)
	_, err = r.Resolve(ctx, "a", "prod")
	require.NoError(t, err)
}

func TestPublishRefusesWhatCannotBeAddressed(t *testing.T) {
	r, _ := newRegister(t)

	_, err := r.Publish(ctx, Publication{Project: "", Environment: "prod", Revision: 1, Compiled: []byte("x")})
	require.ErrorIs(t, err, ErrNoProject)

	_, err = r.Publish(ctx, Publication{Project: "shop", Environment: "", Revision: 1, Compiled: []byte("x")})
	require.ErrorIs(t, err, ErrNoEnvironment)

	_, err = r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: nil})
	require.ErrorIs(t, err, ErrEmptyObject)

	_, err = r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte{}})
	require.ErrorIs(t, err, ErrEmptyObject)

	_, err = r.Resolve(ctx, "", "prod")
	require.ErrorIs(t, err, ErrNoProject)
	_, err = r.Resolve(ctx, "shop", "")
	require.ErrorIs(t, err, ErrNoEnvironment)
}

// TestTheObjectIsWrittenBeforeThePointer. A pointer that arrived before its
// object is an environment that resolves to nothing, which is worse than one
// that has not been updated yet.
func TestTheObjectIsWrittenBeforeThePointer(t *testing.T) {
	store := &orderedStore{MemStore: NewMemStore()}
	r, err := New(WithStore(store))
	require.NoError(t, err)

	_, err = r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("compiled")})
	require.NoError(t, err)

	first, last := store.writes[0], store.writes[len(store.writes)-1]
	require.True(t, strings.HasPrefix(first, "o/"), "the object went first, got %q", first)
	require.True(t, strings.HasPrefix(last, "p/"), "the pointer went last, got %q", last)
}

// TestAFailedObjectWriteMovesNothing. Half a publish must not leave an
// environment pointing at something that is not there.
func TestAFailedObjectWriteMovesNothing(t *testing.T) {
	store := &failingStore{MemStore: NewMemStore(), failOn: "o/"}
	r, err := New(WithStore(store))
	require.NoError(t, err)

	_, err = r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("compiled")})
	require.Error(t, err)

	_, err = r.Resolve(ctx, "shop", "prod")
	require.ErrorIs(t, err, ErrNotPublished, "nothing points at the object that was never stored")
}

func TestAFailedPointerWriteIsReported(t *testing.T) {
	store := &failingStore{MemStore: NewMemStore(), failOn: "p/"}
	r, err := New(WithStore(store))
	require.NoError(t, err)

	_, err = r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("compiled")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "shop/prod")
}

// TestAMalformedPointerIsNotAnUnpublishedOne. Answering ErrNotPublished for a
// corrupted row would have the runtime report 404 for an environment that has
// been serving for months, and nothing in that answer names the broken part.
func TestAMalformedPointerIsNotAnUnpublishedOne(t *testing.T) {
	r, store := newRegister(t)

	for _, row := range []string{"not a pointer", "notanumber abc", "1 "} {
		require.NoError(t, store.Put(ctx, pointerKey("shop", "prod"), []byte(row)))

		_, err := r.Resolve(ctx, "shop", "prod")
		require.ErrorIs(t, err, ErrMalformedPointer, "row %q", row)
		require.NotErrorIs(t, err, ErrNotPublished)
	}
}

// TestAPublishRepairsAMalformedPointer. Reading is where a broken row is
// reported; writing is where it gets fixed. A publish that refused would leave
// an environment repairable only by hand.
func TestAPublishRepairsAMalformedPointer(t *testing.T) {
	r, store := newRegister(t)
	require.NoError(t, store.Put(ctx, pointerKey("shop", "prod"), []byte("garbage")))

	_, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("compiled")})
	require.NoError(t, err)

	p, err := r.Resolve(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, uint64(1), p.Revision)
}

// TestAnUnreachableStoreSaysSo rather than reporting an empty register. The two
// answers make a caller do opposite things.
func TestAnUnreachableStoreSaysSo(t *testing.T) {
	r, err := New(WithStore(&unreadableStore{NewMemStore()}))
	require.NoError(t, err)

	_, err = r.Resolve(ctx, "shop", "prod")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNotPublished)

	_, err = r.Object(ctx, IDOf([]byte("x")))
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNoObject)

	_, err = r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("compiled")})
	require.Error(t, err, "a publish cannot check the revision it must beat")
}

// TestConcurrentPublishesToOneEnvironmentPickOneWinner, and it is the highest
// revision. The store has no compare-and-set, so the read of the current
// revision and the write of the new one are one step under the lock.
func TestConcurrentPublishesToOneEnvironmentPickOneWinner(t *testing.T) {
	r, _ := newRegister(t)

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(rev int) {
			defer wg.Done()
			r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: uint64(rev + 1), Compiled: fmt.Appendf(nil, "revision %d", rev+1)})
		}(i)
	}
	wg.Wait()

	p, err := r.Resolve(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, uint64(20), p.Revision, "the highest revision is what stands")

	got, err := r.Object(ctx, p.Object)
	require.NoError(t, err)
	require.Equal(t, []byte("revision 20"), got, "and the object matches the revision that won")
}

func TestNewRefusesNoStore(t *testing.T) {
	_, err := New()
	require.Error(t, err)

	_, err = New(WithStore(nil))
	require.Error(t, err)
}

func TestTheMemStoreDoesNotAliasItsCaller(t *testing.T) {
	s := NewMemStore()
	val := []byte("original")
	require.NoError(t, s.Put(ctx, "k", val))
	val[0] = 'X'

	got, ok, err := s.Get(ctx, "k")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []byte("original"), got)

	got[0] = 'Y'
	again, _, err := s.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, []byte("original"), again)

	_, ok, err = s.Get(ctx, "missing")
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, []string{"k"}, s.Keys())
}

// ─────────────────────────── stores for the tests ───────────────────────────

type orderedStore struct {
	*MemStore
	mu     sync.Mutex
	writes []string
}

func (s *orderedStore) Put(ctx context.Context, key string, value []byte) error {
	s.mu.Lock()
	s.writes = append(s.writes, key)
	s.mu.Unlock()
	return s.MemStore.Put(ctx, key, value)
}

type failingStore struct {
	*MemStore
	failOn string
}

func (s *failingStore) Put(ctx context.Context, key string, value []byte) error {
	if strings.HasPrefix(key, s.failOn) {
		return errors.New("the store refused")
	}
	return s.MemStore.Put(ctx, key, value)
}

// unreadableStore answers every read with a failure, which is what a store on
// the other side of an unreachable swarm looks like.
type unreadableStore struct{ *MemStore }

func (s *unreadableStore) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errors.New("the store is unreachable")
}

// sometimesFailingStore refuses only a tombstone — an empty value under a host
// key — which is what releasing a name writes.
type sometimesFailingStore struct {
	*MemStore
	fail bool
}

func (s *sometimesFailingStore) Put(ctx context.Context, key string, value []byte) error {
	if s.fail && strings.HasPrefix(key, hostPrefix) && len(value) == 0 {
		return errors.New("the store refused")
	}
	return s.MemStore.Put(ctx, key, value)
}
