package database_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	backing "github.com/taubyte/tau/node/database"
	"github.com/taubyte/tau/pkg/database"
)

// database_test is the copies of a database this node keeps: which store one's
// entries go into, what it says it holds, and what letting go of one does to the
// table it serves from.
//
// EVERY ONE OF THESE USED TO NEED A NODE — a mocknet host, a register, a worker
// and a started composition — to assert which store a spec reached. A factory
// that cannot be asked what it holds, a store that cannot be released and a
// release racing a read are ordinary fixtures here.

// ---------------------------------------------------------------- doubles

// stores is a Stores double: one store per NAME, which is what a real factory
// gives — a database is named, and opening it twice is opening the same rows. A
// double that handed back a fresh map each time would hide every place two
// consumers are meant to see each other's writes.
type stores struct {
	mu     sync.Mutex
	opened []string
	byName map[string]*rows

	openErr    error
	releaseErr error
	released   []string
}

func newStores() *stores { return &stores{byName: map[string]*rows{}} }

func (s *stores) Store(name string) (database.Store, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.openErr != nil {
		return nil, s.openErr
	}
	s.opened = append(s.opened, name)
	if held, ok := s.byName[name]; ok {
		return held, nil
	}
	held := &rows{held: map[string][]byte{}}
	s.byName[name] = held
	return held, nil
}

// Release destroys the way the real one does: the store is dropped from the
// factory, so a later open builds a new one from whatever the fleet still holds.
func (s *stores) Release(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.releaseErr != nil {
		return s.releaseErr
	}
	s.released = append(s.released, name)
	if held, ok := s.byName[name]; ok {
		held.destroyed = true
		delete(s.byName, name)
	}
	return nil
}

func (s *stores) did() (opened, released []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.opened...), append([]string(nil), s.released...)
}

// rows is one database's store, over a map.
type rows struct {
	mu        sync.Mutex
	held      map[string][]byte
	destroyed bool
}

func (r *rows) Get(_ context.Context, key string) ([]byte, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, held := r.held[key]
	return value, held, nil
}

func (r *rows) Put(_ context.Context, key string, value []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.held[key] = value
	return nil
}

func (r *rows) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.held, key)
	return nil
}

func (r *rows) List(_ context.Context, prefix string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for key := range r.held {
		if strings.HasPrefix(key, prefix) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (r *rows) gone() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.destroyed
}

// ledgers is a Ledgers double: the other arm of the consensus axis, which is a
// store like any other from here.
type ledgers struct {
	mu    sync.Mutex
	asked []database.Spec
	store database.Store
	err   error
}

func (l *ledgers) Store(spec database.Spec) (database.Store, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.asked = append(l.asked, spec)
	if l.err != nil {
		return nil, l.err
	}
	return l.store, nil
}

func (l *ledgers) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.asked)
}

// disk is a Disk double: what a node has WITHOUT opening it.
type disk struct {
	holds map[string]bool
	paths []string

	holdErr error
	pathErr error
}

func (d *disk) Holding(_ context.Context, path string) (bool, error) {
	if d.holdErr != nil {
		return false, d.holdErr
	}
	return d.holds[path], nil
}

func (d *disk) Paths(_ context.Context, under string, depth int) ([]string, error) {
	if d.pathErr != nil {
		return nil, d.pathErr
	}
	var out []string
	for _, path := range d.paths {
		if !strings.HasPrefix(path, under+"/") {
			continue
		}
		if len(strings.Split(strings.TrimPrefix(path, under+"/"), "/")) > depth {
			continue
		}
		out = append(out, path)
	}
	return out, nil
}

// ---------------------------------------------------------------- fixtures

func kvSpec(project, name string) database.Spec {
	return database.Spec{
		Type: database.KV, Engine: database.Tau, Consensus: database.CRDT,
		Project: project, Environment: "prod", Name: name,
	}
}

func ledgerSpec() database.Spec {
	spec := kvSpec("shop", "QmLedger")
	spec.Consensus = database.Raft
	return spec
}

