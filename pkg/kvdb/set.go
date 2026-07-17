package kvdb

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	dshelp "github.com/ipfs/boxo/datastore/dshelp"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	logging "github.com/ipfs/go-log/v2"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"

	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
)

var (
	elemsNs        = "s" // /elements namespace /set/s/<key>/<block>
	tombsNs        = "t" // /tombstones namespace /set/t/<key>/<block>
	keysNs         = "k" // /keys namespace /set/k/<key>/{v,p}
	valueSuffix    = "v" // for /keys namespace
	prioritySuffix = "p"
)

// Set specifies operations that the add-wins observed-removed set must fulfil.
type Set interface {
	Add(ctx context.Context, key string, value []byte) (Delta, error)
	Rmv(ctx context.Context, key string) (Delta, error)
	Merge(ctx context.Context, d Delta, id string) error
	Element(ctx context.Context, key string) ([]byte, error)
	Elements(ctx context.Context, q query.Query) (query.Results, error)
	InSet(ctx context.Context, key string) (bool, error)
}

// set implements an Add-Wins Observed-Remove Set using delta-CRDTs
// (https://arxiv.org/abs/1410.2803) and backing all the data in a
// go-datastore. It is fully agnostic to MerkleCRDTs or the delta distribution
// layer.  It chooses the Value with most priority for a Key as the current
// Value. When two values have the same priority, it chooses by alphabetically
// sorting their unique IDs alphabetically.
type set struct {
	store        ds.Datastore
	dagService   ipld.DAGService
	namespace    ds.Key
	putHook      func(key string, v []byte)
	deleteHook   func(key string)
	deltaFactory func() Delta
	logger       logging.StandardLogger

	// dagTimeout bounds how long a single DAG-fetch (GetDelta) issued
	// from the set may take, so that a missing/unreachable block cannot
	// turn a local operation into an indefinite network wait. 0 disables
	// the timeout (the caller's context is used as-is).
	dagTimeout time.Duration

	// Avoid merging two things at the same time since
	// we read-write value-priorities in a non-atomic way.
	putElemsMux sync.Mutex
}

func newCRDTSet(
	ctx context.Context,
	d ds.Datastore,
	namespace ds.Key,
	dagService ipld.DAGService,
	logger logging.StandardLogger,
	putHook func(key string, v []byte),
	deleteHook func(key string),
	deltaFactory func() Delta,
	dagTimeout time.Duration,
) (*set, error) {
	set := &set{
		namespace:    namespace,
		store:        d,
		dagService:   dagService,
		logger:       logger,
		putHook:      putHook,
		deleteHook:   deleteHook,
		deltaFactory: deltaFactory,
		dagTimeout:   dagTimeout,
	}

	return set, nil
}

// withDAGTimeout wraps ctx with s.dagTimeout when it is set (>0), so that
// DAG fetches issued by the set cannot block forever on a missing/slow
// block. The returned cancel function must always be called by the caller.
func (s *set) withDAGTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.dagTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.dagTimeout)
}

// Add returns a new delta-set adding the given key/value.
func (s *set) Add(ctx context.Context, key string, value []byte) (Delta, error) {
	delta := s.deltaFactory()
	delta.SetElements([]*pb.Element{
		{
			Key:   key,
			Value: value,
		},
	})
	return delta, nil
}

// Rmv returns a new delta-set removing the given key.
func (s *set) Rmv(ctx context.Context, key string) (Delta, error) {
	delta := s.deltaFactory()
	var tombs []*pb.Element

	// /namespace/<key>/elements
	prefix := s.elemsPrefix(key)
	q := query.Query{
		Prefix:   prefix.String(),
		KeysOnly: true,
	}

	results, err := s.store.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	//nolint:errcheck
	defer results.Close()

	for r := range results.Next() {
		if r.Error != nil {
			return nil, r.Error
		}
		id := strings.TrimPrefix(r.Key, prefix.String())
		if !ds.RawKey(id).IsTopLevel() {
			// our prefix matches blocks from other keys i.e. our
			// prefix is "hello" and we have a different key like
			// "hello/bye" so we have a block id like
			// "bye/<block>". If we got the right key, then the id
			// should be the block id only.
			continue
		}

		// check if its already tombed, which case don't add it to the
		// Rmv delta set.
		deleted, err := s.inTombsKeyID(ctx, key, id)
		if err != nil {
			return nil, err
		}
		if !deleted {
			tombs = append(tombs, &pb.Element{
				Key: key,
				Id:  id,
			})
		}
	}

	if len(tombs) > 0 {
		delta.SetTombstones(tombs)
	}

	return delta, nil
}

// Element retrieves the value of an element from the CRDT set.
func (s *set) Element(ctx context.Context, key string) ([]byte, error) {
	// We can only GET an element if it's part of the Set (in
	// "elements" and not in "tombstones").

	// * If the key has a value in the store it means that it has been
	//   written and is alive. putTombs will delete the value if all elems
	//   are tombstoned, or leave the best one.

	valueK := s.valueKey(key)
	value, err := s.store.Get(ctx, valueK)
	if err != nil { // not found is fine, we just return it
		return value, err
	}
	return value, nil
}

