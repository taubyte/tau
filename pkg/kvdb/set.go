package kvdb

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"

	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	logging "github.com/ipfs/go-log/v2"
)

// The set is an add-wins observed-remove set of (key, dot) elements with a
// causal context (see causalContext), laid out as:
//
//	/e/<key segment>/<dot segment>  live element: varint priority, at and
//	                                ttl, then value
//	/t/<dot segment>/<key segment>  pending removal: the element is named by a
//	                                tombstone but its dot is not covered; varint
//	                                at and ttl of the removal
//	/k/<key>/v, /k/<key>/p          the key's winning value and its priority
//
// A key's value is its live element with the highest priority, ties broken by
// the greatest value, so every replica holding the same elements reads the
// same value.
const (
	elemsNs        = "e"
	pendingNs      = "t"
	keysNs         = "k"
	valueSuffix    = "v"
	prioritySuffix = "p"
)

type set struct {
	store     ds.Datastore
	namespace ds.Key
	// elems, pending and keys are the prefixes of the elements, the
	// pending removals and the values.
	elems, pending, keys string
	putHook              func(key string, v []byte)
	deleteHook           func(key string)
	logger               logging.StandardLogger
}

type element struct {
	key      string
	dot      dot
	priority uint64
	value    []byte
	// at and ttl are the stamps of the delta that wrote the element.
	at, ttl uint64
}

func (e element) expires() uint64 { return e.at + e.ttl }

// A removal is a pending removal: the tombstone of (key, dot), written at at
// and needed until at + ttl.
type removal struct {
	key     string
	dot     dot
	at, ttl uint64
}

func (r removal) expires() uint64 { return r.at + r.ttl }

// newSet is the set under namespace in store.
func newSet(store ds.Datastore, namespace ds.Key) *set {
	return &set{
		store:     store,
		namespace: namespace,
		elems:     namespace.ChildString(elemsNs).String(),
		pending:   namespace.ChildString(pendingNs).String(),
		keys:      namespace.ChildString(keysNs).String(),
	}
}

func (s *set) elemsPrefix() ds.Key { return ds.RawKey(s.elems) }

func (s *set) keyElemsPrefix(key string) ds.Key {
	b := make([]byte, 0, len(s.elems)+1+keySegmentLen(key))
	return dsKey(appendKeySegment(append(append(b, s.elems...), '/'), key))
}

func (s *set) elemKey(key string, d dot) ds.Key {
	return dsKey(s.appendElemKey(make([]byte, 0, len(s.elems)+2+keySegmentLen(key)+dotSegmentLen(d)), key, d))
}

func (s *set) appendElemKey(b []byte, key string, d dot) []byte {
	b = appendKeySegment(append(append(b, s.elems...), '/'), key)
	return appendDotSegment(append(b, '/'), d)
}

func (s *set) pendingPrefix() ds.Key { return ds.RawKey(s.pending) }

func (s *set) pendingKey(key string, d dot) ds.Key {
	return dsKey(s.appendPendingKey(make([]byte, 0, len(s.pending)+2+dotSegmentLen(d)+keySegmentLen(key)), key, d))
}

func (s *set) appendPendingKey(b []byte, key string, d dot) []byte {
	b = appendDotSegment(append(append(b, s.pending...), '/'), d)
	return appendKeySegment(append(b, '/'), key)
}

func (s *set) valueKey(key string) ds.Key { return dsKey(s.appendValueKey(nil, key)) }

func (s *set) priorityKey(key string) ds.Key { return dsKey(s.appendPriorityKey(nil, key)) }

func (s *set) appendValueKey(b []byte, key string) []byte {
	return append(append(append(append(b, s.keys...), ds.NewKey(key).String()...), '/'), valueSuffix...)
}

func (s *set) appendPriorityKey(b []byte, key string) []byte {
	return append(append(append(append(b, s.keys...), ds.NewKey(key).String()...), '/'), prioritySuffix...)
}

func encodeMarker(e element) []byte { return appendMarker(nil, e) }

func appendMarker(b []byte, e element) []byte {
	b = binary.AppendUvarint(b, e.priority)
	b = binary.AppendUvarint(b, e.at)
	b = binary.AppendUvarint(b, e.ttl)
	return append(b, e.value...)
}

