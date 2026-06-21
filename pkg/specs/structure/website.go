package structureSpec

import (
	"github.com/taubyte/tau/pkg/specs/common"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

type Website struct {
	Id          string
	Name        string
	Description string
	Tags        []string
	Domains     []string
	Paths       []string
	Branch      string
	Provider    string
	RepoID      string `mapstructure:"repository-id"`
	RepoName    string `mapstructure:"repository-name"`

	// Render selects how the website is served: "" / "static" (the historical
	// behaviour, a bundle of static files) or "ssr" (dynamic server side
	// rendering plus /api handlers backed by a WebAssembly server bundle).
	// When unset the runtime falls back to the SSR manifest embedded in the
	// build asset, so this stays fully backwards compatible.
	Render    string `mapstructure:"render"`
	Framework string `mapstructure:"framework"`

	// Entry is the WebAssembly export invoked for SSR/API requests. SSRMemory
	// (bytes) and SSRTimeout (nanoseconds) bound the server bundle VM. All
	// three are optional; sensible defaults are applied at serve time.
	Entry      string `mapstructure:"entry"`
	SSRMemory  uint64 `mapstructure:"ssr-memory"`
	SSRTimeout uint64 `mapstructure:"ssr-timeout"`

	// Bindings declare the resources surfaced on an SSR component's `env`
	// (Workers-style named bindings): `env.<Name>`. Each maps a name to a kv /
	// storage resource (by matcher) or a secret value. Optional.
	Bindings []Binding `mapstructure:"bindings"`

	// noset, this is parsed from the tags
	SmartOps []string

	Basic
	Wasm
}

// Binding types for Website.Bindings.
const (
	BindingKV      = "kv"
	BindingStorage = "storage"
	BindingSecret  = "secret"
)

// Binding maps a component `env.<Name>` entry to a backing resource: a kv or
// storage resource selected by Resource (its matcher), or a secret whose value
// is Resource.
type Binding struct {
	Name     string `mapstructure:"name"`
	Type     string `mapstructure:"type"`
	Resource string `mapstructure:"resource"`
}

// BindingsOfType returns the effective bindings of the given type.
func (w Website) BindingsOfType(t string) []Binding {
	var out []Binding
	for _, b := range w.EffectiveBindings() {
		if b.Type == t {
			out = append(out, b)
		}
	}
	return out
}

// EffectiveBindings returns the declared bindings, or — when none are declared —
// a backwards-compatible default: env.KV and env.STORAGE mapped to resources
// matched by the website name. (Secrets have no default.)
func (w Website) EffectiveBindings() []Binding {
	if len(w.Bindings) > 0 {
		return w.Bindings
	}
	return []Binding{
		{Name: "KV", Type: BindingKV, Resource: w.Name},
		{Name: "STORAGE", Type: BindingStorage, Resource: w.Name},
	}
}

func (w Website) GetName() string {
	return w.Name
}

// IsSSR reports whether the website is explicitly configured for server side
// rendering. A website may still be served as SSR without this set when its
// build asset carries an SSR manifest.
func (w Website) IsSSR() bool {
	return w.Render == websiteSpec.RenderSSR
}

func (w *Website) SetId(id string) {
	w.Id = id
}

func (w *Website) BasicPath(branch, commit, projectId, appId string) (*common.TnsPath, error) {
	return websiteSpec.Tns().BasicPath(branch, commit, projectId, appId, w.Id)
}

func (w *Website) IndexValue(branch, projectId, appId string) (*common.TnsPath, error) {
	return websiteSpec.Tns().IndexValue(branch, projectId, appId, w.Id)
}

func (w *Website) HttpPath(fqdn string) (*common.TnsPath, error) {
	return websiteSpec.Tns().HttpPath(fqdn)
}

func (w *Website) WasmModulePath(projectId, appId string) (*common.TnsPath, error) {
	return websiteSpec.Tns().WasmModulePath(projectId, appId, w.Name)
}

func (w *Website) GetId() string {
	return w.Id
}