// unaddressable is a spec that names nothing: a separator in a segment is a
// readable path into somebody else's database, so Spec.Prefix refuses it.
func unaddressable() database.Spec {
	return database.Spec{Type: database.KV, Name: "ledger"}
}

func holdings(t *testing.T, s backing.Stores, l backing.Ledgers, d backing.Disk) *backing.Holdings {
	t.Helper()
	h, err := backing.NewHoldings(s, l, d)
	require.NoError(t, err)
	return h
}

// ---------------------------------------------------------------- the name

// TestONENAMEADDRESSESADATABASEANDNAMESITSTOPIC, so the store a request reaches,
// the topic its rows travel on and the path a release evicts agree by
// construction rather than by three expressions that happen to match.
func TestOneNameAddressesADatabaseAndNamesItsTopic(t *testing.T) {
	name, err := backing.Name(kvSpec("shop", "QmSessions"))
	require.NoError(t, err)
	require.Equal(t, backing.Prefix+"/shop/prod/kv/QmSessions", name)

	// AND A SPEC THAT ADDRESSES NOTHING NAMES NOTHING.
	_, err = backing.Name(unaddressable())
	require.ErrorIs(t, err, database.ErrUnaddressable)
}

// TestTHECACHESANDTHEDATABASESARENOTONESTORE. They are different kinds with
// different lifetimes: a cache evicts and a database does not, and one CRDT
// holding both would make a cache's churn the database's replication traffic.
func TestADatabaseIsNamedUnderItsOwnPrefix(t *testing.T) {
	require.Equal(t, "databases", backing.Prefix)
}

// ---------------------------------------------------------------- construction

// TestANODEWITHNOWHERETOPUTENTRIESISACOMPOSITIONFAULT, refused where it can be
// named rather than on the first write.
func TestNewHoldingsRefusesWhatItCannotDoWithout(t *testing.T) {
	_, err := backing.NewHoldings(nil, &ledgers{}, nil)
	require.ErrorContains(t, err, "nowhere to keep")

	_, err = backing.NewHoldings(newStores(), nil, nil)
	require.ErrorContains(t, err, "not even a refusal")

	// AND A FACTORY THAT CANNOT BE ASKED WHAT IT HOLDS IS ALLOWED, because the
	// interface a composer satisfies is also satisfied by handles that keep no
	// bytes at all.
	_, err = backing.NewHoldings(newStores(), &ledgers{}, nil)
	require.NoError(t, err)
}

// ---------------------------------------------------------------- entries

// TestASTOREPERDATABASEANDNOTONEFORALLOFTHEM, which is a pubsub topic per
// database: one store for every database on a node is one topic carrying every
// database's rows, and then placement decides nothing.
func TestAStoreIsOpenedPerDatabaseAndKept(t *testing.T) {
	kept := newStores()
	h := holdings(t, kept, &ledgers{}, nil)

	for _, spec := range []database.Spec{
		kvSpec("shop", "QmA"), kvSpec("shop", "QmB"), kvSpec("other", "QmA"),
	} {
		store, err := h.Entries(t.Context(), spec)
		require.NoError(t, err)
		require.NoError(t, store.Put(t.Context(), "k", []byte("v")))
	}

	opened, _ := kept.did()
	require.Equal(t, []string{
		backing.Prefix + "/shop/prod/kv/QmA",
		backing.Prefix + "/shop/prod/kv/QmB",
		backing.Prefix + "/other/prod/kv/QmA",
	}, opened)

	// AND ASKING AGAIN OPENS NOTHING MORE: a database this node already holds
	// costs no second topic and no second subscription.
	for range 3 {
		_, err := h.Entries(t.Context(), kvSpec("shop", "QmA"))
		require.NoError(t, err)
	}
	opened, _ = kept.did()
	require.Len(t, opened, 3, "reopening a held database opened another store: %v", opened)
}

