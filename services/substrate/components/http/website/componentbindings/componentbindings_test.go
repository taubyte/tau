package componentbindings

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/ipfs/go-cid"
	datastore "github.com/ipfs/go-datastore"
	dbIface "github.com/taubyte/tau/core/services/substrate/components/database"
	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

// --- minimal fakes (embed the big interfaces, override only what's used) ---

type fakeKV struct{ m map[string][]byte }

func (k *fakeKV) Get(_ context.Context, key string) ([]byte, error) {
	if v, ok := k.m[key]; ok {
		return v, nil
	}
	return nil, datastore.ErrNotFound
}
func (k *fakeKV) Put(_ context.Context, key string, v []byte) error { k.m[key] = v; return nil }
func (k *fakeKV) Delete(_ context.Context, key string) error        { delete(k.m, key); return nil }
func (k *fakeKV) List(_ context.Context, prefix string) ([]string, error) {
	var out []string
	for key := range k.m {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			out = append(out, key)
		}
	}
	return out, nil
}
func (k *fakeKV) Close()                               {}
func (k *fakeKV) UpdateSize(uint64)                    {}
func (k *fakeKV) Size(context.Context) (uint64, error) { return 0, nil }

type fakeDB struct {
	dbIface.Database
	kv dbIface.KV
}

func (d *fakeDB) KV() dbIface.KV { return d.kv }

type fakeDBService struct {
	dbIface.Service
	db dbIface.Database
}

func (s fakeDBService) Database(dbIface.Context) (dbIface.Database, error) { return s.db, nil }

func TestKVBinding(t *testing.T) {
	kv := &fakeKV{m: map[string][]byte{}}
	svc := fakeDBService{db: &fakeDB{kv: kv}}
	b := NewKV(svc, context.Background(), dbIface.Context{ProjectId: "p", Matcher: "m"})
	ctx := context.Background()

	// miss -> found=false, no error
	if _, found, err := b.Get(ctx, "k"); err != nil || found {
		t.Fatalf("miss = (found %v, err %v), want (false, nil)", found, err)
	}
	if err := b.Put(ctx, "k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if v, found, err := b.Get(ctx, "k"); err != nil || !found || string(v) != "v" {
		t.Fatalf("get = (%q, %v, %v), want (v, true, nil)", v, found, err)
	}
	b.Put(ctx, "ka", []byte("1"))
	if keys, err := b.List(ctx, "k"); err != nil || len(keys) != 2 {
		t.Fatalf("list = (%v, %v), want 2 keys", keys, err)
	}
	if err := b.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := b.Get(ctx, "k"); found {
		t.Fatal("key present after delete")
	}
}

// --- storage fakes ---

type nopRSC struct{ io.ReadSeeker }

func (nopRSC) Close() error { return nil }

type fakeMeta struct{ data []byte }

func (m *fakeMeta) Get() (io.ReadSeekCloser, error) { return nopRSC{bytes.NewReader(m.data)}, nil }
func (m *fakeMeta) Cid() cid.Cid                    { return cid.Cid{} }
func (m *fakeMeta) Version() int                    { return 1 }

type fakeStorage struct {
	storageIface.Storage
	files map[string][]byte
}

func (s *fakeStorage) GetLatestVersion(_ context.Context, name string) (int, error) {
	if _, ok := s.files[name]; !ok {
		return 0, datastore.ErrNotFound
	}
	return 1, nil
}
func (s *fakeStorage) Meta(_ context.Context, name string, _ int) (storageIface.Meta, error) {
	return &fakeMeta{data: s.files[name]}, nil
}
func (s *fakeStorage) AddFile(_ context.Context, r io.ReadSeeker, name string, _ bool) (int, error) {
	data, _ := io.ReadAll(r)
	s.files[name] = data
	return 1, nil
}

type fakeStorageService struct {
	storageIface.Service
	st storageIface.Storage
}

func (s fakeStorageService) Storage(storageIface.Context) (storageIface.Storage, error) {
	return s.st, nil
}

func TestStorageBinding(t *testing.T) {
	st := &fakeStorage{files: map[string][]byte{}}
	svc := fakeStorageService{st: st}
	b := NewStorage(svc, storageIface.Context{Context: context.Background(), ProjectId: "p", Matcher: "m"})
	ctx := context.Background()

	if _, _, found, err := b.Get(ctx, "a.txt"); err != nil || found {
		t.Fatalf("miss = (found %v, err %v), want (false, nil)", found, err)
	}
	if err := b.Put(ctx, "a.txt", []byte("data")); err != nil {
		t.Fatal(err)
	}
	if data, _, found, err := b.Get(ctx, "a.txt"); err != nil || !found || string(data) != "data" {
		t.Fatalf("get = (%q, %v, %v), want (data, true, nil)", data, found, err)
	}
}

func TestIsNotFound(t *testing.T) {
	if !isNotFound(datastore.ErrNotFound) {
		t.Error("datastore.ErrNotFound not recognized")
	}
	if isNotFound(io.EOF) {
		t.Error("io.EOF wrongly treated as not-found")
	}
}

func TestResolveSecrets(t *testing.T) {
	env := map[string]string{"MYAPP_TOKEN": "s3cr3t", "EMPTY": ""}
	getenv := func(k string) string { return env[k] }
	got := resolveSecrets([]structureSpec.Binding{
		{Name: "API_KEY", Type: structureSpec.BindingSecret, Resource: "MYAPP_TOKEN"},
		{Name: "MISSING", Type: structureSpec.BindingSecret, Resource: "NOT_SET"},
		{Name: "BLANK", Type: structureSpec.BindingSecret, Resource: "EMPTY"},
	}, getenv)
	if got["API_KEY"] != "s3cr3t" {
		t.Errorf("API_KEY = %q, want s3cr3t", got["API_KEY"])
	}
	if _, ok := got["MISSING"]; ok {
		t.Error("unset env var should not produce a secret")
	}
	if _, ok := got["BLANK"]; ok {
		t.Error("empty env var should not produce a secret")
	}
	if resolveSecrets(nil, getenv) != nil {
		t.Error("no secret bindings should resolve to nil")
	}
}

func TestEffectiveBindingsDefault(t *testing.T) {
	// No declared bindings -> default KV + STORAGE matched by website name.
	w := structureSpec.Website{Name: "mysite"}
	eff := w.EffectiveBindings()
	if len(eff) != 2 {
		t.Fatalf("default bindings = %d, want 2", len(eff))
	}
	kv := w.BindingsOfType(structureSpec.BindingKV)
	if len(kv) != 1 || kv[0].Name != "KV" || kv[0].Resource != "mysite" {
		t.Errorf("default kv binding = %+v", kv)
	}
	// Declared bindings override the default.
	w2 := structureSpec.Website{Name: "mysite", Bindings: []structureSpec.Binding{
		{Name: "CACHE", Type: structureSpec.BindingKV, Resource: "/cache"},
	}}
	if got := w2.BindingsOfType(structureSpec.BindingKV); len(got) != 1 || got[0].Name != "CACHE" {
		t.Errorf("declared kv binding = %+v", got)
	}
	if got := w2.BindingsOfType(structureSpec.BindingStorage); len(got) != 0 {
		t.Errorf("no storage declared, got %+v", got)
	}
}
