package register

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// fakeRegistry answers the two reads a cache makes and counts them.
type fakeRegistry struct {
	mu sync.Mutex

	pointers map[Deployment]Pointer
	objects  map[ObjectID][]byte

	resolves int
	fetches  int

	// hold, when set, blocks every Object call until it is closed — the only
	// way to have two readers inside one fetch at once.
	hold chan struct{}

	resolveErr error
	objectErr  error
}

func (f *fakeRegistry) Resolve(ctx context.Context, project, environment string) (Pointer, error) {
	f.mu.Lock()
	f.resolves++
	f.mu.Unlock()
	if f.resolveErr != nil {
		return Pointer{}, f.resolveErr
	}
	p, ok := f.pointers[Deployment{project, environment}]
	if !ok {
		return Pointer{}, errors.New("no such deployment")
	}
	return p, nil
}

func (f *fakeRegistry) Object(ctx context.Context, id ObjectID) ([]byte, error) {
	f.mu.Lock()
	f.fetches++
	f.mu.Unlock()
	if f.hold != nil {
		<-f.hold
	}
	if f.objectErr != nil {
		return nil, f.objectErr
	}
	b, ok := f.objects[id]
	if !ok {
		return nil, errors.New("no such object")
	}
	return b, nil
}

func (f *fakeRegistry) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resolves, f.fetches
}

// put writes an object and points a deployment at it, returning its address.
func (f *fakeRegistry) put(at Deployment, body string, revision uint64) ObjectID {
	if f.pointers == nil {
		f.pointers = map[Deployment]Pointer{}
		f.objects = map[ObjectID][]byte{}
	}
	id := IDOf([]byte(body))
	f.objects[id] = []byte(body)
	f.pointers[at] = Pointer{Object: id, Revision: revision}
	return id
}

// asString is the parse a test cache keeps: whatever came back, as a string.
func asString(b []byte) (string, error) { return string(b), nil }

func TestACacheNeedsSomethingToReadFromAndAWayToReadIt(t *testing.T) {
	_, err := NewCache[string](nil, asString)
	assert.ErrorIs(t, err, ErrNoRegistry)

	_, err = NewCache[string](&fakeRegistry{}, nil)
	assert.ErrorIs(t, err, ErrNoParse)
}

func TestTheOptionsRefuseWhatWouldMakeTheCacheUseless(t *testing.T) {
	type testCase struct {
		name string
		opt  CacheOption
	}
	for _, tc := range []testCase{
		{"a negative deploy budget", WithTTL(-time.Second)},
		{"a cache that holds nothing", WithMax(0)},
		{"a nil clock", WithClock(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewCache(&fakeRegistry{}, asString, tc.opt)
			assert.Assert(t, err != nil)
		})
	}
}

func TestAPointerIsTrustedForTheDeployBudgetAndNoLonger(t *testing.T) {
	reg := &fakeRegistry{}
	at := Deployment{"shop", "prod"}
	reg.put(at, "one", 1)

	now := time.Unix(0, 0)
	c, err := NewCache(reg, asString, WithTTL(5*time.Second), WithClock(func() time.Time { return now }))
	assert.NilError(t, err)

	body, pointer, err := c.At(t.Context(), at)
	assert.NilError(t, err)
	assert.Equal(t, body, "one")
	assert.Equal(t, pointer.Revision, uint64(1))

	// Inside the budget nothing is asked again — neither the pointer nor the
	// object it names.
	_, _, err = c.At(t.Context(), at)
	assert.NilError(t, err)
	resolves, fetches := reg.counts()
	assert.Equal(t, resolves, 1)
	assert.Equal(t, fetches, 1)

	// A deploy lands, and until the budget expires the old code keeps answering.
	reg.put(at, "two", 2)
	_, pointer, err = c.At(t.Context(), at)
	assert.NilError(t, err)
	assert.Equal(t, pointer.Revision, uint64(1))

	now = now.Add(5 * time.Second)
	body, pointer, err = c.At(t.Context(), at)
	assert.NilError(t, err)
	assert.Equal(t, body, "two")
	assert.Equal(t, pointer.Revision, uint64(2))
}