// TestTWODATABASESDONOTSEEEACHOTHERSROWS, and neither do two deployments of one
// — which follows from the name and is asserted through the door an engine uses.
func TestTwoDatabasesAreTwoSetsOfRows(t *testing.T) {
	h := holdings(t, newStores(), &ledgers{}, nil)

	mine, err := h.Entries(t.Context(), kvSpec("shop", "QmA"))
	require.NoError(t, err)
	theirs, err := h.Entries(t.Context(), kvSpec("shop", "QmB"))
	require.NoError(t, err)
	elsewhere, err := h.Entries(t.Context(), kvSpec("market", "QmA"))
	require.NoError(t, err)

	require.NoError(t, mine.Put(t.Context(), "k", []byte("mine")))
	require.NoError(t, theirs.Put(t.Context(), "k", []byte("theirs")))
	require.NoError(t, elsewhere.Put(t.Context(), "k", []byte("elsewhere")))

	for want, store := range map[string]database.Store{
		"mine": mine, "theirs": theirs, "elsewhere": elsewhere,
	} {
		value, held, err := store.Get(t.Context(), "k")
		require.NoError(t, err)
		require.True(t, held)
		require.Equal(t, want, string(value))
	}
}

// TestALEDGERISASTORETOO, which is the whole of the difference between the two
// resolutions: the engine above is unchanged, and what `consensus` picks is
// where the four methods go.
func TestTheConsensusPicksTheStoreAndNotASecondEngine(t *testing.T) {
	kept, ordered := newStores(), &ledgers{store: &rows{held: map[string][]byte{}}}
	h := holdings(t, kept, ordered, nil)

	ledger, err := h.Entries(t.Context(), ledgerSpec())
	require.NoError(t, err)
	require.Same(t, ordered.store, ledger)
	require.Equal(t, 1, ordered.count())

	opened, _ := kept.did()
	require.Empty(t, opened, "a ledger opened a crdt store as well: %v", opened)

	// AND A DATABASE THAT DECLARED NOTHING IS A CRDT ONE, which is what every
	// caller that predates the field keeps getting.
	silent := kvSpec("shop", "QmSessions")
	silent.Consensus = ""
	_, err = h.Entries(t.Context(), silent)
	require.NoError(t, err)
	require.Equal(t, 1, ordered.count(), "a database with no consensus was sent to a group")

	// AND A LEDGER IS ASKED EVERY TIME rather than cached here: which group is
	// open, and whether it has elected, is the ledger table's to answer.
	_, err = h.Entries(t.Context(), ledgerSpec())
	require.NoError(t, err)
	require.Equal(t, 2, ordered.count())

	ordered.err = errors.New("no group")
	_, err = h.Entries(t.Context(), ledgerSpec())
	require.ErrorContains(t, err, "no group")
}

// TestASPECTHATADDRESSESNOTHINGOPENSNOTHING, rather than opening a store under
// a name that is a path into somebody else's.
func TestAnUnaddressableSpecOpensNoStore(t *testing.T) {
	kept := newStores()
	h := holdings(t, kept, &ledgers{}, nil)

	_, err := h.Entries(t.Context(), unaddressable())
	require.ErrorIs(t, err, database.ErrUnaddressable)

	opened, _ := kept.did()
	require.Empty(t, opened)
}

// TestASTORETHATCOULDNOTBEOPENEDISNOTREMEMBERED, so the next request tries
// again instead of reading one bad moment back out of the table forever.
func TestAStoreThatCouldNotBeOpenedIsNotKept(t *testing.T) {
	kept := newStores()
	kept.openErr = errors.New("no database factory")
	h := holdings(t, kept, &ledgers{}, nil)

	_, err := h.Entries(t.Context(), kvSpec("shop", "QmA"))
	require.ErrorContains(t, err, "no database factory")

	kept.openErr = nil
	store, err := h.Entries(t.Context(), kvSpec("shop", "QmA"))
	require.NoError(t, err)
	require.NotNil(t, store)
}

// ---------------------------------------------------------------- held

