package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	logging "github.com/ipfs/go-log/v2"
	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/go-sdk/utils/codec"
	"github.com/taubyte/tau/core/kvdb"
	dbIface "github.com/taubyte/tau/core/services/substrate/components/database"
	kvdbMock "github.com/taubyte/tau/pkg/kvdb/mock"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
	dbkv "github.com/taubyte/tau/services/substrate/components/database/kv"
)

// Guest memory layout of these tests.
const (
	outPtr  = 0       // a size, an id or a count
	aPtr    = 1 << 10 // first string argument
	bPtr    = 2 << 10 // second string argument
	bufPtr  = 4 << 10 // value to put in a batch
	readPtr = 8 << 10 // data step output
	putPtr  = 2 << 20 // values put with databasePut
)

// env is a database opened by a function, backed by the size-tracking KV layer
// over the in-memory kvdb, as the vm-low-orbit tests use.
type env struct {
	t     *testing.T
	f     *Factory
	mod   *memModule
	id    uint32
	store kvdb.KVDB
	ctx   context.Context
	used  uint32
}

func newEnv(t *testing.T, wrap func(kvdb.KVDB) kvdb.KVDB) *env {
	store, err := kvdbMock.New().New(logging.Logger("crud-test"), "crud", 0)
	if err != nil {
		t.Fatal(err)
	}
	if wrap != nil {
		store = wrap(store)
	}
	f := &Factory{Methods: helpers.New(context.Background()), database: map[uint32]*Database{}}
	id := f.createDatabasePointer(kvDB{kv: dbkv.New(1<<30, "crud", store)}, "/kv").Id
	return &env{t: t, f: f, mod: &memModule{mem: &memMemory{buf: make([]byte, 4<<20)}}, id: id, store: store, ctx: context.Background()}
}

type kvDB struct {
	dbIface.Database
	kv dbIface.KV
}

func (d kvDB) KV() dbIface.KV { return d.kv }
func (d kvDB) Close()         {}

// str writes s at ptr and returns the pointer and length to pass.
func (e *env) str(ptr uint32, s string) (uint32, uint32) {
	copy(e.mod.mem.buf[ptr:], s)
	return ptr, uint32(len(s))
}

func (e *env) u32(ptr uint32) uint32 { return binary.LittleEndian.Uint32(e.mod.mem.buf[ptr:]) }

func (e *env) ok(rc uint32) {
	e.t.Helper()
	if rc != 0 {
		e.t.Fatalf("errno %s", errno.Error(rc))
	}
}

func (e *env) want(want errno.Error, rc uint32) {
	e.t.Helper()
	if errno.Error(rc) != want {
		e.t.Fatalf("want %s, got %s", want, errno.Error(rc))
	}
}

// fetch runs a size step then a data step, as the SDK does.
func (e *env) fetch(size, data func(ptr uint32) uint32) []byte {
	e.t.Helper()
	e.ok(size(outPtr))
	n := e.u32(outPtr)
	e.ok(data(readPtr))
	return append([]byte{}, e.mod.mem.buf[readPtr:readPtr+n]...)
}

// put writes each value at a new place: the in-memory kvdb keeps the slice it
// is given, which here is a view of guest memory.
func (e *env) put(key, value string) {
	e.t.Helper()
	kp, kl := e.str(aPtr, key)
	ptr := putPtr + e.used
	e.used += uint32(len(value))
	copy(e.mod.mem.buf[ptr:], value)
	e.ok(e.f.databasePut(e.ctx, e.mod, e.id, kp, kl, ptr, uint32(len(value))))
}

func (e *env) get(key string) (string, errno.Error) {
	kp, kl := e.str(aPtr, key)
	if rc := e.f.databaseGetSize(e.ctx, e.mod, e.id, kp, kl, outPtr); rc != 0 {
		return "", errno.Error(rc)
	}
	n := e.u32(outPtr)
	if rc := e.f.databaseGet(e.ctx, e.mod, e.id, kp, kl, readPtr); rc != 0 {
		return "", errno.Error(rc)
	}
	return string(e.mod.mem.buf[readPtr : readPtr+n]), 0
}