func TestOneFetchServesEveryReaderThatWantedItAtTheSameInstant(t *testing.T) {
	reg := &fakeRegistry{hold: make(chan struct{})}
	at := Deployment{"shop", "prod"}
	reg.put(at, "one", 1)

	c, err := NewCache(reg, asString)
	assert.NilError(t, err)

	// Warm the pointer so the readers below race on the OBJECT and not on the
	// resolve; the fetch is the expensive one — a whole compiled project.
	go c.At(t.Context(), at)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, _, err := c.At(t.Context(), at)
			assert.NilError(t, err)
			assert.Equal(t, body, "one")
		}()
	}

	// Let them pile up behind the one in-flight fetch, then release it.
	for {
		if _, fetches := reg.counts(); fetches > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(reg.hold)
	wg.Wait()

	_, fetches := reg.counts()
	assert.Equal(t, fetches, 1)
}

func TestAFailedLoadIsNotRemembered(t *testing.T) {
	reg := &fakeRegistry{}
	at := Deployment{"shop", "prod"}
	reg.put(at, "one", 1)
	reg.objectErr = errors.New("register unreachable")

	c, err := NewCache(reg, asString)
	assert.NilError(t, err)

	_, _, err = c.At(t.Context(), at)
	assert.ErrorContains(t, err, "register unreachable")
	assert.Equal(t, c.Held(), 0)

	// The bad moment passes and the project loads.
	reg.objectErr = nil
	body, _, err := c.At(t.Context(), at)
	assert.NilError(t, err)
	assert.Equal(t, body, "one")
	assert.Equal(t, c.Held(), 1)
}

func TestAPointerThatCannotBeResolvedIsTheWholeAnswer(t *testing.T) {
	reg := &fakeRegistry{resolveErr: errors.New("no register")}
	c, err := NewCache(reg, asString)
	assert.NilError(t, err)

	_, _, err = c.At(t.Context(), Deployment{"shop", "prod"})
	assert.ErrorContains(t, err, "no register")
	assert.Equal(t, len(c.Deployments()), 0)
}

func TestWhatParseRefusesTheCacheRefuses(t *testing.T) {
	reg := &fakeRegistry{}
	at := Deployment{"shop", "prod"}
	reg.put(at, "one", 1)

	c, err := NewCache(reg, func([]byte) (string, error) { return "", errors.New("not a compiled project") })
	assert.NilError(t, err)

	_, _, err = c.At(t.Context(), at)
	assert.ErrorContains(t, err, "not a compiled project")
	assert.Equal(t, c.Held(), 0)
}

func TestTheOldestProjectIsTheOneDropped(t *testing.T) {
	reg := &fakeRegistry{}
	c, err := NewCache(reg, asString, WithMax(2), WithTTL(0))
	assert.NilError(t, err)

	first := Deployment{"shop", "prod"}
	second := Deployment{"shop", "staging"}
	third := Deployment{"books", "prod"}
	reg.put(first, "one", 1)
	reg.put(second, "two", 1)
	reg.put(third, "three", 1)

	for _, at := range []Deployment{first, second} {
		_, _, err := c.At(t.Context(), at)
		assert.NilError(t, err)
	}

	// Reading the first again makes the SECOND the oldest, which is the only
	// thing an LRU claims over a queue.
	_, _, err = c.At(t.Context(), first)
	assert.NilError(t, err)

	_, _, err = c.At(t.Context(), third)
	assert.NilError(t, err)
	assert.Equal(t, c.Held(), 2)

	// The first is still held — no refetch — and the second is gone.
	_, beforeFirst := reg.counts()
	_, _, err = c.At(t.Context(), first)
	assert.NilError(t, err)
	_, afterFirst := reg.counts()
	assert.Equal(t, afterFirst, beforeFirst)

	_, _, err = c.At(t.Context(), second)
	assert.NilError(t, err)
	_, afterSecond := reg.counts()
	assert.Equal(t, afterSecond, beforeFirst+1)
}

func TestACacheSaysWhichDeploymentsItCanAnswerForCheaply(t *testing.T) {
	reg := &fakeRegistry{}
	c, err := NewCache(reg, asString)
	assert.NilError(t, err)

	assert.Equal(t, len(c.Deployments()), 0)

	for _, at := range []Deployment{{"shop", "staging"}, {"books", "prod"}, {"shop", "prod"}} {
		reg.put(at, at.String(), 1)
		_, _, err := c.At(t.Context(), at)
		assert.NilError(t, err)
	}

	held := c.Deployments()
	assert.Equal(t, len(held), 3)
	assert.Equal(t, held[0].String(), "books/prod")
	assert.Equal(t, held[1].String(), "shop/prod")
	assert.Equal(t, held[2].String(), "shop/staging")
}
