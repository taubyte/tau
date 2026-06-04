package bindings

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// Registry maps unguessable tokens to per-website Scopes. A website registers
// its scope (getting a token) at provision and releases it on teardown.
type Registry struct {
	mu     sync.RWMutex
	scopes map[string]func() *Scope
}

func NewRegistry() *Registry {
	return &Registry{scopes: map[string]func() *Scope{}}
}

// Add registers a scope provider (called per request so the website can resolve
// KV/storage lazily) and returns the token to embed in the binding URL.
func (r *Registry) Add(provider func() *Scope) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	r.scopes[token] = provider
	r.mu.Unlock()
	return token, nil
}

// Remove drops a token (idempotent).
func (r *Registry) Remove(token string) {
	r.mu.Lock()
	delete(r.scopes, token)
	r.mu.Unlock()
}

// Resolver returns the lookup used by Handler.
func (r *Registry) Resolver() Resolver {
	return func(token string) (*Scope, bool) {
		r.mu.RLock()
		provider, ok := r.scopes[token]
		r.mu.RUnlock()
		if !ok {
			return nil, false
		}
		return provider(), true
	}
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Server is the loopback HTTP endpoint serving the bindings. It listens on
// 127.0.0.1 only, so only local wasmtime subprocesses can reach it; combined
// with random per-website tokens this scopes each component to its own data.
type Server struct {
	reg  *Registry
	ln   net.Listener
	http *http.Server
	base string
}

// NewServer starts the binding server on a free loopback port.
func NewServer() (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("binding server listen failed: %w", err)
	}
	reg := NewRegistry()
	s := &Server{
		reg:  reg,
		ln:   ln,
		base: "http://" + ln.Addr().String(),
	}
	s.http = &http.Server{
		Handler:           Handler(reg.Resolver()),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go s.http.Serve(ln)
	return s, nil
}

// Registry exposes the token registry so a provider can Add/Remove scopes.
func (s *Server) Registry() *Registry { return s.reg }

// URLFor returns the base binding URL for a token, to inject as x-taubyte-bindings.
func (s *Server) URLFor(token string) string { return s.base + "/" + token }

// Close stops the server.
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return s.http.Shutdown(ctx)
}
