package dream

import (
	"context"
	"fmt"
	"os"
	"strings"

	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
)

type Option func(*Multiverse) error

func LoadPersistent() Option {
	return func(m *Multiverse) error {
		m.loadPersistent = true
		return nil
	}
}

func Name(name string) Option {
	return func(m *Multiverse) error {
		if name == "" {
			m.name = DefaultUniverseName
		} else {
			m.name = strings.ToLower(name)
		}
		return nil
	}
}

func New(ctx context.Context, opts ...Option) (*Multiverse, error) {
	m := &Multiverse{
		name: DefaultUniverseName,
	}

	for _, opt := range opts {
		if err := opt(m); err != nil {
			return nil, err
		}
	}

	return m.init(ctx)
}

func (m *Multiverse) init(ctx context.Context) (*Multiverse, error) {
	m.ctx, m.ctxC = context.WithCancel(ctx)
	m.universes = make(map[string]*Universe)

	if m.loadPersistent {
		err, fatal := m.loadPersistentUniverses()
		if fatal {
			m.Close()
			return nil, err
		}
		logger.Debugf("Loading persistent universes skipped: %w", err)
	}

	return m, nil
}

func (m *Multiverse) loadPersistentUniverses() (err error, fatal bool) {
	cacheFolder, err := GetCacheFolder(m.name)
	if err != nil {
		return err, false
	}

	// Check if cache folder exists
	if _, err := os.Stat(cacheFolder); os.IsNotExist(err) {
		// Return empty list if cache folder doesn't exist
		return err, false
	}

	// Read the cache folder contents
	entries, err := os.ReadDir(cacheFolder)
	if err != nil {
		return err, false
	}

	// Look for universe directories with pattern "universe-{name}"
	for _, entry := range entries {
		if entry.IsDir() {
			dirName := entry.Name()
			// Check if this directory follows the universe pattern
			if universeName, found := strings.CutPrefix(dirName, "universe-"); found {
				_, err := m.New(UniverseConfig{
					Name:     universeName,
					KeepRoot: true,
				})
				if err != nil {
					return err, true
				}
			}
		}
	}

	return nil, false
}

func (m *Multiverse) Context() context.Context {
	return m.ctx
}

func (m *Multiverse) Name() string {
	return m.name
}

func (m *Multiverse) Close() error {
	m.universesLock.RLock()
	defer m.universesLock.RUnlock()
	for _, u := range m.universes {
		u.Cleanup()
	}
	m.universes = nil
	m.ctxC()
	return nil
}

func (m *Multiverse) List() ([]string, error) {
	m.universesLock.RLock()
	defer m.universesLock.RUnlock()
	names := make([]string, 0, len(m.universes))
	for name := range m.universes {
		names = append(names, name)
	}
	return names, nil
}

func (m *Multiverse) ListPersistent() ([]string, error) {
	m.universesLock.RLock()
	defer m.universesLock.RUnlock()
	names := make([]string, 0, len(m.universes))
	for name := range m.universes {
		if m.universes[name].Persistent() {
			names = append(names, name)
		}
	}
	return names, nil
}

func (m *Multiverse) ListTemporary() ([]string, error) {
	m.universesLock.RLock()
	defer m.universesLock.RUnlock()
	names := make([]string, 0, len(m.universes))
	for name := range m.universes {
		if !m.universes[name].Persistent() {
			names = append(names, name)
		}
	}
	return names, nil
}

func (m *Multiverse) Universe(name string) (*Universe, error) {
	name = strings.ToLower(name)
	m.universesLock.RLock()
	defer m.universesLock.RUnlock()
	u, exists := m.universes[name]
	if !exists {
		return nil, fmt.Errorf("universe not found")
	}
	return u, nil
}

func (m *Multiverse) Delete(name string) error {
	name = strings.ToLower(name)
	m.universesLock.RLock()
	defer m.universesLock.RUnlock()

	if _, exists := m.universes[name]; !exists {
		return fmt.Errorf("universe not found")
	}

	delete(m.universes, name)

	return nil
}

func (m *Multiverse) Status() Status {
	status := make(Status)
	m.universesLock.RLock()
	defer m.universesLock.RUnlock()
	for _, u := range m.universes {
		u.lock.RLock()
		status[u.name] = UniverseStatus{
			Root:      u.root,
			SwarmKey:  u.swarmKey,
			NodeCount: len(u.all),
			Simples: func() []string {
				_simples := make([]string, 0)
				for s := range u.simples {
					_simples = append(_simples, s)
				}
				return _simples
			}(),
			Nodes: func() map[string][]string {
				_nodes := make(map[string][]string)
				u.lock.RLock()
				defer u.lock.RUnlock()
				for _, s := range u.all {
					paddrs := s.Peer().Addrs()
					addrs := make([]string, 0, len(paddrs))
					for _, addr := range paddrs {
						addrs = append(addrs, addr.String())
					}
					_nodes[s.ID().String()] = addrs
				}
				return _nodes
			}(),
			Services: func() []ServiceStatus {
				_services := make([]ServiceStatus, 0, len(commonSpecs.Services))
				for _, name := range commonSpecs.Services {
					nodes := u.service[name].nodes
					if nodes != nil {
						_services = append(_services, ServiceStatus{Name: name, Copies: len(nodes)})
					}
				}
				return _services
			}(),
		}
		u.lock.RUnlock()
	}
	return status
}

func (m *Multiverse) Universes() Status {
	status := make(Status)
	m.universesLock.RLock()
	defer m.universesLock.RUnlock()
	for _, u := range m.universes {
		u.lock.RLock()
		status[u.name] = UniverseStatus{
			SwarmKey:  u.swarmKey,
			NodeCount: len(u.all),
		}
		u.lock.RUnlock()
	}
	return status
}
