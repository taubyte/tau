// Package bindings implements the loopback HTTP endpoint that a StarlingMonkey
// component fetches for its `env.KV` / `env.STORAGE` bindings (see the component
// shim, tools/taubyte-ssr-adapter/shim/component.js). The substrate injects a
// per-website, unguessable-token URL into the request as `x-taubyte-bindings`;
// the component fetches `<url>/kv/<key>` etc., this server resolves the token to
// the website's scoped KV/storage, and serves it.
//
// The HTTP surface and token routing here are storage-agnostic: a substrate
// provider supplies a Scope (KV + Storage implementations bound to a website's
// project/application) per token. The server listens on loopback only and uses
// random tokens, so one component cannot reach another website's data.
package bindings

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// KV is a website-scoped key/value store backing env.KV.
type KV interface {
	Get(ctx context.Context, key string) (value []byte, found bool, err error)
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) (keys []string, err error)
}

// Storage is a website-scoped blob store backing env.STORAGE.
type Storage interface {
	Get(ctx context.Context, path string) (data []byte, contentType string, found bool, err error)
	Put(ctx context.Context, path string, data []byte) error
}

// Scope is the per-website backing resolved from a binding token: named KV and
// storage bindings (env.<Name>). A name absent from a map is an undeclared
// binding (404).
type Scope struct {
	KV      map[string]KV
	Storage map[string]Storage
}

// Resolver maps an opaque binding token to its Scope.
type Resolver func(token string) (*Scope, bool)

// maxBody bounds a KV value / stored blob accepted over the endpoint.
const maxBody = 8 << 20 // 8 MiB

// Handler routes `/{token}/kv/{name}/{key}`, `/{token}/kv/{name}?prefix=`, and
// `/{token}/storage/{name}/{path}` to the named binding in the token's Scope.
func Handler(resolve Resolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, rest, ok := splitToken(r.URL.Path)
		if !ok {
			http.Error(w, "bad binding path", http.StatusBadRequest)
			return
		}
		scope, ok := resolve(token)
		if !ok {
			http.Error(w, "unknown binding token", http.StatusForbidden)
			return
		}

		switch {
		case strings.HasPrefix(rest, "kv/"):
			name, key := splitNameKey(rest[len("kv/"):])
			kv, declared := scope.KV[name]
			if !declared {
				http.Error(w, "unknown kv binding `"+name+"`", http.StatusNotFound)
				return
			}
			serveKV(w, r, kv, key)
		case strings.HasPrefix(rest, "storage/"):
			name, path := splitNameKey(rest[len("storage/"):])
			st, declared := scope.Storage[name]
			if !declared {
				http.Error(w, "unknown storage binding `"+name+"`", http.StatusNotFound)
				return
			}
			serveStorage(w, r, st, path)
		default:
			http.NotFound(w, r)
		}
	})
}

// splitToken pulls the leading `/{token}/` segment off the path.
func splitToken(p string) (token, rest string, ok bool) {
	p = strings.TrimPrefix(p, "/")
	i := strings.IndexByte(p, '/')
	if i <= 0 {
		// `/{token}` alone (no resource) is not a valid binding request.
		return "", "", false
	}
	return p[:i], p[i+1:], true
}

// splitNameKey splits `name/rest...` into the binding name and the remaining
// key/path (empty when only a name is present, e.g. a kv list).
func splitNameKey(s string) (name, key string) {
	s = strings.TrimPrefix(s, "/")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func serveKV(w http.ResponseWriter, r *http.Request, kv KV, key string) {
	if kv == nil {
		http.Error(w, "kv binding not configured", http.StatusNotImplemented)
		return
	}
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		if key == "" { // list by prefix
			keys, err := kv.List(ctx, r.URL.Query().Get("prefix"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, keys)
			return
		}
		value, found, err := kv.Get(ctx, key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !found {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("content-type", "application/octet-stream")
		w.Write(value)
	case http.MethodPut:
		value, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := kv.Put(ctx, key, value); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := kv.Delete(ctx, key); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func serveStorage(w http.ResponseWriter, r *http.Request, st Storage, path string) {
	if st == nil {
		http.Error(w, "storage binding not configured", http.StatusNotImplemented)
		return
	}
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		data, ctype, found, err := st.Get(ctx, path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !found {
			http.NotFound(w, r)
			return
		}
		if ctype != "" {
			w.Header().Set("content-type", ctype)
		}
		w.Write(data)
	case http.MethodPut:
		data, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := st.Put(ctx, path, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
