// Package mock is auth for tests: its project table, kept in memory and answered the
// way auth answers it. It runs on a test host or a dream node, so a service reaches
// it through the same client it reaches auth through.
package mock

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/services/auth/projects"
)

// Auth keeps a project table.
type Auth struct {
	table    *projects.Table
	listener *rpc.Server
}

var (
	_ service.Service = (*Auth)(nil)
	_ projects.Keeper = (*Auth)(nil)
)

// New is auth with an empty project table.
func New() *Auth { return &Auth{table: projects.New(&memory{m: map[string][]byte{}}, time.Now)} }

// Projects is the table, as auth's own is reached beside it.
func (a *Auth) Projects() projects.Projects { return a.table }

// Start answers on auth's protocol, for services on other nodes.
func (a *Auth) Start(ctx context.Context, h service.Host) error {
	mux := http.NewServeMux()
	projects.Serve(mux, a.table)
	listener, err := rpc.Listen(ctx, h.Peer().Host(), projects.Protocol, mux)
	if err != nil {
		return err
	}
	a.listener = listener
	h.Serve(projects.Protocol, a)
	return nil
}

func (a *Auth) Close() error {
	if a.listener != nil {
		return a.listener.Close()
	}
	return nil
}

type memory struct {
	mu sync.RWMutex
	m  map[string][]byte
}

func (s *memory) Get(_ context.Context, key string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[key]
	return v, ok, nil
}

func (s *memory) Put(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = append([]byte(nil), value...)
	return nil
}

func (s *memory) List(_ context.Context, prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for k := range s.m {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}