// Elements returns all the elements in the set.
func (s *set) Elements(ctx context.Context, q query.Query) (query.Results, error) {
	// This will cleanup user the query prefix first.
	// This makes sure the use of things like "/../" in the query
	// does not affect our setQuery.
	srcQueryPrefixKey := ds.NewKey(q.Prefix)

	keyNamespacePrefix := s.keyPrefix(keysNs)
	keyNamespacePrefixStr := keyNamespacePrefix.String()
	setQueryPrefix := keyNamespacePrefix.Child(srcQueryPrefixKey).String()
	vSuffix := "/" + valueSuffix

	// We are going to be reading everything in the /set/ namespace which
	// will return items in the form:
	// * /set/<key>/value
	// * /set<key>/priority (a Uvarint)

	// It is clear that KeysOnly=true should be used here when the original
	// query only wants keys.
	//
	// However, there is a question of what is best when the original
	// query wants also values:
	// * KeysOnly: true avoids reading all the priority key values
	//   which are skipped at the cost of doing a separate Get() for the
	//   values (50% of the keys).
	// * KeysOnly: false reads everything from the start. Priorities
	//   and tombstoned values are read for nothing
	//
	// KeysOnly retrieval can be faster with Pebble, at least for larger
	// values, due to pebble's ability to bypass value retrieval. This results
	// in reduced I/O, and reduced memory allocation and garbage collection.
	// Performance gains may be less significant with small values.
	//
	// We honor the caller's KeysOnly request directly on the underlying
	// query: the /v vs /p suffix filtering below only needs the keys, so
	// when the caller only wants keys there is no reason to pay for
	// reading any values (including tombstoned/priority values) at all.
	setQuery := query.Query{
		Prefix:   setQueryPrefix,
		KeysOnly: q.KeysOnly,
	}

	// send the result and returns false if we must exit.
	sendResult := func(ctx, qctx context.Context, r query.Result, out chan<- query.Result) bool {
		select {
		case out <- r:
		case <-ctx.Done():
			return false
		case <-qctx.Done():
			return false
		}
		return r.Error == nil
	}

	// The code below was very inspired in the Query implementation in
	// flatfs.

	// Originally we were able to set the output channel capacity and it
	// was set to 128 even though not much difference to 1 could be
	// observed on mem-based testing.

	// Using KeysOnly still gives a 128-item channel.
	// See: https://github.com/ipfs/go-datastore/issues/40
	r := query.ResultsWithContext(q, func(qctx context.Context, out chan<- query.Result) {
		// qctx is a Background context for the query. It is not
		// associated to ctx. It is closed when this function finishes
		// along with the output channel, or when the Results are
		// Closed directly.
		results, err := s.store.Query(ctx, setQuery)
		if err != nil {
			sendResult(ctx, qctx, query.Result{Error: err}, out)
			return
		}
		//nolint:errcheck
		defer results.Close()

		var entry query.Entry
		for r := range results.Next() {
			if r.Error != nil {
				sendResult(ctx, qctx, query.Result{Error: r.Error}, out)
				return
			}

			// We will be getting keys in the form of
			// /namespace/keys/<key>/v and /namespace/keys/<key>/p
			// We discard anything not ending in /v and sanitize
			// those from:
			// /namespace/keys/<key>/v -> <key>
			if !strings.HasSuffix(r.Key, vSuffix) { // "/v"
				continue
			}

			key := strings.TrimSuffix(
				strings.TrimPrefix(r.Key, keyNamespacePrefixStr),
				"/"+valueSuffix,
			)

			entry.Key = key
			entry.Value = r.Value
			entry.Size = r.Size
			entry.Expiration = r.Expiration

			// The fact that /v is set means it is not tombstoned,
			// as tombstoning removes /v and /p or sets them to
			// the best value.

			if q.KeysOnly {
				entry.Size = -1
				entry.Value = nil
			}
			if !sendResult(ctx, qctx, query.Result{Entry: entry}, out) {
				return
			}
		}
	})

	return r, nil
}

// InSet returns true if the key belongs to one of the elements in the "elems"
// set, and this element is not tombstoned.
func (s *set) InSet(ctx context.Context, key string) (bool, error) {
	// If we do not have a value this key was never added or it was fully
	// tombstoned.
	valueK := s.valueKey(key)
	return s.store.Has(ctx, valueK)
}

// /namespace/<key>
func (s *set) keyPrefix(key string) ds.Key {
	return s.namespace.ChildString(key)
}

// /namespace/elems/<key>
func (s *set) elemsPrefix(key string) ds.Key {
	return s.keyPrefix(elemsNs).ChildString(key)
}

// /namespace/tombs/<key>
func (s *set) tombsPrefix(key string) ds.Key {
	return s.keyPrefix(tombsNs).ChildString(key)
}

// /namespace/keys/<key>/value
func (s *set) valueKey(key string) ds.Key {
	return s.keyPrefix(keysNs).ChildString(key).ChildString(valueSuffix)
}

// /namespace/keys/<key>/priority
func (s *set) priorityKey(key string) ds.Key {
	return s.keyPrefix(keysNs).ChildString(key).ChildString(prioritySuffix)
}

// encodePriority varint-encodes prio+1, so that an empty byte slice remains
// distinguishable from an explicitly-stored priority of 0. Used both for the
// /keys/<key>/p entries and (since v2) for the /s/<key>/<id> element marker
// values.
func encodePriority(prio uint64) []byte {
	buf := make([]byte, binary.MaxVarintLen64)
	n := binary.PutUvarint(buf, prio+1)
	return buf[0:n]
}

// decodePriority is the inverse of encodePriority.
func decodePriority(data []byte) (uint64, error) {
	prio, n := binary.Uvarint(data)
	if n <= 0 {
		return prio, errors.New("error decoding priority")
	}
	return prio - 1, nil
}

// encodeMarker encodes an element marker value (the value stored at
// /s/<key>/<id>): varint(prio+1), optionally followed by an ALIAS CID's raw
// bytes. The alias is how a snapshot element (see putElems) that keeps its
// original id records which block now actually hosts its delta (the
// snapshot block it was folded into) -- the marker's path id alone no
// longer names that block once compaction has re-homed the element's
// storage without changing its identity.
//
// alias == cid.Undef ("none") encodes to exactly encodePriority(prio), so
// every marker written before this format existed (bare varint, or empty
// for legacy pre-v2 markers) remains a valid, alias-less encodeMarker
// output: this is purely additive, no storage migration needed.
func encodeMarker(prio uint64, alias cid.Cid) []byte {
	buf := encodePriority(prio)
	if !alias.Defined() {
		return buf
	}
	return append(buf, alias.Bytes()...)
}

