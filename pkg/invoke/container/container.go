// Package container runs a container as a serverless tier.
//
// It is the first real implementation of invoke.Runtime, and it is what makes
// "bring the code you already have" true: a wasm component is the
// differentiator, but a container is what runs the thing somebody already
// built.
//
// # The runtime contract is tau's
//
// Request in, response out. A container function is HANDED a request — it does
// not poll anything, and no vendor's invocation protocol appears here. What the
// container sees is an ordinary HTTP server's job: listen on $PORT, answer.
// That is the same contract Heroku, Cloud Run, Fly and Render all use, which is
// why an imported app usually needs no change at all.
//
// # Addressing, and why the engine picks the port
//
// This is forced by pkg/containers and it is worth stating so nobody "fixes" it:
//
//   - core.NetworkConfig.PortMappings is documented DOCKER ONLY; the containerd
//     backend runs without CNI and accepts only host and none modes.
//   - core.ContainerInfo carries ID, Status, ExitCode, StartedAt, Image and
//     Resources — and NO assigned IP or resolved port.
//
// So an ephemeral mapping cannot be read back, and a bridge IP does not exist on
// one of the two backends. The engine therefore ALLOCATES a free host port
// itself and tells the container which one to use, via $PORT. On docker it also
// maps host→container so the number means the same thing inside; on containerd
// the container is on the host network and already does. One scheme, both
// backends, no change to pkg/containers.
//
// Each instance gets its own port, so the pool starting N instances of one spec
// for concurrency does not collide.
//
// # Readiness is dialled, not assumed
//
// A container that has STARTED is not a container that is LISTENING. Start
// blocks until the port accepts a connection or the deadline passes, because
// invoke.Pool calls Start on the request path — returning early would hand the
// first request to a socket nobody is on yet, and that failure would be
// attributed to the request rather than to the cold start.
package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/invoke"
)

// Name is what the config vocabulary calls this tier.
const Name = "container"

var (
	ErrNoBackend = errors.New("container: no container backend")
	ErrNoImage   = errors.New("container: a spec with no image has nothing to run")
	ErrNotReady  = errors.New("container: the container never listened")
	ErrClosed    = errors.New("container: instance is closed")
	ErrExited    = errors.New("container: the container exited")
)

// Backend is the slice of a container backend this runtime needs.
//
// IT IS DECLARED HERE, BY THE CONSUMER, AND IS AS NARROW AS THE USE — the same
// doctrine services/storage states about not taking a peer.Node to get at one
// datastore. core.Backend has eleven methods covering builds, logs, image
// cleanup and health checks; running a function needs six. Declaring the six is
// what lets every test in this package run with no daemon anywhere.
//
// *containers.Client and any core.Backend satisfy it.
type Backend interface {
	Create(ctx context.Context, cfg *core.ContainerConfig) (core.ContainerID, error)
	Start(ctx context.Context, id core.ContainerID) error
	Stop(ctx context.Context, id core.ContainerID) error
	Remove(ctx context.Context, id core.ContainerID) error
	Inspect(ctx context.Context, id core.ContainerID) (*core.ContainerInfo, error)
	Image(name string) core.Image
}

// Option configures the runtime. The backend is the only thing it cannot work
// without, and it is a With… like everything else so a caller composing a node
// registers what it found rather than filling a struct whose shape it has to
// know.
type Option func(*options) error

type options struct {
	backend       Backend
	readyTimeout  time.Duration
	readyInterval time.Duration
	stopTimeout   time.Duration
	port          func() (int, error)
	dial          func(ctx context.Context, network, addr string) (net.Conn, error)
}

// WithBackend is the container backend. Required.
func WithBackend(b Backend) Option {
	return func(o *options) error {
		if b == nil {
			return ErrNoBackend
		}
		o.backend = b
		return nil
	}
}

// WithReadyTimeout bounds how long Start waits for the container to listen.
// Default 60s — an image that has to be pulled and a runtime that has to boot
// are both inside that budget.
func WithReadyTimeout(d time.Duration) Option {
	return func(o *options) error {
		if d <= 0 {
			return fmt.Errorf("container: ready timeout %s: nothing is ever ready in no time", d)
		}
		o.readyTimeout = d
		return nil
	}
}

