// Package container runs a container as a serverless tier: request in, response
// out. The container listens on $PORT and answers, the contract Heroku, Cloud
// Run, Fly and Render use, so an imported app usually needs no change.
//
// The engine allocates a free host port itself and passes it in $PORT, because
// neither backend reports an assigned port or IP (port mappings are docker-only,
// containerd runs without CNI). Docker maps host to container; containerd is on
// the host network. Each instance gets its own port.
//
// Start blocks until the port accepts a connection, since invoke.Pool starts on
// the request path.
package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/invoke"
)

// Name is what the config vocabulary calls this tier.
const Name = specs.PackageTypeOci

var (
	ErrNoBackend = errors.New("container: no container backend")
	ErrNoImage   = errors.New("container: a spec with no image has nothing to run")

	// ErrNoSuchPort is a caller naming a port the workload never declared, a config
	// fault.
	ErrNoSuchPort = errors.New("container: that workload declares no port by that name")
	ErrNotReady   = errors.New("container: the container never listened")
	ErrClosed     = errors.New("container: instance is closed")
	ErrExited     = errors.New("container: the container exited")

	// ErrNoMounts is a workload declaring a filesystem this tier cannot give it, the
	// one unserved capability refused rather than warned about, since running without
	// it loses data silently (a database writing into the discarded container layer).
	ErrNoMounts = errors.New("container: this tier cannot mount a filesystem, and the workload declared one")
)

// Backend is the slice of a container backend this runtime needs, declared by the
// consumer so tests need no daemon; any core.Backend satisfies it.
type Backend interface {
	Create(ctx context.Context, cfg *core.ContainerConfig) (core.ContainerID, error)
	Start(ctx context.Context, id core.ContainerID) error
	Stop(ctx context.Context, id core.ContainerID) error
	Remove(ctx context.Context, id core.ContainerID) error
	Inspect(ctx context.Context, id core.ContainerID) (*core.ContainerInfo, error)
	Image(name string) core.Image

	// Wait and Logs are what a run to completion (job.go) needs: when it ended,
	// and what it said.
	Wait(ctx context.Context, id core.ContainerID) error
	Logs(ctx context.Context, id core.ContainerID) (io.ReadCloser, error)
}

// Option configures the runtime; the backend is required.
type Option func(*options) error

type options struct {
	backend       Backend
	volumes       Volumes
	readyTimeout  time.Duration
	readyInterval time.Duration
	stopTimeout   time.Duration
	port          func() (int, error)
	dial          func(ctx context.Context, network, addr string) (net.Conn, error)
	warn          func(format string, args ...any)
	secrets       Secrets
}

// Volumes gives this tier the host directory a mount grant names; where volumes
// live is the composer's. Without one, a workload declaring a mount is refused.
type Volumes interface {
	// Mount resolves one mount grant to a directory on this host. It is called
	// on the request path, once per cold start, so it must not walk anything
	// large that it has already walked.
	Mount(ctx context.Context, grant invoke.Grant) (string, error)
}