// uvarints reads n uvarints off the front of b and returns them with the rest.
func uvarints(b []byte, n int) ([]uint64, []byte, error) {
	out := make([]uint64, n)
	rest, err := uvarintsInto(out, b)
	if err != nil {
		return nil, nil, err
	}
	return out, rest, nil
}

// uvarintsInto reads len(out) uvarints from b into out, and returns what
// follows them.
func uvarintsInto(out []uint64, b []byte) ([]byte, error) {
	for i := range out {
		v, k := binary.Uvarint(b)
		if k <= 0 {
			return nil, errors.New("error decoding varint")
		}
		out[i], b = v, b[k:]
	}
	return b, nil
}

func decodeMarker(b []byte) (element, error) {
	var v [3]uint64
	rest, err := uvarintsInto(v[:], b)
	if err != nil {
		return element{}, errors.New("error decoding element")
	}
	return element{priority: v[0], at: v[1], ttl: v[2], value: rest}, nil
}

func encodeStamps(at, ttl uint64) []byte { return appendStamps(nil, at, ttl) }

func appendStamps(b []byte, at, ttl uint64) []byte {
	return binary.AppendUvarint(binary.AppendUvarint(b, at), ttl)
}

func decodeStamps(b []byte) (uint64, uint64, error) {
	var v [2]uint64
	_, err := uvarintsInto(v[:], b)
	if err != nil {
		return 0, 0, errors.New("error decoding stamps")
	}
	return v[0], v[1], nil
}

func encodePriority(prio uint64) []byte { return appendPriority(nil, prio) }

func appendPriority(b []byte, prio uint64) []byte { return binary.AppendUvarint(b, prio+1) }

func decodePriority(data []byte) (uint64, error) {
	prio, n := binary.Uvarint(data)
	if n <= 0 {
		return 0, errors.New("error decoding priority")
	}
	return prio - 1, nil
}

// parseElem turns a full /e/<key>/<dot> entry back into an element.
func (s *set) parseElem(k string, v []byte) (element, error) {
	rel := strings.TrimPrefix(k, s.elemsPrefix().String()+"/")
	kseg, dseg, ok := strings.Cut(rel, "/")
	if !ok {
		return element{}, errors.New("bad element key " + k)
	}
	key, err := parseKeySegment(kseg)
	if err != nil {
		return element{}, err
	}
	d, err := parseDotSegment(dseg)
	if err != nil {
		return element{}, err
	}
	el, err := decodeMarker(v)
	if err != nil {
		return element{}, err
	}
	el.key, el.dot = key, d
	return el, nil
}

// parsePending turns a full /t/<dot>/<key> entry back into a removal.
func (s *set) parsePending(k string, v []byte) (removal, error) {
	rel := strings.TrimPrefix(k, s.pendingPrefix().String()+"/")
	dseg, kseg, ok := strings.Cut(rel, "/")
	if !ok {
		return removal{}, errors.New("bad pending key " + k)
	}
	key, err := parseKeySegment(kseg)
	if err != nil {
		return removal{}, err
	}
	d, err := parseDotSegment(dseg)
	if err != nil {
		return removal{}, err
	}
	at, ttl, err := decodeStamps(v)
	if err != nil {
		return removal{}, err
	}
	return removal{key: key, dot: d, at: at, ttl: ttl}, nil
}

// liveElems is every live element of key, until the next liveElems on t.
func (s *set) liveElems(ctx context.Context, t *txn, key string) ([]element, error) {
	t.prefix = appendKeySegment(append(append(t.prefix[:0], s.elems...), '/'), key)
	entries, err := t.scan(ctx, dsKey(t.prefix))
	if err != nil {
		return nil, err
	}
	t.elems = t.elems[:0]
	for _, e := range entries {
		d, err := parseDotSegment(e.key[len(t.prefix)+1:])
		if err != nil {
			return nil, err
		}
		el, err := decodeMarker(e.value)
		if err != nil {
			return nil, err
		}
		el.key, el.dot = key, d
		t.elems = append(t.elems, el)
	}
	return t.elems, nil
}