// decodeMarker is the inverse of encodeMarker. The leading varint
// (self-delimiting) is decoded as a priority exactly like decodePriority;
// any remaining bytes are cast as the alias CID. No remainder means no
// alias (cid.Undef) -- this is what makes every pre-alias marker (bare
// varint priority, or an empty legacy marker handled by the caller before
// ever reaching here) decode correctly with an Undef alias.
func decodeMarker(data []byte) (prio uint64, alias cid.Cid, err error) {
	p, n := binary.Uvarint(data)
	if n <= 0 {
		return 0, cid.Undef, errors.New("error decoding marker")
	}
	prio = p - 1

	rest := data[n:]
	if len(rest) == 0 {
		return prio, cid.Undef, nil
	}
	alias, err = cid.Cast(rest)
	if err != nil {
		return 0, cid.Undef, err
	}
	return prio, alias, nil
}

// idToCid converts an element/tombstone marker id -- the "/<base32...>"
// datastore-key string convention used throughout this file for both
// marker path components and block keys (always a ds.Key.String(), always
// absolute) -- to the CID of the DAG block it names. This is the inverse of
// the blockKey computation in processNode (dshelp.MultihashToDsKey(...).
// String()) and is used everywhere a marker's path id or alias needs to be
// compared against a set of DAG block CIDs.
func idToCid(id string) (cid.Cid, error) {
	mhash, err := dshelp.DsKeyToMultihash(ds.NewKey(id))
	if err != nil {
		return cid.Undef, err
	}
	return cid.NewCidV1(cid.DagProtobuf, mhash), nil
}

func (s *set) getPriority(ctx context.Context, key string) (uint64, error) {
	prioK := s.priorityKey(key)
	data, err := s.store.Get(ctx, prioK)
	if err != nil {
		if err == ds.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}

	return decodePriority(data)
}

func (s *set) setPriority(ctx context.Context, writeStore ds.Write, key string, prio uint64) error {
	prioK := s.priorityKey(key)
	buf := encodePriority(prio)
	if len(buf) == 0 {
		return errors.New("error encoding priority")
	}

	return writeStore.Put(ctx, prioK, buf)
}

// sets a value if priority is higher. When equal, it sets if the
// value is lexicographically higher than the current value.
func (s *set) setValue(ctx context.Context, writeStore ds.Write, key, id string, value []byte, prio uint64) error {
	// If this key was tombstoned already, do not store/update the value.
	deleted, err := s.inTombsKeyID(ctx, key, id)
	if err != nil || deleted {
		return err
	}

	curPrio, err := s.getPriority(ctx, key)
	if err != nil {
		return err
	}

	if prio < curPrio {
		return nil
	}
	valueK := s.valueKey(key)

	if prio == curPrio {
		curValue, _ := s.store.Get(ctx, valueK)
		// new value greater than old
		if bytes.Compare(curValue, value) >= 0 {
			return nil
		}
	}

	// store value
	err = writeStore.Put(ctx, valueK, value)
	if err != nil {
		return err
	}

	// store priority
	err = s.setPriority(ctx, writeStore, key, prio)
	if err != nil {
		return err
	}

	// trigger add hook
	s.putHook(key, value)
	return nil
}

// fetchDelta retrieves and unmarshals the delta stored in the DAG block
// identified by c, bounding the fetch with the set's dagTimeout (Item E)
// when configured.
func (s *set) fetchDelta(ctx context.Context, ng crdtNodeGetter, c cid.Cid) (Delta, error) {
	fctx, cancel := s.withDAGTimeout(ctx)
	defer cancel()

	_, deltaBytes, err := ng.GetDelta(fctx, c)
	if err != nil {
		return nil, err
	}

	delta := s.deltaFactory()
	if err := delta.Unmarshal(deltaBytes); err != nil {
		return nil, err
	}
	return delta, nil
}

