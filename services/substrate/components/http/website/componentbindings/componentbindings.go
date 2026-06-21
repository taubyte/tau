// Package componentbindings backs a StarlingMonkey component's env.KV /
// env.STORAGE with real Taubyte database (KV) and storage services, scoped to
// the website's project/application. It adapts the substrate's KV/storage
// interfaces to the loopback binding server (../bindings) and wires the whole
// thing onto the website component via website.EnableComponentBindings.
//
// The substrate node calls Enable once at startup with its database + storage
// services (and, optionally, a matcher policy and a secrets source). This
// package imports `website`; `website` does not import it, so there is no cycle.
package componentbindings

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"

	datastore "github.com/ipfs/go-datastore"
	dbIface "github.com/taubyte/tau/core/services/substrate/components/database"
	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/http/website"
	"github.com/taubyte/tau/services/substrate/components/http/website/bindings"
)

// kvBinding adapts a Taubyte database's KV (resolved per op — the service caches
// the instance) to bindings.KV.
type kvBinding struct {
	svc  dbIface.Service
	ctx  context.Context
	dctx dbIface.Context
}

// NewKV returns a bindings.KV backed by the database the service resolves for
// dctx (project/application/matcher).
func NewKV(svc dbIface.Service, base context.Context, dctx dbIface.Context) bindings.KV {
	return &kvBinding{svc: svc, ctx: base, dctx: dctx}
}

func (b *kvBinding) kv() (dbIface.KV, error) {
	d, err := b.svc.Database(b.dctx)
	if err != nil {
		return nil, err
	}
	return d.KV(), nil
}

func (b *kvBinding) Get(ctx context.Context, key string) ([]byte, bool, error) {
	kv, err := b.kv()
	if err != nil {
		return nil, false, err
	}
	v, err := kv.Get(ctx, key)
	if err != nil {
		if isNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return v, true, nil
}

func (b *kvBinding) Put(ctx context.Context, key string, value []byte) error {
	kv, err := b.kv()
	if err != nil {
		return err
	}
	return kv.Put(ctx, key, value)
}

func (b *kvBinding) Delete(ctx context.Context, key string) error {
	kv, err := b.kv()
	if err != nil {
		return err
	}
	return kv.Delete(ctx, key)
}

func (b *kvBinding) List(ctx context.Context, prefix string) ([]string, error) {
	kv, err := b.kv()
	if err != nil {
		return nil, err
	}
	return kv.List(ctx, prefix)
}

// storageBinding adapts Taubyte's versioned file storage to the flat
// get/put(path) shape of bindings.Storage (latest version only).
type storageBinding struct {
	svc  storageIface.Service
	sctx storageIface.Context
}

// NewStorage returns a bindings.Storage backed by the storage the service
// resolves for sctx (project/application/matcher).
func NewStorage(svc storageIface.Service, sctx storageIface.Context) bindings.Storage {
	return &storageBinding{svc: svc, sctx: sctx}
}

func (b *storageBinding) store() (storageIface.Storage, error) {
	return b.svc.Storage(b.sctx)
}

func (b *storageBinding) Get(ctx context.Context, path string) ([]byte, string, bool, error) {
	st, err := b.store()
	if err != nil {
		return nil, "", false, err
	}
	version, err := st.GetLatestVersion(ctx, path)
	if err != nil {
		if isNotFound(err) {
			return nil, "", false, nil
		}
		return nil, "", false, err
	}
	meta, err := st.Meta(ctx, path, version)
	if err != nil {
		if isNotFound(err) {
			return nil, "", false, nil
		}
		return nil, "", false, err
	}
	rc, err := meta.Get()
	if err != nil {
		return nil, "", false, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, "", false, err
	}
	return data, "", true, nil
}

func (b *storageBinding) Put(ctx context.Context, path string, data []byte) error {
	st, err := b.store()
	if err != nil {
		return err
	}
	_, err = st.AddFile(ctx, bytes.NewReader(data), path, true) // replace latest
	return err
}

func isNotFound(err error) bool {
	return errors.Is(err, datastore.ErrNotFound) ||
		strings.Contains(err.Error(), datastore.ErrNotFound.Error())
}

// Options configure Enable.
type Options struct {
	// Secrets resolves a website's secret bindings (env.<Name>). When nil, secret
	// bindings resolve from the node's environment: a binding's Resource names the
	// env var holding the value (so secrets stay out of the website config/git).
	Secrets func(*website.Website) map[string]string
}

// Enable starts the loopback binding server and wires each website's declared
// bindings (Website.EffectiveBindings) onto the component path: kv/storage
// bindings become env.<Name> backed by db/storage scoped to the website's
// project/application and the binding's Resource (matcher); secret bindings
// become env.<Name>. Call once at substrate startup when the component runtime
// is enabled; Close the returned server at shutdown.
func Enable(db dbIface.Service, storage storageIface.Service, opts Options) (*bindings.Server, error) {
	server, err := bindings.NewServer()
	if err != nil {
		return nil, err
	}

	scope := func(w *website.Website) *bindings.Scope {
		base := w.Service().Context()
		sc := &bindings.Scope{KV: map[string]bindings.KV{}, Storage: map[string]bindings.Storage{}}
		for _, b := range w.Config().EffectiveBindings() {
			switch b.Type {
			case structureSpec.BindingKV:
				if db != nil {
					sc.KV[b.Name] = NewKV(db, base, dbIface.Context{
						ProjectId: w.Project(), ApplicationId: w.Application(), Matcher: b.Resource,
					})
				}
			case structureSpec.BindingStorage:
				if storage != nil {
					sc.Storage[b.Name] = NewStorage(storage, storageIface.Context{
						Context: base, ProjectId: w.Project(), ApplicationId: w.Application(), Matcher: b.Resource,
					})
				}
			}
		}
		return sc
	}

	secrets := opts.Secrets
	if secrets == nil {
		secrets = secretsFromEnv
	}

	website.EnableComponentBindings(server, scope, secrets)
	return server, nil
}

// secretsFromEnv resolves a website's secret bindings from the node environment:
// each secret binding's Resource is the name of an env var holding the value.
func secretsFromEnv(w *website.Website) map[string]string {
	return resolveSecrets(w.Config().BindingsOfType(structureSpec.BindingSecret), os.Getenv)
}

// resolveSecrets maps secret bindings to env.<Name> values via getenv(Resource).
func resolveSecrets(secretBindings []structureSpec.Binding, getenv func(string) string) map[string]string {
	out := map[string]string{}
	for _, b := range secretBindings {
		if v := getenv(b.Resource); v != "" {
			out[b.Name] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