// change is what a merge did to one key's value, for the hooks.
type change struct {
	put     bool
	value   []byte
	deleted bool
}

// settle recomputes each touched key's winner and reports what changed: put
// when the key's value appeared or changed, deleted when it went from present
// to absent. A new winner holding the same bytes is not a change, though its
// priority is stored.
func (s *set) settle(ctx context.Context, t *txn, touched map[string]change) error {
	for key := range touched {
		live, err := s.liveElems(ctx, t, key)
		if err != nil {
			return err
		}
		var best *element
		for i := range live {
			e := &live[i]
			if best == nil || e.priority > best.priority ||
				(e.priority == best.priority && bytes.Compare(e.value, best.value) > 0) {
				best = e
			}
		}
		cur, err := t.get(ctx, t.key(s.appendValueKey(t.buf(), key)))
		had := err == nil
		if err != nil && !errors.Is(err, ds.ErrNotFound) {
			return err
		}
		if best == nil {
			if had {
				t.del(t.key(s.appendValueKey(t.buf(), key)))
				t.del(t.key(s.appendPriorityKey(t.buf(), key)))
				touched[key] = change{deleted: true}
			}
			continue
		}
		curPrio := uint64(0)
		if had {
			p, err := t.get(ctx, t.key(s.appendPriorityKey(t.buf(), key)))
			if err != nil && !errors.Is(err, ds.ErrNotFound) {
				return err
			}
			if err == nil {
				if curPrio, err = decodePriority(p); err != nil {
					return err
				}
			}
		}
		if had && bytes.Equal(cur, best.value) {
			if curPrio != best.priority {
				t.put(t.key(s.appendPriorityKey(t.buf(), key)), t.keep(appendPriority(t.buf(), best.priority)))
			}
			continue
		}
		t.put(t.key(s.appendValueKey(t.buf(), key)), best.value)
		t.put(t.key(s.appendPriorityKey(t.buf(), key)), t.keep(appendPriority(t.buf(), best.priority)))
		touched[key] = change{put: true, value: best.value}
	}
	return nil
}

func (s *set) fireHooks(touched map[string]change) {
	for key, ch := range touched {
		if ch.deleted {
			s.deleteHook(key)
		}
		if ch.put {
			s.putHook(key, ch.value)
		}
	}
}

// Element retrieves the value of an element from the CRDT set.
func (s *set) Element(ctx context.Context, key string) ([]byte, error) {
	return s.store.Get(ctx, s.valueKey(key))
}

// InSet returns true if the key has a live element.
func (s *set) InSet(ctx context.Context, key string) (bool, error) {
	return s.store.Has(ctx, s.valueKey(key))
}

// Elements returns every key with a live element under the query's prefix.
func (s *set) Elements(ctx context.Context, q query.Query) (query.Results, error) {
	keyNamespacePrefix := s.namespace.ChildString(keysNs)
	keyNamespacePrefixStr := keyNamespacePrefix.String()
	setQueryPrefix := keyNamespacePrefix.Child(ds.NewKey(q.Prefix)).String()
	vSuffix := "/" + valueSuffix

	setQuery := query.Query{
		Prefix:   setQueryPrefix,
		KeysOnly: q.KeysOnly,
	}

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

	return query.ResultsWithContext(q, func(qctx context.Context, out chan<- query.Result) {
		results, err := s.store.Query(ctx, setQuery)
		if err != nil {
			sendResult(ctx, qctx, query.Result{Error: err}, out)
			return
		}
		defer results.Close()

		for r := range results.Next() {
			if r.Error != nil {
				sendResult(ctx, qctx, query.Result{Error: r.Error}, out)
				return
			}
			if !strings.HasSuffix(r.Key, vSuffix) {
				continue
			}
			entry := query.Entry{
				Key:        strings.TrimSuffix(strings.TrimPrefix(r.Key, keyNamespacePrefixStr), vSuffix),
				Value:      r.Value,
				Size:       r.Size,
				Expiration: r.Expiration,
			}
			if q.KeysOnly {
				entry.Size = -1
				entry.Value = nil
			}
			if !sendResult(ctx, qctx, query.Result{Entry: entry}, out) {
				return
			}
		}
	}), nil
}