// WithReadyInterval is how often readiness is redialled. Default 25ms: a cold
// start is on the request path, so asking too often costs far less than
// noticing late.
func WithReadyInterval(d time.Duration) Option {
	return func(o *options) error {
		if d <= 0 {
			return fmt.Errorf("container: ready interval %s", d)
		}
		o.readyInterval = d
		return nil
	}
}

// WithStopTimeout bounds how long Close waits for a container to stop before
// removing it anyway. Default 10s.
func WithStopTimeout(d time.Duration) Option {
	return func(o *options) error {
		if d <= 0 {
			return fmt.Errorf("container: stop timeout %s", d)
		}
		o.stopTimeout = d
		return nil
	}
}

// WithPortAllocator injects how a free host port is chosen. Tests use it;
// production gets an ephemeral bind-then-close.
func WithPortAllocator(f func() (int, error)) Option {
	return func(o *options) error {
		if f == nil {
			return errors.New("container: a nil port allocator")
		}
		o.port = f
		return nil
	}
}

// WithDialer injects how the readiness probe and the request path connect.
func WithDialer(f func(ctx context.Context, network, addr string) (net.Conn, error)) Option {
	return func(o *options) error {
		if f == nil {
			return errors.New("container: a nil dialer")
		}
		o.dial = f
		return nil
	}
}

// Defaults.
const (
	DefaultReadyTimeout  = 60 * time.Second
	DefaultReadyInterval = 25 * time.Millisecond
	DefaultStopTimeout   = 10 * time.Second
)

func (o *options) withDefaults() error {
	if o.backend == nil {
		return ErrNoBackend
	}
	if o.readyTimeout == 0 {
		o.readyTimeout = DefaultReadyTimeout
	}
	if o.readyInterval == 0 {
		o.readyInterval = DefaultReadyInterval
	}
	if o.stopTimeout == 0 {
		o.stopTimeout = DefaultStopTimeout
	}
	if o.port == nil {
		o.port = freePort
	}
	if o.dial == nil {
		d := &net.Dialer{}
		o.dial = d.DialContext
	}
	return nil
}

// Runtime starts containers and serves requests to them.
type Runtime struct {
	opts options

	// image is what to run for a spec, and it is injected rather than derived.
	// A Spec names WHAT to run and a revision; turning that into an image
	// reference is the build service's knowledge, not this package's, and
	// guessing a naming convention here would hardcode one.
	image func(invoke.Spec) (Image, error)
}

// Image is everything the engine needs to know about what to run. It comes from
// whatever composed the node — for a built resource that is the build service's
// output; for an imported one it may be a registry reference the user gave.
type Image struct {
	// Ref is the image reference, e.g. "registry/app@sha256:…".
	Ref string

	// Command overrides the image's entrypoint. Usually empty.
	Command []string

	// Env is what the container gets on top of PORT. The engine sets PORT
	// itself and refuses to let this override it — see start().
	Env map[string]string

	// Memory is the hard memory limit in bytes. Zero means the runtime's
	// default, which for a serverless tier is a thing an operator should set.
	Memory int64
}

// ImageFunc resolves a spec to an image.
type ImageFunc func(invoke.Spec) (Image, error)

// ImageFromSpec is the ImageFunc for a composer whose Spec already carries
// everything: the worker reads the artifact, the memory limit and the
// environment off the compiled resource, so there is nothing left to look up.
//
// It exists so that the common case is a named function rather than the same
// four-line closure at every composition root, and the injected ImageFunc stays
// for the cases that are not it — a registry mirror, a local override, a tier
// whose artifact is not a reference at all.
func ImageFromSpec(spec invoke.Spec) (Image, error) {
	return Image{Ref: spec.Artifact, Env: spec.Env, Memory: spec.Memory}, nil
}