// findBestValue looks for all entries for the given key, figures out their
// priority (from the element marker when available, falling back to
// fetching their delta from the DAG for legacy markers written before
// v2 -- see migrate1to2) and returns the value with the highest priority
// that is not tombstoned nor about to be tombstoned (skipping the blocks
// in pendingTombIDs).
//
// Only the delta(s) for the highest-priority candidate(s) are ever fetched
// from the DAG (needed to obtain the actual value, and to break ties by
// picking the lexicographically-greatest value), which is usually a single
// fetch even for keys with many versions.
func (s *set) findBestValue(ctx context.Context, key string, pendingTombIDs []string) ([]byte, uint64, error) {
	// /namespace/elems/<key>
	prefix := s.elemsPrefix(key)
	q := query.Query{
		Prefix:   prefix.String(),
		KeysOnly: false,
	}

	results, err := s.store.Query(ctx, q)
	if err != nil {
		return nil, 0, err
	}
	//nolint:errcheck
	defer results.Close()

	// a surviving (non-tombstoned, non-pending) element marker: its
	// priority (known either from the marker value or, for legacy
	// markers, from having fetched its delta already) and enough
	// information to fetch its delta's value later if it turns out to
	// be a max-priority candidate. cid is the CID to fetch the delta
	// from: the marker's alias when it has one (the element was
	// re-homed by compaction -- see putElems/S2), otherwise the CID
	// derived from the marker's own path id, same as before aliases
	// existed.
	type candidate struct {
		cid      cid.Cid
		priority uint64
		delta    Delta // non-nil if already fetched while resolving a legacy marker
	}

	ng := crdtNodeGetter{NodeGetter: s.dagService}

	var candidates []candidate
	var maxPriority uint64

	// range all the /namespace/elems/<key>/<block_cid>.
NEXT:
	for r := range results.Next() {
		if r.Error != nil {
			return nil, 0, r.Error
		}

		id := strings.TrimPrefix(r.Key, prefix.String())
		if !ds.RawKey(id).IsTopLevel() {
			// our prefix matches blocks from other keys i.e. our
			// prefix is "hello" and we have a different key like
			// "hello/bye" so we have a block id like
			// "bye/<block>". If we got the right key, then the id
			// should be the block id only.
			continue
		}
		// if block is one of the pending tombIDs, continue
		for _, tombID := range pendingTombIDs {
			if tombID == id {
				continue NEXT
			}
		}

		// if tombstoned, continue
		inTomb, err := s.inTombsKeyID(ctx, key, id)
		if err != nil {
			return nil, 0, err
		}
		if inTomb {
			continue
		}

		pathCid, err := idToCid(id)
		if err != nil {
			return nil, 0, err
		}

		c := candidate{cid: pathCid}
		if len(r.Value) > 0 {
			// marker carries the priority (v2+): no DAG fetch needed.
			// It may also carry an alias (the element was re-homed
			// by compaction -- see putElems/S2): when present, the
			// alias, not the path id, names the block that now
			// hosts this element's delta.
			prio, alias, err := decodeMarker(r.Value)
			if err != nil {
				return nil, 0, err
			}
			c.priority = prio
			if alias.Defined() {
				c.cid = alias
			}
		} else {
			// legacy (pre-v2) marker: fall back to fetching the
			// delta to learn its priority. Keep the fetched delta
			// around in case this candidate ends up being a
			// max-priority one, to avoid fetching it twice.
			delta, err := s.fetchDelta(ctx, ng, pathCid)
			if err != nil {
				return nil, 0, err
			}
			c.priority = delta.GetPriority()
			c.delta = delta
		}

		if len(candidates) == 0 || c.priority > maxPriority {
			maxPriority = c.priority
		}
		candidates = append(candidates, c)
	}

	if len(candidates) == 0 {
		return nil, 0, nil
	}

	var bestValue []byte
	haveBest := false
	for _, c := range candidates {
		if c.priority != maxPriority {
			continue
		}

		delta := c.delta
		if delta == nil {
			var err error
			delta, err = s.fetchDelta(ctx, ng, c.cid)
			if err != nil {
				return nil, 0, err
			}
		}

		// Among the values for our key in this delta, keep the
		// greatest (there can only be one under normal usage, but
		// custom delta implementations could set more).
		var greatestValueInDelta []byte
		elems, err := delta.GetElements()
		if err != nil {
			return nil, 0, err
		}
		for _, elem := range elems {
			if elem.GetKey() != key {
				continue
			}
			v := elem.GetValue()
			if bytes.Compare(greatestValueInDelta, v) < 0 {
				greatestValueInDelta = v
			}
		}

		if !haveBest {
			bestValue = greatestValueInDelta
			haveBest = true
			continue
		}
		// equal priority (ties among max-priority candidates): choose
		// the greatest value.
		if bytes.Compare(bestValue, greatestValueInDelta) < 0 {
			bestValue = greatestValueInDelta
		}
	}

	return bestValue, maxPriority, nil
}

