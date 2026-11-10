package kv

import (
	"context"
	"strings"
	"testing"

	"github.com/taubyte/tau/core/kvdb"
)

// mapKV is an in-memory database that counts the calls a remote database would
// turn into network round trips.
type mapKV struct {
	kvdb.KVDB
	data  map[string][]byte
	calls int
}

func (m *mapKV) Get(_ context.Context, key string) ([]byte, error) {
	m.calls++
	return m.data[key], nil
}

func (m *mapKV) List(_ context.Context, prefix string) (keys []string, _ error) {
	m.calls++
	for k := range m.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (m *mapKV) Batch(context.Context) (kvdb.Batch, error) { return &mapBatch{m: m}, nil }

type mapBatch struct {
	m   *mapKV
	ops map[string][]byte
}

func (b *mapBatch) Put(k string, v []byte) error {
	if b.ops == nil {
		b.ops = map[string][]byte{}
	}
	b.ops[k] = v
	return nil
}
func (b *mapBatch) Delete(k string) error { return nil }
func (b *mapBatch) Commit() error {
	b.m.calls++
	for k, v := range b.ops {
		b.m.data[k] = v
	}
	return nil
}

// quotaKV is a hoarder-backed database: the hoarder checks the quota.
type quotaKV struct{ *mapKV }

func (q quotaKV) BatchWithMax(ctx context.Context, _ uint64) (kvdb.Batch, error) {
	return q.Batch(ctx)
}

// A write costs the same network calls whatever the database holds: it used to
// read every size record first.
func TestPutDoesNotScanSizes(t *testing.T) {
	db := &mapKV{data: map[string][]byte{}}
	for i := range 1000 {
		db.data["size/k"+strings.Repeat("x", i)] = []byte("1")
	}
	k := &kv{name: "test", database: quotaKV{db}, maxSize: 1 << 20}
	if err := k.Put(t.Context(), "/k", []byte("value")); err != nil {
		t.Fatal(err)
	}
	if db.calls > 1 {
		t.Fatalf("one Put made %d remote calls, want at most 1", db.calls)
	}
}

// A user key under size/ that is not a number used to make every later write
// to the database fail.
func TestNonNumberUnderSizeDoesNotBreakWrites(t *testing.T) {
	db := &mapKV{data: map[string][]byte{"size/junk": []byte("not a number"), "size/a": []byte("5")}}
	k := &kv{name: "test", database: db, maxSize: 100}
	if err := k.Put(t.Context(), "/b", []byte("12345")); err != nil {
		t.Fatal(err)
	}
	if left, err := k.Size(t.Context()); err != nil || left != 90 {
		t.Fatalf("Size = %d, %v; want 90", left, err)
	}
}