// New builds the runtime. resolve says what image a spec runs.
func New(resolve ImageFunc, opts ...Option) (*Runtime, error) {
	if resolve == nil {
		return nil, errors.New("container: no way to tell what image a spec runs")
	}
	var o options
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, err
		}
	}
	if err := o.withDefaults(); err != nil {
		return nil, err
	}
	return &Runtime{opts: o, image: resolve}, nil
}

// Name is the tier's name in the config vocabulary.
func (e *Runtime) Name() string { return Name }

var _ invoke.Runtime = (*Runtime)(nil)

// Start brings up one container and waits until it is listening.
func (e *Runtime) Start(ctx context.Context, spec invoke.Spec) (invoke.Instance, error) {
	img, err := e.image(spec)
	if err != nil {
		return nil, fmt.Errorf("container: resolving image for %s: %w", spec.Name, err)
	}
	if img.Ref == "" {
		return nil, ErrNoImage
	}

	port, err := e.opts.port()
	if err != nil {
		return nil, fmt.Errorf("container: allocating a port: %w", err)
	}

	// Pull before create. A backend whose create auto-pulls does this twice and
	// the second is free; one whose create does not would otherwise fail on
	// every genuinely cold node, which is the case this exists for.
	if im := e.opts.backend.Image(img.Ref); im != nil {
		if !im.Exists(ctx) {
			if err := im.Pull(ctx); err != nil {
				return nil, fmt.Errorf("container: pulling %s: %w", img.Ref, err)
			}
		}
	}

	id, err := e.opts.backend.Create(ctx, e.config(img, port))
	if err != nil {
		return nil, fmt.Errorf("container: creating %s: %w", img.Ref, err)
	}

	inst := &instance{
		eng:  e,
		id:   id,
		port: port,
		spec: spec,
	}

	if err := e.opts.backend.Start(ctx, id); err != nil {
		inst.Close()
		return nil, fmt.Errorf("container: starting %s: %w", img.Ref, err)
	}

	if err := inst.waitReady(ctx); err != nil {
		inst.Close()
		return nil, err
	}
	return inst, nil
}

// config is what the container is created with, and every security-relevant
// field is set HERE rather than left to a backend default.
//
// EGRESS IS RESTRICTED BECAUSE THIS IS UNTRUSTED CODE. pkg/builder is the only
// other caller of pkg/containers and it restricts egress too, but for a
// different reason and with a different threat model — a build runs a
// repository's own script, this runs whatever a tenant deployed. The settings
// must not be copied across on the assumption that both are "containers we run".
//
// RestrictEgress blocks node-local and RFC1918 destinations — the cloud metadata
// endpoint, loopback and the private network — while leaving the public internet
// reachable, because a function that cannot make an outbound call is not a
// serverless function. It is fail-closed in the backend: no firewall, no start.
func (e *Runtime) config(img Image, port int) *core.ContainerConfig {
	env := make([]string, 0, len(img.Env)+1)
	for k, v := range img.Env {
		if k == "PORT" {
			// Refused rather than honoured: the engine dials the port it
			// allocated, so a container listening somewhere else is a container
			// that never becomes ready — a confusing timeout instead of a clear
			// refusal.
			continue
		}
		env = append(env, k+"="+v)
	}
	env = append(env, "PORT="+strconv.Itoa(port))

	cfg := &core.ContainerConfig{
		Image:   img.Ref,
		Command: img.Command,
		Env:     env,
		Network: &core.NetworkConfig{
			// Host mode is the one both backends support: containerd runs
			// without CNI and takes only host or none, and on docker it makes
			// the allocated port mean the same number inside and out. The
			// mapping below is redundant under host mode on docker and harmless;
			// it is what makes bridge mode work if a backend ever prefers it.
			Mode: "host",
			PortMappings: []core.PortMapping{{
				HostPort:      port,
				ContainerPort: port,
				Protocol:      "tcp",
				HostIP:        "127.0.0.1",
			}},
			RestrictEgress: true,
		},
	}
	if img.Memory > 0 {
		cfg.Resources = &core.ResourceLimits{Memory: img.Memory}
	}
	// Privileges stays nil. Nothing a function does needs a capability, a host
	// device or an unconfined seccomp profile, and the one caller that does need
	// them (BuildKit assembling layers) is not this one.
	return cfg
}