// TestHOLDINGISASKEDOFTHEDISKANDNOTOFTHETABLE. A restarted node holds
// everything it held before and has opened none of it; a holder answering from
// its warm table would say it holds nothing, and every move that needed it would
// either wait forever or count its "not held" as a reason for somebody else to
// let go.
func TestHeldIsAnsweredFromTheDiskAndNotTheTable(t *testing.T) {
	has := &disk{holds: map[string]bool{backing.Prefix + "/shop/prod/kv/QmA": true}}
	h := holdings(t, newStores(), &ledgers{}, has)

	require.True(t, h.Held(t.Context(), kvSpec("shop", "QmA")),
		"a copy this node has never opened in this run was reported as not held")
	require.False(t, h.Held(t.Context(), kvSpec("shop", "QmB")))

	// A SPEC THAT ADDRESSES NOTHING IS NOT HELD, because there is no path to
	// ask about.
	require.False(t, h.Held(t.Context(), unaddressable()))

	// AND A DISK THAT COULD NOT BE READ HOLDS NOTHING, as far as anybody else is
	// concerned: answering "yes" on a failed read would let a peer drop the last
	// copy.
	has.holdErr = errors.New("the datastore is closed")
	require.False(t, h.Held(t.Context(), kvSpec("shop", "QmA")))
}

// TestAFACTORYTHATCANNOTBEASKEDHOLDSNOTHING. Answering "yes" on a guess would
// let a peer drop the last copy.
func TestAFactoryThatCannotBeAskedHoldsNothing(t *testing.T) {
	h := holdings(t, newStores(), &ledgers{}, nil)

	require.False(t, h.Held(t.Context(), kvSpec("shop", "QmA")))
	require.Nil(t, h.OnDisk(t.Context()))
}

// ---------------------------------------------------------------- on disk

// TestONDISKISFOURSEGMENTSBELOWTHEPREFIX, which is what Spec.Prefix writes:
// project, environment, model, name. Everything under that is the CRDT's own
// structure and enumerating it would report internals as databases.
func TestOnDiskReadsBackWhatTheNameWrote(t *testing.T) {
	has := &disk{paths: []string{
		backing.Prefix + "/shop/prod/kv/QmA",
		backing.Prefix + "/shop/staging/document/QmOrders",
		// Not four segments: the CRDT's own bookkeeping, and a database at the
		// wrong depth is not a database.
		backing.Prefix + "/shop/prod/kv",
		backing.Prefix + "/shop/prod/kv/QmA/heads/one",
		// Another kind's store entirely, which Paths already filters by prefix.
		"caches/shop/prod/QmCache",
	}}
	h := holdings(t, newStores(), &ledgers{}, has)

	require.Equal(t, []database.Spec{
		{Project: "shop", Environment: "prod", Type: database.KV, Name: "QmA"},
		{Project: "shop", Environment: "staging", Type: database.Document, Name: "QmOrders"},
	}, h.OnDisk(t.Context()))

	// AND A DISK THAT COULD NOT BE WALKED IS NOT AN EMPTY NODE. A sweep told
	// this node holds nothing would move everything off it.
	has.pathErr = errors.New("the datastore is closed")
	require.Nil(t, h.OnDisk(t.Context()))
}

// ---------------------------------------------------------------- letting go

// TestARELEASEDDATABASEISNOTSERVEDFROMTHETABLEAGAIN.
//
// Destroying a database closes the store it destroys. A node that kept that
// store in the table it serves from went on answering every later call out of a
// closed store — not-found for rows another node holds, and the CRDT's own
// bookkeeping written back under the prefix that was just removed, which makes
// this node advertise a database it does not have.
func TestAReleasedDatabaseIsNotServedFromTheTableAgain(t *testing.T) {
	kept := newStores()
	h := holdings(t, kept, &ledgers{}, nil)
	spec := kvSpec("shop", "QmA")

	first, err := h.Entries(t.Context(), spec)
	require.NoError(t, err)

	require.NoError(t, h.Destroy(t.Context(), spec))
	require.True(t, first.(*rows).gone(),
		"the release destroyed a store the node was not serving from")

	_, released := kept.did()
	require.Equal(t, []string{backing.Prefix + "/shop/prod/kv/QmA"}, released)

	again, err := h.Entries(t.Context(), spec)
	require.NoError(t, err)
	require.False(t, again.(*rows).gone(), "the node went on serving the store it destroyed")
	require.NotSame(t, first, again)
}