// compactSnapshotState computes Compact's live-element and carried-
// tombstone results for every key in touched, in a single pass over the
// whole elems and tombs namespaces, grouping results in memory by key,
// instead of running two datastore Queries per touched key.
//
// This matters for the same reason Item A's putTombs batching did: a naive
// (e.g. in-memory) datastore's Query() scans the whole store regardless of
// the requested prefix, so one Query per key turns compaction of a big
// store into O(touched keys) x O(store size) work. A single pass over each
// namespace keeps it O(store size) overall, with the per-key result
// selection (priority/tie-break, carry-or-drop) done in memory.
//
// Live-value candidate selection and the priority/tie-break rule mirror
// findBestValue exactly (see Item B/G3), scoped to element markers whose
// alias-or-path CID (the alias when the marker was re-homed by an earlier
// compaction generation, otherwise the CID derived from its own path id --
// see putElems/S2 and scopeOrFetchCid below) is in dagCIDSet (the set the
// calling DAG's walk just found reachable) -- the elems/tombs namespaces are
// shared across all dagNames writing to the same key (see
// TestPurgeDAGMixedKey), so without the scope restriction compacting one
// dagName could pick up markers written by a different one. Resolving scope
// through the alias (rather than only the path id) is what makes a SECOND
// compaction generation see a marker re-homed by the first one: after gen-1,
// the surviving marker for a key is (key, original id) aliased to the gen-1
// snapshot block, and gen-2's walk finds that snapshot block, not the
// original (now-purged) id. The carried-tombstone rule is Compact's
// two-generation rule (see the Compact doc comment). Every element this
// produces carries its winner marker's ORIGINAL path id (not the block
// that happens to host it), preserving element identity across every future
// generation and every concurrent tombstone that targets it.
//
// Every DAG fetch here (needed to resolve a max-priority candidate's value,
// or a legacy empty-value marker's priority) is expected to hit the local
// blockstore only: dagCIDSet was itself built from a local-only walk.
//
// Caveat: recovering a key from a marker's datastore path (to group by key)
// only round-trips for keys following the ds.Key.String() convention
// (always starting with "/"), which is exactly what every call through the
// public Datastore.Put/Delete/Batch API produces. A caller that reaches the
// advanced MerkleCRDT.Set().Add/Rmv API directly with a key that does not
// start with "/" will see that key silently dropped by Compact (its
// history purged with no snapshot successor) rather than preserved -- do
// not do that.
func (s *set) compactSnapshotState(ctx context.Context, touched map[string]dagWalkKind, dagCIDSet map[cid.Cid]struct{}) ([]*pb.Element, []*pb.Element, error) {
	type elemMarker struct {
		id       string
		cid      cid.Cid // CID derived from the marker's own path id.
		alias    cid.Cid // cid.Undef unless the marker was re-homed by compaction (see putElems/S2).
		priority uint64
		hasPrio  bool // false for legacy (pre-v2, empty-value) markers.
	}

	// scopeOrFetchCid is the CID a marker resolves to for DAG-scope
	// ("does this marker belong to the DAG being compacted") and delta-fetch
	// purposes alike: its alias when it has one -- the element was re-homed
	// by an earlier compaction generation and is now hosted by that alias
	// block -- otherwise the CID derived from its own path id. This is what
	// makes second-generation compaction work: after gen-1, the surviving
	// marker for a key is (key, original id) with alias == the gen-1
	// snapshot block, and gen-2's dagCIDSet contains that snapshot block,
	// not the original id's now-purged block.
	scopeOrFetchCid := func(m elemMarker) cid.Cid {
		if m.alias.Defined() {
			return m.alias
		}
		return m.cid
	}

	// keyAndIDFromMarkerPath splits a full "/<elemsOrTombsNs>/<key...>/<id>"
	// datastore key (as returned by a namespace-wide Query) back into its
	// (key, id) parts, preserving the leading "/" on both: every key
	// flowing through set.Add/Rmv (and therefore into pb.Element.Key and
	// the "touched" map below) is a ds.Key.String(), always absolute (e.g.
	// "/foo", never "foo") -- and every existing "id" (e.g. the block keys
	// findBestValue/Rmv extract via the identical
	// strings.TrimPrefix(r.Key, prefix.String()) idiom, or the blockKey
	// processNode passes into Merge) is, likewise, always
	// dshelp.MultihashToDsKey(...).String(), always absolute. id must match
	// that convention exactly (not just its normalized CID) because
	// findBestValue's pendingTombIDs skip is a raw string comparison, not a
	// CID/Key-normalized one.
	keyAndIDFromMarkerPath := func(nsPrefix, fullKey string) (key, id string) {
		rel := strings.TrimPrefix(fullKey, nsPrefix) // "/<key...>/<idName>"
		idName := ds.NewKey(rel).Name()
		key = strings.TrimSuffix(rel, "/"+idName)
		id = "/" + idName
		return key, id
	}

	elemsByKey := make(map[string][]elemMarker)
	elemsNsPrefix := s.keyPrefix(elemsNs)
	{
		q := query.Query{Prefix: elemsNsPrefix.String(), KeysOnly: false}
		results, err := s.store.Query(ctx, q)
		if err != nil {
			return nil, nil, err
		}
		defer results.Close() //nolint:errcheck

		for r := range results.Next() {
			if r.Error != nil {
				return nil, nil, r.Error
			}
			key, id := keyAndIDFromMarkerPath(elemsNsPrefix.String(), r.Key)
			if _, ok := touched[key]; !ok {
				continue
			}
			pathCid, err := idToCid(id)
			if err != nil {
				return nil, nil, err
			}
			m := elemMarker{id: id, cid: pathCid}
			if len(r.Value) > 0 {
				prio, alias, err := decodeMarker(r.Value)
				if err != nil {
					return nil, nil, err
				}
				m.priority = prio
				m.hasPrio = true
				m.alias = alias
			}
			elemsByKey[key] = append(elemsByKey[key], m)
		}
	}

	tombsByKey := make(map[string][]string)
	tombsNsPrefix := s.keyPrefix(tombsNs)
	{
		q := query.Query{Prefix: tombsNsPrefix.String(), KeysOnly: true}
		results, err := s.store.Query(ctx, q)
		if err != nil {
			return nil, nil, err
		}
		defer results.Close() //nolint:errcheck

		for r := range results.Next() {
			if r.Error != nil {
				return nil, nil, r.Error
			}
			key, id := keyAndIDFromMarkerPath(tombsNsPrefix.String(), r.Key)
			if _, ok := touched[key]; !ok {
				continue
			}
			tombsByKey[key] = append(tombsByKey[key], id)
		}
	}

	ng := crdtNodeGetter{NodeGetter: s.dagService}

	keys := make([]string, 0, len(touched))
	for k := range touched {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	type candidate struct {
		id    string
		cid   cid.Cid
		prio  uint64
		delta Delta // non-nil if already fetched while resolving a legacy marker.
	}

	var elements, tombstones []*pb.Element
	for _, key := range keys {
		tombstonedIDs := make(map[string]struct{}, len(tombsByKey[key]))
		for _, id := range tombsByKey[key] {
			tombstonedIDs[id] = struct{}{}
		}

		// Live contribution.
		var candidates []candidate
		var maxPriority uint64
		for _, m := range elemsByKey[key] {
			scopeCid := scopeOrFetchCid(m)
			if _, ok := dagCIDSet[scopeCid]; !ok {
				continue // belongs to a different DAG's history.
			}
			if _, tombed := tombstonedIDs[m.id]; tombed {
				continue
			}

			c := candidate{id: m.id, cid: scopeCid}
			if m.hasPrio {
				c.prio = m.priority
			} else {
				delta, err := s.fetchDelta(ctx, ng, scopeCid)
				if err != nil {
					return nil, nil, err
				}
				c.prio = delta.GetPriority()
				c.delta = delta
			}

			if len(candidates) == 0 || c.prio > maxPriority {
				maxPriority = c.prio
			}
			candidates = append(candidates, c)
		}

		if len(candidates) > 0 {
			var bestValue []byte
			var bestID string
			haveBest := false
			for _, c := range candidates {
				if c.prio != maxPriority {
					continue
				}
				delta := c.delta
				if delta == nil {
					var err error
					delta, err = s.fetchDelta(ctx, ng, c.cid)
					if err != nil {
						return nil, nil, err
					}
				}

				var greatestValueInDelta []byte
				elems, err := delta.GetElements()
				if err != nil {
					return nil, nil, err
				}
				for _, elem := range elems {
					if elem.GetKey() != key {
						continue
					}
					v := elem.GetValue()
					if bytes.Compare(greatestValueInDelta, v) < 0 {
						greatestValueInDelta = v
					}
				}

				if !haveBest {
					bestValue = greatestValueInDelta
					bestID = c.id
					haveBest = true
					continue
				}
				if bytes.Compare(bestValue, greatestValueInDelta) < 0 {
					bestValue = greatestValueInDelta
					bestID = c.id
				}
			}
			// The winner's ORIGINAL id (its marker's path id, always
			// carrying the leading-slash ds.Key.String() convention --
			// see keyAndIDFromMarkerPath) rides through into the
			// snapshot element, so that every future generation (and
			// any concurrent tombstone targeting it) keeps addressing
			// the exact same element identity regardless of which
			// block currently hosts its value.
			elements = append(elements, &pb.Element{Key: key, Id: bestID, Value: bestValue, Priority: maxPriority})
		}

		// Carried tombstones.
		if len(tombsByKey[key]) == 0 {
			continue
		}
		liveIDs := make(map[string]struct{}, len(elemsByKey[key]))
		for _, m := range elemsByKey[key] {
			liveIDs[m.id] = struct{}{}
		}
		for _, id := range tombsByKey[key] {
			targetCid, err := idToCid(id)
			if err != nil {
				return nil, nil, err
			}

			_, purgingNow := dagCIDSet[targetCid]
			_, elemStillLive := liveIDs[id]
			if purgingNow || elemStillLive {
				tombstones = append(tombstones, &pb.Element{Key: key, Id: id})
			}
		}
	}

	return elements, tombstones, nil
}

// putElems adds items to the "elems" set. It will also set current
// values and priorities for each element. This needs to run in a lock,
// as otherwise races may occur when reading/writing the priorities, resulting
// in bad behaviours.
//
// Technically the lock should only affect the keys that are being written,
// but with the batching optimization the locks would need to be hold until
// the crdtBatch is written), and one lock per key might be way worse than a single
// global lock in the end.
//
// For a NON-snapshot delta every element's Id is overwritten with id (the
// key of the block being merged), exactly as before this element carried an
// Id at all.
//
// For a snapshot delta (isSnapshot == true, id is the snapshot block's own
// key), an element is immutable once created, so re-homing its storage into
// a snapshot block must not change its identity: a concurrent tombstone
// (key, originalId) has to keep covering it. So the element's ORIGINAL id
// (e.GetId(), carried by the snapshot delta -- see compactSnapshotState) is
// kept as-is, the marker is written under that original id, and the marker
// value's alias records that this snapshot block (id) is what now hosts the
// element's delta -- findBestValue, purgeKeyBlocks and
// compactSnapshotState all resolve "where does this element's value live"
// via alias-or-path from that point on. A snapshot element that
// (defensively) arrives with an empty Id falls back to the old
// overwrite-with-block-id behavior, same as a non-snapshot element.
func (s *set) putElems(ctx context.Context, elems []*pb.Element, id string, prio uint64, isSnapshot bool) error {
	s.putElemsMux.Lock()
	defer s.putElemsMux.Unlock()

	if len(elems) == 0 {
		return nil
	}

	var store ds.Write = s.store
	var err error
	batchingDs, batching := store.(ds.Batching)
	if batching {
		store, err = batchingDs.Batch(ctx)
		if err != nil {
			return err
		}
	}

	// snapshotBlockCid is the alias every re-homed element in this delta
	// (if any) will point at: the snapshot block itself, derived from its
	// own key exactly like every other id->CID conversion in this file.
	// Only computed when needed since it is a no-op fetch-free
	// conversion but there is no reason to pay it for the common,
	// non-snapshot case.
	var snapshotBlockCid cid.Cid
	if isSnapshot {
		snapshotBlockCid, err = idToCid(id)
		if err != nil {
			return err
		}
	}

	for _, e := range elems {
		key := e.GetKey()

		// The element's effective priority is normally the delta's
		// priority, except in snapshot deltas (Item G) where each
		// element carries its own original priority so that a
		// snapshot restores exactly the same per-key priorities on
		// every replica instead of inflating them all to the
		// snapshot's height. 0 means "use the delta's priority",
		// which covers all pre-snapshot data.
		eprio := e.GetPriority()
		if eprio == 0 {
			eprio = prio
		}

		elemID := id
		alias := cid.Undef
		if isSnapshot && e.GetId() != "" {
			elemID = e.GetId() // keep the original id -- do NOT overwrite.
			alias = snapshotBlockCid
		} else {
			e.Id = id // overwrite the identifier as it would come unset
		}

		// /namespace/elems/<key>/<elemID>
		// The marker value carries this element's effective priority
		// (varint(prio+1), same convention as setPriority) so that
		// findBestValue can later learn it without fetching the delta
		// block from the DAG, plus (for re-homed snapshot elements)
		// the alias CID of the block that now hosts it. See
		// migrate1to2 for backfilling markers written before the
		// priority was introduced.
		k := s.elemsPrefix(key).ChildString(elemID)
		err := store.Put(ctx, k, encodeMarker(eprio, alias))
		if err != nil {
			return err
		}

		// update the value if applicable:
		// * higher priority than we currently have.
		// * not tombstoned before.
		err = s.setValue(ctx, store, key, elemID, e.GetValue(), eprio)
		if err != nil {
			return err
		}
	}

	if batching {
		err := store.(ds.Batch).Commit(ctx)
		if err != nil {
			return err
		}
	}
	return nil
}

// putTombs writes the given tombstones into the store. It groups them by
// key first, so that a single Delete() removing N versions of the same key
// (which produces N tombstones in one delta) only pays for a single
// findBestValue scan per key instead of one per tombstone (Item A): the
// intermediate states after each individual tombstone are never observed by
// anyone else (they all land in the same uncommitted crdtBatch), so only the
// final value/priority for each key matters, and that is exactly what
// calling findBestValue once, with the full set of pending tombstone IDs
// for that key, computes.
//
// id and isSnapshot mirror putElems' parameters: for a snapshot delta's
// CARRIED tombstones (the two-generation rule -- see Compact's doc
// comment), the marker is written with an alias pointing at this snapshot
// block (id), exactly like a re-homed element's marker (S2). This closes a
// self-purge gap that has nothing to do with element-id stability directly,
// but that stable ids make newly reachable now that Compact no longer
// requires a quiesced dagName: a carried tombstone's target id is, in the
// common case, the very id being purged THIS SAME Compact() run
// (compactSnapshotState's "purgingNow" carry reason). Without an alias,
// purgeKeyBlocks would delete that just-(re)written tombstone marker in the
// very same run that wrote it (its path-id CID is trivially in the purge
// set), silently discarding the compacting replica's own record of the
// kill -- so a later-arriving element for that id (from a replica that
// diverged before the delete, exactly the scenario Compact must now
// tolerate) would wrongly resurrect the key locally. The alias keeps the
// carried tombstone alive across this generation's purge the same way an
// aliased element marker survives it (S4), and is otherwise inert: a plain
// (non-snapshot) tombstone is written with a nil value exactly as before,
// so this is purely additive to the wire/storage format, matching the
// alias convention already established for elements.
func (s *set) putTombs(ctx context.Context, tombs []*pb.Element, id string, isSnapshot bool) error {
	if len(tombs) == 0 {
		return nil
	}

	var store ds.Write = s.store
	var err error
	batchingDs, batching := store.(ds.Batching)
	if batching {
		store, err = batchingDs.Batch(ctx)
		if err != nil {
			return err
		}
	}

	var tombValue []byte
	if isSnapshot {
		blockCid, err := idToCid(id)
		if err != nil {
			return err
		}
		tombValue = encodeMarker(0, blockCid)
	}

	// key -> tombstoned block IDs. Carries the tombstoned blocks for each
	// element in this delta. keyOrder preserves first-seen order so that
	// results (and the delete hook below) are deterministic.
	deletedElems := make(map[string][]string)
	var keyOrder []string
	for _, e := range tombs {
		// /namespace/tombs/<key>/<id>
		key := e.GetKey()
		tombID := e.GetId()
		if _, ok := deletedElems[key]; !ok {
			keyOrder = append(keyOrder, key)
		}
		deletedElems[key] = append(deletedElems[key], tombID)

		// Write tomb into store.
		k := s.tombsPrefix(key).ChildString(tombID)
		if err := store.Put(ctx, k, tombValue); err != nil {
			return err
		}
	}

	for _, key := range keyOrder {
		valueK := s.valueKey(key)

		// Find best surviving value for the key, now that all its
		// tombstones from this delta are known.
		v, p, err := s.findBestValue(ctx, key, deletedElems[key])
		if err != nil {
			return err
		}

		var errs []error
		if v == nil {
			if err := store.Delete(ctx, valueK); err != nil {
				errs = append(errs, err)
			}
			if err := store.Delete(ctx, s.priorityKey(key)); err != nil {
				errs = append(errs, err)
			}
		} else {
			if err := store.Put(ctx, valueK, v); err != nil {
				errs = append(errs, err)
			}
			if err := s.setPriority(ctx, store, key, p); err != nil {
				errs = append(errs, err)
			}
		}
		if err := errors.Join(errs...); err != nil {
			return err
		}
	}

	if batching {
		err := store.(ds.Batch).Commit(ctx)
		if err != nil {
			return err
		}
	}

	// run delete hook only once for all versions of the same element
	// tombstoned in this delta. Note it may be that the element was not
	// fully deleted and only a different value took its place.
	for _, key := range keyOrder {
		s.deleteHook(key)
	}

	return nil
}

func (s *set) Merge(ctx context.Context, d Delta, id string) error {
	tombs, err := d.GetTombstones()
	if err != nil {
		return err
	}

	elems, err := d.GetElements()
	if err != nil {
		return err
	}

	isSnapshot := d.IsSnapshot()

	err = s.putTombs(ctx, tombs, id, isSnapshot)
	if err != nil {
		return err
	}

	return s.putElems(ctx, elems, id, d.GetPriority(), isSnapshot)
}

// currently unused
// func (s *set) inElemsKeyID(key, id string) (bool, error) {
// 	k := s.elemsPrefix(key).ChildString(id)
// 	return s.store.Has(k)
// }

func (s *set) inTombsKeyID(ctx context.Context, key, id string) (bool, error) {
	k := s.tombsPrefix(key).ChildString(id)
	return s.store.Has(ctx, k)
}

// currently unused
// // inSet returns if the given cid/block is in elems and not in tombs (and
// // thus, it is an element of the set).
// func (s *set) inSetKeyID(key, id string) (bool, error) {
// 	inTombs, err := s.inTombsKeyID(key, id)
// 	if err != nil {
// 		return false, err
// 	}
// 	if inTombs {
// 		return false, nil
// 	}

// 	return s.inElemsKeyID(key, id)
// }

// purgeKeyBlocks removes element and tombstone entries for the given key that
// were created by any of the given block CIDs. After removal, it recomputes
// the best value from surviving elements. If no elements survive, the key's
// value and priority are deleted.
//
// hasElems and hasTombs indicate which namespaces the DAG being purged
// actually wrote entries for this key; passing false for either skips the
// corresponding datastore query.
func (s *set) purgeKeyBlocks(ctx context.Context, key string, blockCIDs map[cid.Cid]struct{}, hasElems, hasTombs bool) error {
	s.putElemsMux.Lock()
	defer s.putElemsMux.Unlock()

	var store ds.Write = s.store
	batchingDs, batching := store.(ds.Batching)
	var err error
	if batching {
		store, err = batchingDs.Batch(ctx)
		if err != nil {
			return err
		}
	}

	// deleteMatchingIDs deletes the markers under prefix that the given
	// purge set covers. What "covers" means differs between the two marker
	// namespaces because an alias means a different thing for each -- see
	// the aliasIsHost parameter:
	//
	// aliasIsHost == true (elems): a marker's alias is the block that now
	// HOSTS its element's value (it was re-homed into a snapshot -- see
	// putElems/S2). The marker's value can only ever be fetched from that
	// hosting block, so the marker survives iff its host (its alias when it
	// has one, else the block named by its own path id) survives the purge.
	// This deletes a "loser" element that an earlier generation folded into
	// a snapshot: it keeps its original path id -- whose block that earlier
	// generation already purged, so it is NOT in this purge set -- but its
	// alias points at that now-being-purged snapshot. Leaving it behind
	// would accumulate a stale, unfetchable marker that a later
	// findBestValue could pick as a survivor (fetching a purged block, and
	// resurrecting a lower-priority value once the current winner is
	// tombstoned by a snapshot-only replica). The current winner, by
	// contrast, was just re-aliased to the NEW snapshot (not in this purge
	// set), so its host survives and it is correctly kept.
	//
	// aliasIsHost == false (tombs): a tombstone has no value to host; its
	// alias only records that a snapshot RE-AFFIRMED the kill (putTombs).
	// It is keyed on its target's own id (path): kept whenever its target
	// block is not in this purge set (its target was purged by an earlier
	// generation, so this is inert two-generation residue -- tiny and
	// harmless, see TestCompactTwoGenerationTombstones), and, when its
	// target IS in the purge set, kept iff a snapshot outside the purge set
	// re-affirmed it (so the kill outlives the purge of the target's block
	// and a divergent replica cannot resurrect it). A plain, alias-less
	// tomb (the common case) is deleted exactly as before aliasing existed.
	deleteMatchingIDs := func(prefix ds.Key, aliasIsHost bool) error {
		q := query.Query{
			Prefix:   prefix.String(),
			KeysOnly: false,
		}
		results, err := s.store.Query(ctx, q)
		if err != nil {
			return err
		}
		defer results.Close() //nolint:errcheck

		for r := range results.Next() {
			if r.Error != nil {
				return r.Error
			}
			// Strip the query prefix to get the relative remainder, which for a
			// direct child is just the block ID (a CID encoded as a datastore key
			// string): "/namespace/s/foo/<blockID>" → "/<blockID>".
			blockID := strings.TrimPrefix(r.Key, prefix.String())
			// The prefix query also returns entries for child keys: prefix "foo"
			// matches both "foo/<block>" and "foo/bar/<block>". After trimming, a
			// direct child yields a top-level key "/<blockID>", while a grandchild
			// yields "/bar/<blockID>". Reject the latter since it belongs to a
			// different key.
			if !ds.RawKey(blockID).IsTopLevel() {
				continue
			}
			// Decode the datastore key back into a CID so we can look it up in the
			// caller-supplied set.
			pathCid, err := idToCid(blockID)
			if err != nil {
				return err
			}
			var alias cid.Cid
			if len(r.Value) > 0 {
				_, alias, err = decodeMarker(r.Value)
				if err != nil {
					return err
				}
			}

			if aliasIsHost {
				// Element marker: delete iff the block hosting its value
				// (alias if re-homed, else its own path id) is purged.
				host := pathCid
				if alias.Defined() {
					host = alias
				}
				if _, ok := blockCIDs[host]; !ok {
					continue
				}
			} else {
				// Tomb marker: keyed on the target's own path id, with a
				// re-affirming snapshot alias keeping it alive across the
				// purge of that target block.
				if _, ok := blockCIDs[pathCid]; !ok {
					continue
				}
				if alias.Defined() {
					if _, aliasPurged := blockCIDs[alias]; !aliasPurged {
						continue
					}
				}
			}
			if err := store.Delete(ctx, prefix.ChildString(blockID)); err != nil {
				return err
			}
		}
		return nil
	}

	if hasElems {
		if err := deleteMatchingIDs(s.elemsPrefix(key), true); err != nil {
			return err
		}
	}
	if hasTombs {
		if err := deleteMatchingIDs(s.tombsPrefix(key), false); err != nil {
			return err
		}
	}

	// The delete crdtBatch and the value/priority rewrite below are not atomic.
	// A crash between them leaves a stale value key, but PurgeDAG is
	// idempotent: a retry will skip the already-deleted entries and rewrite
	// the value correctly.
	if batching {
		if err := store.(ds.Batch).Commit(ctx); err != nil {
			return err
		}
	}

	// Recompute best value from surviving elements. Entries are already
	// deleted from the store, so nil pendingTombIDs is correct.
	bestVal, bestPrio, err := s.findBestValue(ctx, key, nil)
	if err != nil {
		return err
	}

	// Hook-quiet on no-ops (Item R8): a purge that does not actually change
	// a surviving key's value/priority, or that targets a key whose value
	// entry was already absent, must not fire a spurious putHook/deleteHook.
	// This matters for callers like receiver-side reclaim (reclaimCovered)
	// where the same live key is very commonly untouched by the generation
	// being purged. A real change still always writes and fires its hook.
	valueK := s.valueKey(key)
	if bestVal == nil {
		_, getErr := s.store.Get(ctx, valueK)
		alreadyAbsent := errors.Is(getErr, ds.ErrNotFound)

		var errs []error
		if err := s.store.Delete(ctx, valueK); err != nil && !errors.Is(err, ds.ErrNotFound) {
			errs = append(errs, err)
		}
		if err := s.store.Delete(ctx, s.priorityKey(key)); err != nil && !errors.Is(err, ds.ErrNotFound) {
			errs = append(errs, err)
		}
		if err := errors.Join(errs...); err != nil {
			return err
		}
		if !alreadyAbsent {
			s.deleteHook(key)
		}
	} else {
		curVal, getErr := s.store.Get(ctx, valueK)
		curPrio, prioErr := s.getPriority(ctx, key)
		unchanged := getErr == nil && prioErr == nil && bytes.Equal(curVal, bestVal) && curPrio == bestPrio

		if !unchanged {
			if err := s.store.Put(ctx, valueK, bestVal); err != nil {
				return err
			}
			if err := s.setPriority(ctx, s.store, key, bestPrio); err != nil {
				return err
			}
			s.putHook(key, bestVal)
		}
	}

	return nil
}

// perform a sync against all the paths associated with a key prefix
func (s *set) datastoreSync(ctx context.Context, prefix ds.Key) error {
	prefixStr := prefix.String()
	toSync := []ds.Key{
		s.elemsPrefix(prefixStr),
		s.tombsPrefix(prefixStr),
		s.keyPrefix(keysNs).Child(prefix), // covers values and priorities
	}

	errs := make([]error, len(toSync))

	for i, k := range toSync {
		if err := s.store.Sync(ctx, k); err != nil {
			errs[i] = err
		}
	}

	return errors.Join(errs...)
}
