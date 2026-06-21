package website

import (
	"encoding/json"
	goHttp "net/http"
	"sync"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/services/substrate/components/http/website/bindings"
)

// Component bindings wiring. A substrate that enables the component engine starts
// a loopback binding server (bindings.Server) and calls EnableComponentBindings
// with a resolver for a website's named KV/storage Scope and its secrets. This
// registers the injector (via RegisterComponentBindings) that adds the internal
// x-taubyte-bindings / x-taubyte-env headers the component shim consumes.
//
// The named bindings come from the website config (Website.EffectiveBindings):
// each kv/storage binding becomes env.<Name>; secrets are resolved by `secrets`.
var (
	bindingServer  *bindings.Server
	bindingScope   func(*Website) *bindings.Scope
	bindingSecrets func(*Website) map[string]string
	bindingTokens  sync.Map // website Id -> token (string)
)

// bindingConfig is the x-taubyte-bindings payload the shim parses.
type bindingConfig struct {
	Base    string   `json:"base"`
	KV      []string `json:"kv,omitempty"`
	Storage []string `json:"storage,omitempty"`
}

// EnableComponentBindings wires per-website KV/storage/secrets into components.
func EnableComponentBindings(server *bindings.Server, scope func(*Website) *bindings.Scope, secrets func(*Website) map[string]string) {
	bindingServer = server
	bindingScope = scope
	bindingSecrets = secrets
	RegisterComponentBindings(injectComponentBindings)
}

// injectComponentBindings sets the internal binding headers on r: secret bindings
// in x-taubyte-env (JSON name->value), and the per-website endpoint + the kv /
// storage binding names in x-taubyte-bindings (JSON). The shim reads and strips
// both.
func injectComponentBindings(w *Website, r *goHttp.Request) {
	if bindingSecrets != nil {
		if env := bindingSecrets(w); len(env) > 0 {
			if data, err := json.Marshal(env); err == nil {
				r.Header.Set("x-taubyte-env", string(data))
			}
		}
	}
	if bindingServer == nil || bindingScope == nil {
		return
	}
	token := websiteBindingToken(w)
	if token == "" {
		return
	}
	cfg := bindingConfig{Base: bindingServer.URLFor(token)}
	for _, b := range w.config.EffectiveBindings() {
		switch b.Type {
		case structureSpec.BindingKV:
			cfg.KV = append(cfg.KV, b.Name)
		case structureSpec.BindingStorage:
			cfg.Storage = append(cfg.Storage, b.Name)
		}
	}
	if data, err := json.Marshal(cfg); err == nil {
		r.Header.Set("x-taubyte-bindings", string(data))
	}
}

// websiteBindingToken returns the website's binding token, minting and caching
// one (registered against a lazy scope resolver) on first use.
func websiteBindingToken(w *Website) string {
	id := w.config.Id
	if t, ok := bindingTokens.Load(id); ok {
		return t.(string)
	}
	token, err := bindingServer.Registry().Add(func() *bindings.Scope { return bindingScope(w) })
	if err != nil {
		return ""
	}
	if actual, loaded := bindingTokens.LoadOrStore(id, token); loaded {
		bindingServer.Registry().Remove(token) // lost the race; release ours
		return actual.(string)
	}
	return token
}
