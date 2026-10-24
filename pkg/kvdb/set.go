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
//	/e/<key segment>/<dot segment>  live element: varint priority, then value
//	/t/<dot segment>/<key segment>  pending removal: the element is named by a
//	                                tombstone but its dot is not yet covered
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
	store      ds.Datastore
	namespace  ds.Key
	putHook    func(key string, v []byte)
	deleteHook func(key string)
	logger     logging.StandardLogger
}

type element struct {
	key      string
	dot      dot
	priority uint64
	value    []byte
}

func (s *set) elemsPrefix() ds.Key { return s.namespace.ChildString(elemsNs) }

func (s *set) keyElemsPrefix(key string) ds.Key {
	return s.elemsPrefix().ChildString(keySegment(key))
}

func (s *set) elemKey(key string, d dot) ds.Key {
	return s.keyElemsPrefix(key).ChildString(d.segment())
}

func (s *set) pendingPrefix() ds.Key { return s.namespace.ChildString(pendingNs) }

func (s *set) pendingKey(key string, d dot) ds.Key {
	return s.pendingPrefix().ChildString(d.segment()).ChildString(keySegment(key))
}

func (s *set) valueKey(key string) ds.Key {
	return s.namespace.ChildString(keysNs).Child(ds.NewKey(key)).ChildString(valueSuffix)
}

func (s *set) priorityKey(key string) ds.Key {
	return s.namespace.ChildString(keysNs).Child(ds.NewKey(key)).ChildString(prioritySuffix)
}

func encodeMarker(prio uint64, value []byte) []byte {
	return append(binary.AppendUvarint(nil, prio), value...)
}

func decodeMarker(b []byte) (uint64, []byte, error) {
	p, n := binary.Uvarint(b)
	if n <= 0 {
		return 0, nil, errors.New("error decoding element")
	}
	return p, b[n:], nil
}

func encodePriority(prio uint64) []byte { return binary.AppendUvarint(nil, prio+1) }

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
	prio, value, err := decodeMarker(v)
	if err != nil {
		return element{}, err
	}
	return element{key: key, dot: d, priority: prio, value: value}, nil
}

func (s *set) parsePending(k string) (string, dot, error) {
	rel := strings.TrimPrefix(k, s.pendingPrefix().String()+"/")
	dseg, kseg, ok := strings.Cut(rel, "/")
	if !ok {
		return "", dot{}, errors.New("bad pending key " + k)
	}
	key, err := parseKeySegment(kseg)
	if err != nil {
		return "", dot{}, err
	}
	d, err := parseDotSegment(dseg)
	return key, d, err
}

// liveElems is every live element of key.
func (s *set) liveElems(ctx context.Context, t *txn, key string) ([]element, error) {
	entries, err := t.scan(ctx, s.keyElemsPrefix(key))
	if err != nil {
		return nil, err
	}
	out := make([]element, 0, len(entries))
	for _, e := range entries {
		el, err := s.parseElem(e.key, e.value)
		if err != nil {
			return nil, err
		}
		out = append(out, el)
	}
	return out, nil
}

// change is what a merge did to one key, for the hooks.
type change struct {
	removed bool
	put     bool
	value   []byte
	deleted bool
}

// settle recomputes each touched key's winner and reports what changed.
func (s *set) settle(ctx context.Context, t *txn, touched map[string]*change) error {
	for key, ch := range touched {
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
		cur, err := t.get(ctx, s.valueKey(key))
		had := err == nil
		if err != nil && !errors.Is(err, ds.ErrNotFound) {
			return err
		}
		if best == nil {
			if had {
				t.del(s.valueKey(key))
				t.del(s.priorityKey(key))
				ch.deleted = true
			}
			continue
		}
		curPrio := uint64(0)
		if had {
			p, err := t.get(ctx, s.priorityKey(key))
			if err != nil && !errors.Is(err, ds.ErrNotFound) {
				return err
			}
			if err == nil {
				if curPrio, err = decodePriority(p); err != nil {
					return err
				}
			}
		}
		if had && curPrio == best.priority && bytes.Equal(cur, best.value) {
			continue
		}
		t.put(s.valueKey(key), best.value)
		t.put(s.priorityKey(key), encodePriority(best.priority))
		ch.put = true
		ch.value = best.value
	}
	return nil
}

func (s *set) fireHooks(touched map[string]*change) {
	for key, ch := range touched {
		if ch.removed || ch.deleted {
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