// WithVolumes turns a declared filesystem into a real one.
func WithVolumes(v Volumes) Option {
	return func(o *options) error {
		if v == nil {
			return errors.New("container: a nil volume backing")
		}
		o.volumes = v
		return nil
	}
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

// WithWarn is where this tier reports what an operator must know but is no reason
// to refuse a workload.
func WithWarn(warn func(format string, args ...any)) Option {
	return func(o *options) error {
		if warn == nil {
			return errors.New("container: a nil warn")
		}
		o.warn = warn
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

	// image resolves a spec to an image; injected because the naming is the build
	// service's knowledge.
	image func(invoke.Spec) (Image, error)
}

// Image is what the engine runs, from whatever composed the node.
type Image struct {
	// Ref is the image reference, e.g. "registry/app@sha256:…".
	Ref string

	// Command overrides the image's entrypoint. Usually empty.
	Command []string

	// Env is added on top of PORT, which it may not override.
	Env map[string]string

	// Memory is the hard memory limit in bytes. Zero means the runtime's
	// default, which for a serverless tier is a thing an operator should set.
	Memory int64

	// CPU is a ceiling in millicores. Zero is none.
	CPU int64
}

// ImageFunc resolves a spec to an image.
type ImageFunc func(invoke.Spec) (Image, error)

// ImageFromSpec is the ImageFunc for a Spec that already carries the artifact,
// limits and environment.
func ImageFromSpec(spec invoke.Spec) (Image, error) {
	return Image{Ref: spec.Artifact, Env: spec.Env, Memory: spec.Memory, CPU: spec.CPU}, nil
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
	mounts, err := e.mounts(ctx, spec)
	if err != nil {
		return nil, err
	}
	e.egressGap(spec)
	e.unserved(spec)

	held, secretMounts, err := e.vaultFor(spec)
	if err != nil {
		return nil, err
	}
	inst, err := e.launch(ctx, spec, append(mounts, secretMounts...))
	if err != nil {
		return nil, errors.Join(err, held.closeVault())
	}
	// HELD ONCE THE INSTANCE EXISTS: a start retried for a taken port closes the
	// attempt that failed, and that close must not take the vault with it.
	inst.(vaultHolder).hold(held)
	return inst, nil
}

// launch is Start once its mounts are known.
func (e *Runtime) launch(ctx context.Context, spec invoke.Spec, mounts []core.VolumeMount) (invoke.Instance, error) {

	img, err := e.image(spec)
	if err != nil {
		return nil, fmt.Errorf("container: resolving image for %s: %w", spec.Name, err)
	}
	if img.Ref == "" {
		return nil, ErrNoImage
	}

	// A WORKLOAD THAT RUNS TO COMPLETION listens on nothing, so nothing below —
	// ports, the Runtime API, readiness — applies to it.
	if spec.Completes {
		return e.startJob(ctx, spec, img, mounts)
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

	for attempt := 1; ; attempt++ {
		inst, err := e.startOnce(ctx, spec, img, mounts)
		if err == nil {
			return inst, nil
		}
		if attempt == startAttempts || !portTaken(err) {
			return nil, err
		}
	}
}

// startAttempts bounds the starts of one workload whose host port was taken between
// choosing it and binding it. Nothing can hold a port across that gap for a backend,
// so the start chooses again.
const startAttempts = 3

// portTaken says a start failed because its host port was bound in the meantime.
func portTaken(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "address already in use") || strings.Contains(msg, "port is already allocated")
}

// startOnce chooses ports, creates the container and waits for it to be ready.
func (e *Runtime) startOnce(ctx context.Context, spec invoke.Spec, img Image, mounts []core.VolumeMount) (*instance, error) {
	// A workload that declared ports binds those numbers, as off-the-shelf images do;
	// one that declared none is told a number in $PORT.
	ports, port, err := e.portsFor(spec)
	if err != nil {
		return nil, err
	}

	// Which host port answers which declared name, so a caller naming a port reaches
	// the right socket.
	reachable := make(map[string]int, len(spec.Ports))
	for i, name := range sortedPortNames(spec.Ports) {
		reachable[name] = ports[i].HostPort
	}

	// THE RUNTIME API IS BOUND BEFORE THE CONTAINER IS CREATED, because the
	// client polls as soon as it starts and an address that is not listening
	// yet is a client that backs off.
	api, err := newLambdaAPI()
	if err != nil {
		return nil, err
	}

	id, err := e.opts.backend.Create(ctx, e.config(img, ports, port, api.address(), mounts))
	if err != nil {
		api.Close()
		return nil, fmt.Errorf("container: creating %s: %w", img.Ref, err)
	}

	inst := &instance{
		eng:       e,
		id:        id,
		port:      port,
		spec:      spec,
		reachable: reachable,
		lambda:    api,
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

// portsFor is every port this container is reached on, and the one $PORT names.
// On docker a declared port is translated (allocated host side, declared
// container side), so two workloads can bind 5432. On containerd the host port is
// the declared number and two such workloads collide; the backend's conformance
// refusal says so.
func (e *Runtime) portsFor(spec invoke.Spec) (mapped []core.PortMapping, told int, err error) {
	if len(spec.Ports) == 0 {
		port, err := e.opts.port()
		if err != nil {
			return nil, 0, fmt.Errorf("container: allocating a port: %w", err)
		}
		return []core.PortMapping{{
			HostPort: port, ContainerPort: port, Protocol: "tcp", HostIP: "127.0.0.1",
		}}, port, nil
	}

	taken := make(map[int]bool, len(spec.Ports))
	for _, name := range sortedPortNames(spec.Ports) {
		host, err := e.distinctPort(taken)
		if err != nil {
			return nil, 0, fmt.Errorf("container: allocating a port for %q: %w", name, err)
		}
		taken[host] = true
		mapped = append(mapped, core.PortMapping{
			HostPort: host, ContainerPort: spec.Ports[name], Protocol: "tcp", HostIP: "127.0.0.1",
		})
	}

	// $PORT IS STILL SET, to the first declared port's host side, because an
	// image that reads it is not made to stop by an author declaring where it
	// listens — and a workload that declares one port and reads $PORT is a
	// perfectly ordinary thing to write.
	return mapped, mapped[0].HostPort, nil
}

// distinctPort asks the allocator again when it hands back a port this container
// already maps: a port found free by binding and closing is free again at once, and
// the kernel may give it out twice in a row.
func (e *Runtime) distinctPort(taken map[int]bool) (int, error) {
	for range maxPortTries {
		port, err := e.opts.port()
		if err != nil || !taken[port] {
			return port, err
		}
	}
	return 0, fmt.Errorf("%d tries gave only ports already mapped", maxPortTries)
}

const maxPortTries = 16

// config is what the container is created with, every security-relevant field set
// here rather than left to a backend default. Egress is restricted because this is
// a tenant's code: node-local, metadata and private ranges are blocked, the public
// internet reachable, fail-closed in the backend.
func (e *Runtime) config(img Image, ports []core.PortMapping, port int, runtimeAPI string, mounts []core.VolumeMount) *core.ContainerConfig {
	env := make([]string, 0, len(img.Env)+2)
	for k, v := range img.Env {
		if k == "PORT" {
			// Refused: the engine dials the port it allocated, so honouring it would only
			// time out.
			continue
		}
		env = append(env, k+"="+v)
	}
	// A workload that listens is told where. One that runs to completion has
	// no port, and a PORT=0 would be telling it something false.
	if port > 0 {
		env = append(env, "PORT="+strconv.Itoa(port))
	}

	// The Lambda Runtime API address is always set; non-Lambda images ignore it.
	if runtimeAPI != "" {
		env = append(env, "AWS_LAMBDA_RUNTIME_API="+runtimeAPI)
	}

	cfg := &core.ContainerConfig{
		Image:   img.Ref,
		Command: img.Command,
		Env:     env,
		Network: &core.NetworkConfig{
			// No network mode is named: "host" was refused by both backends (docker cannot
			// filter egress on host networking, containerd refuses a mapping beside it). The
			// tier states the intent and each backend realises it (backend_reality_test.go).
			PortMappings:   ports,
			RestrictEgress: true,
		},
		// The declared mounts, as host paths.
		Volumes: mounts,
	}
	if img.Memory > 0 || img.CPU > 0 {
		cfg.Resources = &core.ResourceLimits{Memory: img.Memory}
	}
	// A MILLICORE CEILING IS A CFS QUOTA over a 100ms period: 250 millicores
	// is 25ms of CPU in every 100ms, which is what both backends enforce.
	if img.CPU > 0 {
		cfg.Resources.CPUPeriod = cpuPeriod
		cfg.Resources.CPUQuota = img.CPU * cpuPeriod / 1000
	}
	// Privileges stays nil: nothing a function does needs one.
	return cfg
}

// cpuPeriod is the CFS period a CPU ceiling is expressed over, in microseconds.
const cpuPeriod = 100_000

// sortedPortNames is the declared names in a stable order, so allocation is
// deterministic and portsFor and reachable agree.
func sortedPortNames(ports map[string]int) []string {
	names := make([]string, 0, len(ports))
	for name := range ports {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Dial opens a connection to one of this workload's declared ports by name, for
// L4 callers that need the socket rather than a round trip (invoke.Dials). An
// empty name is the $PORT of a workload that declared none.
func (i *instance) Dial(ctx context.Context, name string) (net.Conn, error) {
	port := i.port
	if name != "" {
		declared, ok := i.reachable[name]
		if !ok {
			return nil, fmt.Errorf("%w: %s declares no port called %q", ErrNoSuchPort, i.spec.Name, name)
		}
		port = declared
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return nil, fmt.Errorf("container: dialling %s on %d: %w", i.spec.Name, port, err)
	}
	return conn, nil
}

// freePort asks the kernel for a port and gives it back. Something else may take it
// before the container binds, and there is no way to read an ephemeral port back
// from either backend; a start that loses the port chooses again (startAttempts).
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

	// reachable is the host port each declared port is answered on, by the
	// name its author gave it. Empty for a workload that declared none, which
	// is reached on `port` alone.
	reachable map[string]int

	// lambda is this instance's Lambda Runtime API, always bound: whichever signal
	// arrives first decides what the image is (lambda.go).
	lambda *lambdaAPI

	// polls is set when the runtime client polled before the container
	// listened, which is what makes this a Lambda image rather than a server.
	polls bool

	vaulted

	mu     sync.Mutex
	closed bool
}

var _ invoke.Instance = (*instance)(nil)

func (i *instance) addr() string { return "127.0.0.1:" + strconv.Itoa(i.port) }

// waitReady waits for the port to accept or the Lambda client to poll, whichever
// comes first, and watches for the container dying so a crash on boot does not
// burn the whole ReadyTimeout.
func (i *instance) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(i.eng.opts.readyTimeout)
	t := time.NewTicker(i.eng.opts.readyInterval)
	defer t.Stop()

	for {
		select {
		case <-i.lambda.polled:
			// A LAMBDA IMAGE, and it is ready the moment it asks for work.
			i.polls = true
			return nil
		default:
		}

		if i.ready(ctx) {
			return nil
		}

		if info, ierr := i.eng.opts.backend.Inspect(ctx, i.id); ierr == nil && info != nil && exited(info) {
			return fmt.Errorf("%w before it listened: status %q, exit code %d",
				ErrExited, info.Status, info.ExitCode)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-i.lambda.polled:
			i.polls = true
			return nil
		case <-t.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("%w on %s within %s, and nothing polled the lambda runtime api either",
					ErrNotReady, i.addr(), i.eng.opts.readyTimeout)
			}
		}
	}
}

// ready is one readiness check: a connection accepted, and — when the workload
// declared a probe — its path answered 2xx or 3xx on the port it named.
func (i *instance) ready(ctx context.Context) bool {
	addr := i.addr()
	probe := i.spec.Readiness
	if probe != nil && probe.Port != "" {
		port, ok := i.reachable[probe.Port]
		if !ok {
			return false
		}
		addr = "127.0.0.1:" + strconv.Itoa(port)
	}
	conn, err := i.eng.opts.dial(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	conn.Close()
	if probe == nil || probe.Path == "" {
		return true
	}

	check, cancel := context.WithTimeout(ctx, i.eng.opts.readyInterval*10+time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(check, http.MethodGet, "http://"+addr+probe.Path, nil)
	if err != nil {
		return false
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode >= 200 && res.StatusCode < 400
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

	if i.polls {
		return i.serveLambda(ctx, req)
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

	// A client per request: the pool may retire this instance, and a pooled keep-alive
	// would outlive the container.
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

// Close stops and removes the container, removing even if stop failed; safe to
// call twice.
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

	// THE RUNTIME API GOES FIRST, so a client blocked on a long poll is
	// released rather than held while the container is stopped.
	apiErr := i.lambda.Close()
	stopErr := i.eng.opts.backend.Stop(ctx, i.id)
	removeErr := i.eng.opts.backend.Remove(ctx, i.id)
	return errors.Join(apiErr, stopErr, removeErr, i.closeVault())
}

// serveLambda renders the request as an event, hands it to the polling client,
// and reads back what the handler returned.
func (i *instance) serveLambda(ctx context.Context, req *invoke.Request) (*invoke.Response, error) {
	id := requestID()

	event, err := lambdaEvent(req, id)
	if err != nil {
		return nil, fmt.Errorf("container: rendering the event for %s: %w", i.spec.Name, err)
	}

	result, err := i.lambda.invoke(ctx, id, event)
	if err != nil {
		return nil, err
	}
	return lambdaResponse(result), nil
}

// mounts resolves every declared filesystem or refuses. A mount with no path is
// refused too.
func (e *Runtime) mounts(ctx context.Context, spec invoke.Spec) ([]core.VolumeMount, error) {
	mounted := spec.Mounted()
	if len(mounted) == 0 {
		return nil, nil
	}

	if e.opts.volumes == nil {
		where := make([]string, 0, len(mounted))
		for _, m := range mounted {
			at := m.Settings[settingMountPath]
			if at == "" {
				at = "an unnamed path"
			}
			where = append(where, m.Label+" at "+at)
		}
		sort.Strings(where)

		return nil, fmt.Errorf("%w: %s wants %s — started without it, everything written there is discarded with the container",
			ErrNoMounts, spec.Name, strings.Join(where, ", "))
	}

	out := make([]core.VolumeMount, 0, len(mounted))
	for _, m := range mounted {
		at := m.Settings[settingMountPath]
		if at == "" {
			return nil, fmt.Errorf("%w: %s mounts %s at no path", ErrNoMounts, spec.Name, m.Label)
		}
		if m.Refused != "" {
			return nil, fmt.Errorf("%w: %s mounts %s, and %s", ErrNoMounts, spec.Name, m.Label, m.Refused)
		}

		source, err := e.opts.volumes.Mount(ctx, m)
		if err != nil {
			return nil, fmt.Errorf("%w: %s mounts %s: %w", ErrNoMounts, spec.Name, m.Label, err)
		}

		// READ-ONLY UNLESS THE GRANT ASKED TO WRITE, which is the access
		// block's own distinction: `mount-ro` and `mount-rw` are two verbs and
		// serving the first as the second would widen a grant.
		out = append(out, core.VolumeMount{
			Source:      source,
			Destination: at,
			ReadOnly:    !m.Allows(opMountRW),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Destination < out[j].Destination })
	return out, nil
}

// The vocabulary's own names for what a mount entry says.
const (
	settingMountPath = "path"
	opMountRW        = "mount-rw"
)

// unserved warns about every other declared capability this tier lacks; each fails
// where it is used, so a warning at start is enough.
func (e *Runtime) unserved(spec invoke.Spec) {
	if e.opts.warn == nil {
		return
	}

	kinds := map[string]bool{}
	for _, g := range spec.Reaches {
		if g.Kind == invoke.MountKind || (g.Kind == secretKind && e.opts.secrets != nil) {
			continue
		}
		kinds[g.Kind] = true
	}
	if len(kinds) == 0 {
		return
	}

	names := make([]string, 0, len(kinds))
	for kind := range kinds {
		names = append(names, kind)
	}
	sort.Strings(names)

	e.opts.warn("container: %s declares access this tier does not serve (%s); "+
		"nothing is injected for them and the workload will fail where it uses one",
		spec.Name, strings.Join(names, ", "))
}

// egressGap warns that this tier cannot narrow a declared egress allowlist: a
// container gets the deny floor and then the whole public internet. Not a refusal,
// since the vocabulary allows the config and the floor is already strict; closing
// it means programming resolved addresses into the floor's nftables set, with DNS
// rebinding as the hazard.
func (e *Runtime) egressGap(spec invoke.Spec) {
	if len(spec.Egress) == 0 || e.opts.warn == nil {
		return
	}
	e.opts.warn("container: %s declares an egress allowlist (%s) that this tier cannot narrow to; "+
		"it gets the node-wide deny floor and the public internet",
		spec.Name, strings.Join(spec.Egress, ", "))
}