// TestACOPYTHISNODENEVEROPENEDCANSTILLBERELEASED, because not open is not
// absent: a restarted node holds bytes it has never opened, and placement moving
// that database off it has to be able to land.
func TestADatabaseThatWasNeverOpenedIsStillReleased(t *testing.T) {
	kept := newStores()
	h := holdings(t, kept, &ledgers{}, nil)

	require.NoError(t, h.Destroy(t.Context(), kvSpec("shop", "QmNeverOpened")))

	opened, released := kept.did()
	require.Empty(t, opened, "the release served the request path a store: %v", opened)
	require.Equal(t, []string{backing.Prefix + "/shop/prod/kv/QmNeverOpened"}, released)
}

// TestARELEASETHATFAILEDISNOTARELEASE, and it is the caller that is told: a
// sweep that counted a failed drop as done would advertise this node as no
// longer a holder while its bytes are still here.
func TestAReleaseThatFailedSaysSo(t *testing.T) {
	kept := newStores()
	kept.releaseErr = errors.New("this store cannot release a database it holds")
	h := holdings(t, kept, &ledgers{}, nil)

	require.ErrorContains(t, h.Destroy(t.Context(), kvSpec("shop", "QmA")),
		"cannot release a database it holds")

	// AND A SPEC THAT ADDRESSES NOTHING IS REFUSED BEFORE ANYTHING IS TOUCHED.
	require.ErrorIs(t, h.Destroy(t.Context(), unaddressable()), database.ErrUnaddressable)
	_, released := kept.did()
	require.Empty(t, released)
}

// TestARELEASERACINGTHEREQUESTPATHISONETABLEANDONELOCK. Run with -race: a
// placement sweep letting go of a copy and a request opening one reach the same
// table, and a torn map here is a store nothing ever closes.
//
// WHAT THIS DOES NOT ASSERT, BECAUSE IT IS NOT TRUE. The drop from the table and
// the destroy are two steps: a request that opens the store between them leaves
// a live entry for a database that is then destroyed, and this node serves that
// closed store until it restarts. They are two steps BY DESIGN — holding the
// table's lock across a destroy would stop every open on the node for as long as
// the destroy takes — so the window is real and is stated rather than narrowed by
// an assertion that would only ever say the window was short. What IS asserted,
// deterministically, is the sequence that matters: see
// TestAReleasedDatabaseIsNotServedFromTheTableAgain.
func TestAReleaseRacingTheRequestPath(t *testing.T) {
	stores := newStores()
	h := holdings(t, stores, &ledgers{}, nil)

	specs := []database.Spec{kvSpec("shop", "QmA"), kvSpec("shop", "QmB"), kvSpec("other", "QmA")}

	var wg sync.WaitGroup
	for range 8 {
		for _, spec := range specs {
			wg.Add(2)
			go func(spec database.Spec) {
				defer wg.Done()
				store, err := h.Entries(t.Context(), spec)
				if err == nil {
					store.Put(t.Context(), "k", []byte("v"))
				}
			}(spec)
			go func(spec database.Spec) {
				defer wg.Done()
				h.Destroy(t.Context(), spec)
			}(spec)
		}
	}
	wg.Wait()

	// Every database still answers, and every release that ran named a database
	// rather than a torn fragment of one.
	for _, spec := range specs {
		store, err := h.Entries(t.Context(), spec)
		require.NoError(t, err)
		require.NotNil(t, store)
	}
	_, released := stores.did()
	require.NotEmpty(t, released)
	for _, name := range released {
		require.True(t, strings.HasPrefix(name, backing.Prefix+"/"), "released %q", name)
	}
}
