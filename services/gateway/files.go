package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/site"
	"github.com/taubyte/tau/pkg/specs"
)

// files.go serves a route whose handler is a package: its built files, answered by
// the gateway itself, since nothing has to run to read a file.

// packageKind is a route handing a request to a package's files.
const packageKind = specs.GatewayRouteHandlerKindPackage

// Artifacts says which artifact a package's build produced for an environment.
type Artifacts interface {
	Artifact(ctx context.Context, project, environment, kind, resourceID string) (string, error)
}

// Assets is where an artifact's bytes are read.
type Assets interface {
	Read(ctx context.Context, id string) (io.ReadCloser, error)
}

// ErrNoFiles is a route serving a package's files on a gateway given nothing to read
// them from.
var ErrNoFiles = errors.New("gateway: this route serves a package's files and this gateway has nothing to read them from")

// DefaultFilesBudget bounds the unpacked artifacts a gateway holds at once.
const DefaultFilesBudget = 1 << 30

// WithFiles is where a package handler's artifact is found and read. Optional:
// without it such a route is refused by name.
func WithFiles(a Artifacts, assets Assets) Option {
	return func(o *options) error {
		if a == nil || assets == nil {
			return errors.New("gateway: files need both an artifact lookup and an asset store")
		}
		o.artifacts, o.assets = a, assets
		return nil
	}
}

// unpacked holds artifacts unpacked on disk, by content id, within a byte budget.
// Content ids never change content, so an entry is never stale, only evicted.
type unpacked struct {
	root   string
	budget int64

	mu   sync.Mutex
	held map[string]*unpackedEntry
	used int64
	tick uint64
}

type unpackedEntry struct {
	ready   chan struct{}
	files   *site.Files
	err     error
	users   int
	last    uint64
	evicted bool
}

func newUnpacked(budget int64) *unpacked {
	return &unpacked{budget: budget, held: map[string]*unpackedEntry{}}
}

// acquire is the artifact's files, unpacked once whoever asks first; release when the
// request is answered.
func (u *unpacked) acquire(ctx context.Context, id string, open func() (io.ReadCloser, error)) (*site.Files, func(), error) {
	u.mu.Lock()
	e, ok := u.held[id]
	if !ok {
		e = &unpackedEntry{ready: make(chan struct{})}
		u.held[id] = e
	}
	e.users++
	u.tick++
	e.last = u.tick
	u.mu.Unlock()

	if !ok {
		e.files, e.err = u.unpack(id, open)
		u.mu.Lock()
		if e.err != nil {
			delete(u.held, id)
		} else {
			u.used += e.files.Size()
			u.evict()
		}
		u.mu.Unlock()
		close(e.ready)
	}

	select {
	case <-e.ready:
	case <-ctx.Done():
		u.release(e)
		return nil, nil, ctx.Err()
	}
	if e.err != nil {
		u.release(e)
		return nil, nil, e.err
	}
	return e.files, func() { u.release(e) }, nil
}

func (u *unpacked) unpack(id string, open func() (io.ReadCloser, error)) (*site.Files, error) {
	content, err := open()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", id, err)
	}
	defer content.Close()

	u.mu.Lock()
	if u.root == "" {
		if u.root, err = os.MkdirTemp("", "tau-gateway-files-"); err != nil {
			u.mu.Unlock()
			return nil, err
		}
	}
	root := u.root
	u.mu.Unlock()

	dir, err := os.MkdirTemp(root, "artifact-")
	if err != nil {
		return nil, err
	}
	files, err := site.Unpack(content, filepath.Join(dir, "files"), site.MaxSize)
	if err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("unpacking %s: %w", id, err)
	}
	return files, nil
}

func (u *unpacked) release(e *unpackedEntry) {
	u.mu.Lock()
	defer u.mu.Unlock()
	e.users--
	if e.evicted && e.users == 0 && e.files != nil {
		e.files.Close()
	}
}

// evict drops the least recently used artifacts no request holds until the rest fit;
// one still in use goes when its last request ends. The caller holds mu.
func (u *unpacked) evict() {
	for u.used > u.budget {
		var oldest string
		var at uint64
		for id, e := range u.held {
			if e.files == nil {
				continue
			}
			if oldest == "" || e.last < at {
				oldest, at = id, e.last
			}
		}
		if oldest == "" {
			return
		}
		e := u.held[oldest]
		delete(u.held, oldest)
		u.used -= e.files.Size()
		e.evicted = true
		if e.users == 0 {
			e.files.Close()
		}
	}
}

func (u *unpacked) close() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.root == "" {
		return nil
	}
	return os.RemoveAll(u.root)
}

// serveFiles answers a request from the package the route names, or says why not.
func (s *Service) serveFiles(ctx context.Context, m match, proj *specs.Project, projectName, environment string, req *invoke.Request) (*invoke.Response, int, string) {
	if s.artifacts == nil {
		return nil, http.StatusNotImplemented, ErrNoFiles.Error()
	}
	at := m.address(projectName, environment)
	_, id, err := proj.Resource(at.Scope, specs.KindPackage, at.Name)
	if err != nil {
		return nil, http.StatusNotFound, "no package " + at.Name + " in this environment"
	}
	cid, err := s.artifacts.Artifact(ctx, projectName, environment, specs.KindPackage, id)
	if err != nil {
		s.logger.Warnf("%s/%s package %s has no artifact: %v", projectName, environment, at.Name, err)
		return nil, statusFor(err), "this package has not been built"
	}
	files, release, err := s.files.acquire(ctx, cid, func() (io.ReadCloser, error) { return s.assets.Read(ctx, cid) })
	if err != nil {
		s.logger.Warnf("%s/%s package %s, artifact %s: %v", projectName, environment, at.Name, cid, err)
		return nil, statusFor(err), "this package's files could not be read"
	}
	defer release()

	h := m.handler()
	return files.Serve(site.Serving{Mode: h.HandlerMode, Index: h.HandlerIndex, NotFound: h.HandlerNotFound}, req.Method, req.Path), 0, ""
}

// Close removes what this gateway unpacked.
func (s *Service) Close() error { return s.files.close() }