func strs(t *testing.T, b []byte) []string {
	out := []string{}
	if err := codec.Convert(b).To(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// decodeEntries reads the databaseListEntries encoding; see appendEntry.
func decodeEntries(t *testing.T, b []byte) map[string]string {
	out := map[string]string{}
	for len(b) > 0 {
		i := bytes.IndexByte(b, 0)
		if i < 0 || len(b) < i+5 {
			t.Fatalf("truncated entry %q", b)
		}
		n := binary.LittleEndian.Uint32(b[i+1:])
		out[string(b[:i])] = string(b[i+5 : i+5+int(n)])
		b = b[i+5+int(n):]
	}
	return out
}

func (e *env) batchNew() uint32 {
	e.t.Helper()
	e.ok(e.f.databaseBatchNew(e.ctx, e.mod, e.id, outPtr))
	return e.u32(outPtr)
}

func (e *env) batchPut(b uint32, key, value string) uint32 {
	kp, kl := e.str(aPtr, key)
	copy(e.mod.mem.buf[bufPtr:], value)
	return e.f.databaseBatchPut(e.ctx, e.mod, b, kp, kl, bufPtr, uint32(len(value)))
}

func (e *env) batchDelete(b uint32, key string) uint32 {
	kp, kl := e.str(aPtr, key)
	return e.f.databaseBatchDelete(e.ctx, e.mod, b, kp, kl)
}

func TestBatchCommitsPutsAndDeletesTogether(t *testing.T) {
	e := newEnv(t, nil)
	e.put("/old", "x")

	b := e.batchNew()
	e.ok(e.batchPut(b, "/a", "1"))
	e.ok(e.batchPut(b, "/b", "2"))
	e.ok(e.batchDelete(b, "/old"))
	if _, err := e.get("/a"); err != errno.ErrorDatabaseKeyNotFound {
		t.Fatalf("written before commit: %s", err)
	}
	e.ok(e.f.databaseBatchCommit(e.ctx, e.mod, b))

	for k, v := range map[string]string{"/a": "1", "/b": "2"} {
		if got, err := e.get(k); err != 0 || got != v {
			t.Fatalf("%s = %q, %s", k, got, err)
		}
	}
	if _, err := e.get("/old"); err != errno.ErrorDatabaseKeyNotFound {
		t.Fatalf("/old not deleted: %s", err)
	}
	// The size records follow the batch, so the quota sees the batch.
	if used, err := e.store.Get(e.ctx, "size/a"); err != nil || string(used) != "1" {
		t.Fatalf("size/a = %q, %v", used, err)
	}
	if _, err := e.store.Get(e.ctx, "size/old"); err == nil {
		t.Fatal("size/old not deleted")
	}

	// A committed batch is freed.
	e.want(errno.ErrorDatabaseNotFound, e.f.databaseBatchCommit(e.ctx, e.mod, b))
	e.want(errno.ErrorDatabaseNotFound, e.batchPut(b, "/c", "3"))
}

// failingKVDB fails the batch write of one key, after the writes before it.
type failingKVDB struct {
	kvdb.KVDB
	failKey string
}

func (f failingKVDB) Batch(ctx context.Context) (kvdb.Batch, error) {
	b, err := f.KVDB.Batch(ctx)
	return failingBatch{b, f.failKey}, err
}

type failingBatch struct {
	kvdb.Batch
	failKey string
}

func (b failingBatch) Put(key string, v []byte) error {
	if key == b.failKey {
		return errors.New("disk on fire")
	}
	return b.Batch.Put(key, v)
}

func TestBatchWithAFailingOpWritesNothing(t *testing.T) {
	e := newEnv(t, func(s kvdb.KVDB) kvdb.KVDB { return failingKVDB{s, "/bad"} })

	b := e.batchNew()
	e.ok(e.batchPut(b, "/a", "1"))
	e.ok(e.batchPut(b, "/bad", "2"))
	e.ok(e.batchPut(b, "/c", "3"))
	e.want(errno.ErrorDatabasePutFailed, e.f.databaseBatchCommit(e.ctx, e.mod, b))

	keys, err := e.store.List(e.ctx, "")
	if err != nil || len(keys) != 0 {
		t.Fatalf("a failed batch wrote %v (%v)", keys, err)
	}
	e.want(errno.ErrorDatabaseNotFound, e.f.databaseBatchCommit(e.ctx, e.mod, b))
}

func TestBatchOverQuotaWritesNothing(t *testing.T) {
	e := newEnv(t, nil)
	e.f.database[e.id].KV().UpdateSize(4)

	b := e.batchNew()
	e.ok(e.batchPut(b, "/a", "12"))
	e.ok(e.batchPut(b, "/b", "345"))
	e.want(errno.ErrorDatabasePutFailed, e.f.databaseBatchCommit(e.ctx, e.mod, b))
	if keys, _ := e.store.List(e.ctx, ""); len(keys) != 0 {
		t.Fatalf("over quota wrote %v", keys)
	}
}

func TestBatchIsFreedWhenTheDatabaseCloses(t *testing.T) {
	e := newEnv(t, nil)
	b := e.batchNew()
	e.ok(e.batchPut(b, "/a", "1"))
	e.ok(e.f.databaseClose(e.ctx, e.mod, e.id))
	e.want(errno.ErrorDatabaseNotFound, e.batchPut(b, "/b", "2"))
	e.want(errno.ErrorDatabaseNotFound, e.f.databaseBatchCommit(e.ctx, e.mod, b))
	e.want(errno.ErrorDatabaseNotFound, e.f.databaseBatchNew(e.ctx, e.mod, e.id, outPtr))
}

func (e *env) entries(prefix string) map[string]string {
	e.t.Helper()
	pp, pl := e.str(bPtr, prefix)
	return decodeEntries(e.t, e.fetch(
		func(p uint32) uint32 { return e.f.databaseListEntriesSize(e.ctx, e.mod, e.id, pp, pl, p) },
		func(p uint32) uint32 { return e.f.databaseListEntries(e.ctx, e.mod, e.id, pp, pl, p) },
	))
}

func TestListEntries(t *testing.T) {
	e := newEnv(t, nil)
	e.put("/users/1", "ann")
	e.put("/users/2", "")
	e.put("/orders/1", "bob\x00with nul")

	if got := e.entries("/users/"); !reflect.DeepEqual(got, map[string]string{"/users/1": "ann", "/users/2": ""}) {
		t.Fatalf("prefix: %v", got)
	}
	if got := e.entries("/none/"); len(got) != 0 {
		t.Fatalf("no match: %v", got)
	}
	// An empty prefix lists every key, as databaseList does.
	all := e.entries("")
	for k, v := range map[string]string{"/users/1": "ann", "/users/2": "", "/orders/1": "bob\x00with nul"} {
		if all[k] != v {
			t.Fatalf("empty prefix: %s = %q in %v", k, all[k], all)
		}
	}

	big := strings.Repeat("v", 600<<10) // past the 64 KiB a uint16 length would hold
	e.put("/big", big)
	if got := e.entries("/big"); got["/big"] != big {
		t.Fatalf("large value came back as %d bytes", len(got["/big"]))
	}
}

func (e *env) match(prefix, pattern string) (uint32, []string) {
	pp, pl := e.str(aPtr, prefix)
	rp, rl := e.str(bPtr, pattern)
	if rc := e.f.databaseListMatchSize(e.ctx, e.mod, e.id, pp, pl, rp, rl, outPtr); rc != 0 {
		return rc, nil
	}
	n := e.u32(outPtr)
	if rc := e.f.databaseListMatch(e.ctx, e.mod, e.id, pp, pl, rp, rl, readPtr); rc != 0 {
		return rc, nil
	}
	return 0, strs(e.t, e.mod.mem.buf[readPtr:readPtr+n])
}

func TestListMatch(t *testing.T) {
	e := newEnv(t, nil)
	for _, k := range []string{"/users/1", "/users/22", "/users/x", "/orders/1"} {
		e.put(k, "v")
	}

	rc, keys := e.match("/users/", `^/users/[0-9]+$`)
	e.ok(rc)
	if !sameSet(keys, "/users/1", "/users/22") {
		t.Fatalf("match: %v", keys)
	}
	rc, keys = e.match("", `^/[a-z]+/1$`)
	e.ok(rc)
	if !sameSet(keys, "/users/1", "/orders/1") {
		t.Fatalf("empty prefix: %v", keys)
	}

	rc, _ = e.match("/users/", `([a-z`)
	e.want(errno.ErrorDatabaseListFailed, rc)
	rc, _ = e.match("/users/", strings.Repeat("a", maxPatternLen+1))
	e.want(errno.ErrorDatabaseListFailed, rc)
}

func sameSet(got []string, want ...string) bool {
	m := map[string]bool{}
	for _, k := range got {
		m[k] = true
	}
	if len(m) != len(want) || len(got) != len(want) {
		return false
	}
	for _, k := range want {
		if !m[k] {
			return false
		}
	}
	return true
}

func (e *env) page(prefix, cursor string, limit uint32) (uint32, []string) {
	pp, pl := e.str(aPtr, prefix)
	cp, cl := e.str(bPtr, cursor)
	if rc := e.f.databaseListPageSize(e.ctx, e.mod, e.id, pp, pl, cp, cl, limit, outPtr); rc != 0 {
		return rc, nil
	}
	n := e.u32(outPtr)
	if rc := e.f.databaseListPage(e.ctx, e.mod, e.id, pp, pl, cp, cl, limit, readPtr); rc != 0 {
		return rc, nil
	}
	return 0, strs(e.t, e.mod.mem.buf[readPtr:readPtr+n])
}

func TestListPageWalksEveryKeyOnce(t *testing.T) {
	e := newEnv(t, nil)
	for _, k := range []string{"/k/e", "/k/a", "/k/d", "/k/b", "/k/c", "/other"} {
		e.put(k, "v")
	}

	var all []string
	cursor := ""
	for pages := 0; ; pages++ {
		rc, keys := e.page("/k/", cursor, 2)
		e.ok(rc)
		if len(keys) == 0 {
			if pages != 3 {
				t.Fatalf("%d pages, want 3", pages)
			}
			break
		}
		all = append(all, keys...)
		cursor = keys[len(keys)-1]
	}
	if fmt.Sprint(all) != "[/k/a /k/b /k/c /k/d /k/e]" {
		t.Fatalf("pages gave %v", all)
	}

	// A cursor that is no longer a key still resumes after it.
	rc, keys := e.page("/k/", "/k/bb", 10)
	e.ok(rc)
	if fmt.Sprint(keys) != "[/k/c /k/d /k/e]" {
		t.Fatalf("after a deleted cursor: %v", keys)
	}
	rc, keys = e.page("/none/", "", 10)
	e.ok(rc)
	if len(keys) != 0 {
		t.Fatalf("empty prefix: %v", keys)
	}
	rc, _ = e.page("/k/", "", 0)
	e.want(errno.ErrorZeroSize, rc)
}

func TestCount(t *testing.T) {
	e := newEnv(t, nil)
	for _, k := range []string{"/k/a", "/k/b", "/other"} {
		e.put(k, "v")
	}
	for prefix, want := range map[string]uint32{"/k/": 2, "/": 3, "/none": 0} {
		pp, pl := e.str(aPtr, prefix)
		e.ok(e.f.databaseCount(e.ctx, e.mod, e.id, pp, pl, outPtr))
		if got := e.u32(outPtr); got != want {
			t.Fatalf("count %q = %d, want %d", prefix, got, want)
		}
	}
}

// A missing key is ErrorDatabaseKeyNotFound, not a failed read, so a function
// can tell "absent" from "broken". Both the local kvdb and the hoarder client
// report a missing key with their own "key not found" error.
func TestGetMissingKeyIsKeyNotFound(t *testing.T) {
	e := newEnv(t, nil)
	if _, err := e.get("/missing"); err != errno.ErrorDatabaseKeyNotFound {
		t.Fatalf("want ErrorDatabaseKeyNotFound, got %s", err)
	}
	kp, kl := e.str(aPtr, "/missing")
	e.want(errno.ErrorDatabaseKeyNotFound, e.f.databaseGet(e.ctx, e.mod, e.id, kp, kl, readPtr))
}

// The imports that existed before keep their behaviour.
func TestOldImportsUnchanged(t *testing.T) {
	e := newEnv(t, nil)
	e.put("/a", "1")
	e.put("/b", "2")
	if got, err := e.get("/a"); err != 0 || got != "1" {
		t.Fatalf("get: %q %s", got, err)
	}
	pp, pl := e.str(bPtr, "/")
	keys := strs(t, e.fetch(
		func(p uint32) uint32 { return e.f.databaseListSize(e.ctx, e.mod, e.id, pp, pl, p) },
		func(p uint32) uint32 { return e.f.databaseList(e.ctx, e.mod, e.id, pp, pl, p) },
	))
	if !sameSet(keys, "/a", "/b") {
		t.Fatalf("list: %v", keys)
	}
	kp, kl := e.str(aPtr, "/a")
	e.ok(e.f.databaseDelete(e.ctx, e.mod, e.id, kp, kl))
	if _, err := e.get("/a"); err != errno.ErrorDatabaseKeyNotFound {
		t.Fatalf("after delete: %s", err)
	}
	e.ok(e.f.databaseClose(e.ctx, e.mod, e.id))
	e.want(errno.ErrorDatabaseNotFound, e.f.databaseClose(e.ctx, e.mod, e.id))
}
