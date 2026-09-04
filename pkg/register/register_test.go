package register

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

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

	id, err := r.Publish("shop", "prod", 1, compiled)
	require.NoError(t, err)

	p, err := r.Resolve("shop", "prod")
	require.NoError(t, err)
	require.Equal(t, id, p.Object)
	require.Equal(t, uint64(1), p.Revision)

	got, err := r.Object(p.Object)
	require.NoError(t, err)
	require.Equal(t, compiled, got)
}

// TestTheSameConfigurationIsTheSameObject. Two identical compilations produce
// one id, which is what makes "I already have this" answerable without asking.
func TestTheSameConfigurationIsTheSameObject(t *testing.T) {
	r, store := newRegister(t)
	compiled := []byte("the same bytes")

	first, err := r.Publish("shop", "prod", 1, compiled)
	require.NoError(t, err)
	second, err := r.Publish("shop", "prod", 2, compiled)
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

	a, err := r.Publish("shop", "prod", 1, []byte("memory: 32MB"))
	require.NoError(t, err)
	b, err := r.Publish("shop", "prod", 2, []byte("memory: 64MB"))
	require.NoError(t, err)
	require.NotEqual(t, a, b)

	// The old object is still there, addressable, unchanged — which is what a
	// rollback needs and what a node still running the old revision reads.
	old, err := r.Object(a)
	require.NoError(t, err)
	require.Equal(t, []byte("memory: 32MB"), old)
}

// TestEnvironmentsAreIndependent. execution/memory is PerEnv() in the schema, so
// staging and prod resolving to the same object would defeat the point.
func TestEnvironmentsAreIndependent(t *testing.T) {
	r, _ := newRegister(t)

	prod, err := r.Publish("shop", "prod", 5, []byte("memory: 64MB"))
	require.NoError(t, err)
	staging, err := r.Publish("shop", "staging", 1, []byte("memory: 8MB"))
	require.NoError(t, err)
	require.NotEqual(t, prod, staging)

	p, err := r.Resolve("shop", "prod")
	require.NoError(t, err)
	require.Equal(t, prod, p.Object)
	require.Equal(t, uint64(5), p.Revision)

	s, err := r.Resolve("shop", "staging")
	require.NoError(t, err)
	require.Equal(t, staging, s.Object)
	require.Equal(t, uint64(1), s.Revision, "and each environment counts its own deploys")
}

func TestProjectsAreIndependent(t *testing.T) {
	r, _ := newRegister(t)

	_, err := r.Publish("shop", "prod", 1, []byte("shop"))
	require.NoError(t, err)
	_, err = r.Publish("blog", "prod", 1, []byte("blog"))
	require.NoError(t, err)

	shop, err := r.Resolve("shop", "prod")
	require.NoError(t, err)
	got, err := r.Object(shop.Object)
	require.NoError(t, err)
	require.Equal(t, []byte("shop"), got)
}

// TestALateArrivalCannotRollAnEnvironmentBack. A retry, a node catching up or a
// replayed message must not put back code somebody already replaced.
func TestALateArrivalCannotRollAnEnvironmentBack(t *testing.T) {
	r, _ := newRegister(t)

	_, err := r.Publish("shop", "prod", 7, []byte("current"))
	require.NoError(t, err)

	_, err = r.Publish("shop", "prod", 3, []byte("stale, arriving late"))
	require.ErrorIs(t, err, ErrStaleRevision)

	_, err = r.Publish("shop", "prod", 7, []byte("the same revision again"))
	require.ErrorIs(t, err, ErrStaleRevision, "the same revision is not forward either")

	p, err := r.Resolve("shop", "prod")
	require.NoError(t, err)
	require.Equal(t, uint64(7), p.Revision)
	got, _ := r.Object(p.Object)
	require.Equal(t, []byte("current"), got, "nothing moved")
}

// TestARollbackIsAForwardPublish. "Go back to Tuesday's build" is a new
// revision naming an old object, so the history says what happened rather than
// having a reversal erase it.
func TestARollbackIsAForwardPublish(t *testing.T) {
	r, _ := newRegister(t)
	tuesday := []byte("the good one")

	good, err := r.Publish("shop", "prod", 1, tuesday)
	require.NoError(t, err)
	_, err = r.Publish("shop", "prod", 2, []byte("the bad one"))
	require.NoError(t, err)

	back, err := r.Publish("shop", "prod", 3, tuesday)
	require.NoError(t, err)
	require.Equal(t, good, back, "the same configuration is the same object, so nothing is re-stored")

	p, err := r.Resolve("shop", "prod")
	require.NoError(t, err)
	require.Equal(t, uint64(3), p.Revision, "and the revision moved forward")
	require.Equal(t, good, p.Object)
}

func TestNothingPublishedIsSaidPlainly(t *testing.T) {
	r, _ := newRegister(t)

	_, err := r.Resolve("shop", "prod")
	require.ErrorIs(t, err, ErrNotPublished)
	require.Contains(t, err.Error(), "shop/prod", "a caller needs to know which one")
}

func TestAnUnknownObjectIsSaidPlainly(t *testing.T) {
	r, _ := newRegister(t)

	_, err := r.Object("")
	require.ErrorIs(t, err, ErrNoObject)

	_, err = r.Object(IDOf([]byte("never published")))
	require.ErrorIs(t, err, ErrNoObject)
}