// freePort asks the kernel for one and immediately gives it back.
//
// THERE IS A RACE HERE AND IT IS THE ORDINARY ONE: between closing and the
// container binding, something else could take the port. The alternatives are
// worse — holding the listener open means the container cannot bind at all, and
// there is no way to read an ephemeral port back out of either backend
// (core.ContainerInfo has no port field). A lost race surfaces as a container
// that fails to bind and therefore fails readiness, which is a clear error
// rather than a silent misroute.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// instance is one running container.
type instance struct {
	eng  *Runtime
	id   core.ContainerID
	port int
	spec invoke.Spec

	mu     sync.Mutex
	closed bool
}

var _ invoke.Instance = (*instance)(nil)

func (i *instance) addr() string { return "127.0.0.1:" + strconv.Itoa(i.port) }

// waitReady dials until the container accepts, or the budget runs out.
//
// IT ALSO WATCHES FOR THE CONTAINER DYING. An image whose entrypoint exits
// immediately — a bad command, a missing file, a crash on boot — would otherwise
// burn the whole ReadyTimeout before saying anything, and say the wrong thing
// when it did. Inspect tells us it is gone; that is a different error and a
// much more useful one.
func (i *instance) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(i.eng.opts.readyTimeout)
	t := time.NewTicker(i.eng.opts.readyInterval)
	defer t.Stop()

	for {
		conn, err := i.eng.opts.dial(ctx, "tcp", i.addr())
		if err == nil {
			conn.Close()
			return nil
		}

		if info, ierr := i.eng.opts.backend.Inspect(ctx, i.id); ierr == nil && info != nil && exited(info) {
			return fmt.Errorf("%w before it listened: status %q, exit code %d",
				ErrExited, info.Status, info.ExitCode)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("%w on %s within %s", ErrNotReady, i.addr(), i.eng.opts.readyTimeout)
			}
		}
	}
}

func exited(info *core.ContainerInfo) bool {
	switch info.Status {
	case "exited", "dead", "stopped":
		return true
	}
	return false
}

// Serve hands one request to the container and returns what it answered.
func (i *instance) Serve(ctx context.Context, req *invoke.Request) (*invoke.Response, error) {
	i.mu.Lock()
	closed := i.closed
	i.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	if req == nil {
		req = &invoke.Request{}
	}

	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	path := req.Path
	if path == "" {
		path = "/"
	}

	hr, err := http.NewRequestWithContext(ctx, method, "http://"+i.addr()+path, bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("container: building the request: %w", err)
	}
	for k, vs := range req.Headers {
		for _, v := range vs {
			hr.Header.Add(k, v)
		}
	}

	// A CLIENT PER REQUEST, WITH NO CONNECTION REUSE ACROSS INSTANCES. The pool
	// may retire this instance the moment the request fails, and a pooled
	// transport holding a keep-alive to a container that has been removed hands
	// the next caller a connection to nothing.
	client := &http.Client{Transport: &http.Transport{DialContext: i.eng.opts.dial}}
	defer client.CloseIdleConnections()

	resp, err := client.Do(hr)
	if err != nil {
		return nil, fmt.Errorf("container: %s: %w", i.addr(), err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("container: reading the response: %w", err)
	}
	return &invoke.Response{
		Status:  resp.StatusCode,
		Headers: resp.Header,
		Body:    body,
	}, nil
}

// Close stops and removes the container. It is safe to call twice.
//
// REMOVE HAPPENS EVEN IF STOP FAILED. A node that left a container behind every
// time a stop timed out would fill its disk with dead containers, and the pool
// retires instances routinely — every failed request is one.
func (i *instance) Close() error {
	i.mu.Lock()
	if i.closed {
		i.mu.Unlock()
		return nil
	}
	i.closed = true
	i.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), i.eng.opts.stopTimeout)
	defer cancel()

	stopErr := i.eng.opts.backend.Stop(ctx, i.id)
	removeErr := i.eng.opts.backend.Remove(ctx, i.id)
	return errors.Join(stopErr, removeErr)
}