// TestANameMayNotContainTheSeparator. Without this, project "a/prod" with
// environment "prod" and project "a" with environment "prod/prod" address the
// same row, and one deploy silently overwrites another project's.
func TestANameMayNotContainTheSeparator(t *testing.T) {
	r, _ := newRegister(t)

	_, err := r.Publish("a/prod", "prod", 1, []byte("x"))
	require.ErrorIs(t, err, ErrSeparator)

	_, err = r.Publish("a", "prod/prod", 1, []byte("x"))
	require.ErrorIs(t, err, ErrSeparator)

	_, err = r.Resolve("a/prod", "prod")
	require.ErrorIs(t, err, ErrSeparator)

	// And the collision it prevents, demonstrated: the two legal spellings do
	// not meet.
	_, err = r.Publish("a", "prod", 1, []byte("first"))
	require.NoError(t, err)
	_, err = r.Resolve("a", "prod")
	require.NoError(t, err)
}

func TestPublishRefusesWhatCannotBeAddressed(t *testing.T) {
	r, _ := newRegister(t)

	_, err := r.Publish("", "prod", 1, []byte("x"))
	require.ErrorIs(t, err, ErrNoProject)

	_, err = r.Publish("shop", "", 1, []byte("x"))
	require.ErrorIs(t, err, ErrNoEnvironment)

	_, err = r.Publish("shop", "prod", 1, nil)
	require.ErrorIs(t, err, ErrEmptyObject)

	_, err = r.Publish("shop", "prod", 1, []byte{})
	require.ErrorIs(t, err, ErrEmptyObject)

	_, err = r.Resolve("", "prod")
	require.ErrorIs(t, err, ErrNoProject)
	_, err = r.Resolve("shop", "")
	require.ErrorIs(t, err, ErrNoEnvironment)
}

// TestTheObjectIsWrittenBeforeThePointer. A pointer that arrived before its
// object is an environment that resolves to nothing, which is worse than one
// that has not been updated yet.
func TestTheObjectIsWrittenBeforeThePointer(t *testing.T) {
	store := &orderedStore{MemStore: NewMemStore()}
	r, err := New(WithStore(store))
	require.NoError(t, err)

	_, err = r.Publish("shop", "prod", 1, []byte("compiled"))
	require.NoError(t, err)

	require.Len(t, store.writes, 2)
	require.True(t, store.writes[0][:2] == "o/", "the object went first, got %q", store.writes[0])
	require.True(t, store.writes[1][:2] == "p/", "the pointer went second, got %q", store.writes[1])
}

// TestAFailedObjectWriteMovesNothing. Half a publish must not leave an
// environment pointing at something that is not there.
func TestAFailedObjectWriteMovesNothing(t *testing.T) {
	store := &failingStore{MemStore: NewMemStore(), failOn: "o/"}
	r, err := New(WithStore(store))
	require.NoError(t, err)

	_, err = r.Publish("shop", "prod", 1, []byte("compiled"))
	require.Error(t, err)

	_, err = r.Resolve("shop", "prod")
	require.ErrorIs(t, err, ErrNotPublished, "nothing points at the object that was never stored")
}

func TestAFailedPointerWriteIsReported(t *testing.T) {
	store := &failingStore{MemStore: NewMemStore(), failOn: "p/"}
	r, err := New(WithStore(store))
	require.NoError(t, err)

	_, err = r.Publish("shop", "prod", 1, []byte("compiled"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "shop/prod")
}

// TestAMalformedPointerReadsAsUnpublished rather than as a pointer to nowhere.
func TestAMalformedPointerReadsAsUnpublished(t *testing.T) {
	r, store := newRegister(t)
	require.NoError(t, store.Put(pointerKey("shop", "prod"), []byte("not a pointer")))

	_, err := r.Resolve("shop", "prod")
	require.ErrorIs(t, err, ErrNotPublished)

	require.NoError(t, store.Put(pointerKey("shop", "prod"), []byte("notanumber abc")))
	_, err = r.Resolve("shop", "prod")
	require.ErrorIs(t, err, ErrNotPublished)

	require.NoError(t, store.Put(pointerKey("shop", "prod"), []byte("1 ")))
	_, err = r.Resolve("shop", "prod")
	require.ErrorIs(t, err, ErrNotPublished, "a pointer naming no object is not a pointer")
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
			r.Publish("shop", "prod", uint64(rev+1), fmt.Appendf(nil, "revision %d", rev+1))
		}(i)
	}
	wg.Wait()

	p, err := r.Resolve("shop", "prod")
	require.NoError(t, err)
	require.Equal(t, uint64(20), p.Revision, "the highest revision is what stands")

	got, err := r.Object(p.Object)
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
	require.NoError(t, s.Put("k", val))
	val[0] = 'X'

	got, ok := s.Get("k")
	require.True(t, ok)
	require.Equal(t, []byte("original"), got)

	got[0] = 'Y'
	again, _ := s.Get("k")
	require.Equal(t, []byte("original"), again)

	_, ok = s.Get("missing")
	require.False(t, ok)
	require.Equal(t, []string{"k"}, s.Keys())
}

// ─────────────────────────── stores for the tests ───────────────────────────

type orderedStore struct {
	*MemStore
	mu     sync.Mutex
	writes []string
}

func (s *orderedStore) Put(key string, value []byte) error {
	s.mu.Lock()
	s.writes = append(s.writes, key)
	s.mu.Unlock()
	return s.MemStore.Put(key, value)
}

type failingStore struct {
	*MemStore
	failOn string
}

func (s *failingStore) Put(key string, value []byte) error {
	if len(key) >= len(s.failOn) && key[:len(s.failOn)] == s.failOn {
		return errors.New("the store refused")
	}
	return s.MemStore.Put(key, value)
}
