package js

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"warpjs/engine"

	"github.com/taubyte/tau/pkg/invoke"
)

// The URL a request arrives at, and what a non-HTTP invocation arrives as:
// the wasm tier's standard world uses the same. The host is a placeholder: the
// gateway already matched the name, and a handler acting on it would be acting
// on a routing decision it cannot see.
const (
	origin        = "http://workload.invalid"
	defaultMethod = "POST"
)

// instance is one realm, serving one request at a time.
type instance struct {
	name    string
	rt      *engine.Runtime
	timeout time.Duration

	// serve is the handler, bound by the prelude; answer and settleWork are the
	// request's answer and its waitUntil work; guard races a promise against
	// a cancellation; reserve grows the window.
	serve      engine.Value
	answer     engine.Value
	settleWork engine.Value
	guard      engine.Value
	reserve    engine.Value

	// window is the buffer bytes cross through (see host.go), current the
	// request being served, seq its number, response what its handler
	// answered, and waits whether it handed anything to waitUntil.
	window   engine.Value
	current  *invoke.Request
	seq      int
	response *invoke.Response
	waits    bool

	// gate is held by whatever uses the realm: a request, the waitUntil work
	// it left running, or Close, which takes it for good. alarm is used under
	// it.
	gate  chan struct{}
	alarm alarm

	// life ends when Close starts, and ends whatever holds the gate with it.
	life    context.Context
	end     context.CancelFunc
	closing sync.Once

	// dead is why this instance serves nothing more. It is read and written
	// under the gate.
	dead error
}

var _ invoke.Instance = (*instance)(nil)

func (r *Runtime) instantiate(ctx context.Context, spec invoke.Spec, b *bundle, export string) (*instance, error) {
	memory := spec.Memory
	if memory <= 0 {
		memory = DefaultMemory
	}
	rt, err := newRealm(append(r.mode.options(), engine.WithMemory(int(memory)), engine.WithStack(stackFor(memory)))...)
	if err != nil {
		return nil, fmt.Errorf("%w: %s, with %d bytes: %w", ErrEngine, spec.Name, memory, err)
	}

	life, end := context.WithCancel(context.Background())
	i := &instance{name: spec.Name, rt: rt, timeout: timeoutOf(spec), gate: make(chan struct{}, 1), life: life, end: end}

	var stdout, stderr io.Writer
	if r.output != nil {
		stdout, stderr = r.output(spec)
	}

	deadline := i.deadlineOf(ctx)
	a := i.arm(deadline)
	run, stop := i.bound(ctx, deadline)
	err = facade(func() error {
		return rt.RunContext(run, func() error { return i.boot(r, b, export, stdout, stderr, run, deadline) })
	})
	stop()
	if a.disarm() && err == nil {
		err = engine.ErrTimeout
	}
	if err != nil {
		end()
		rt.Close()
		return nil, i.fault(err)
	}
	return i, nil
}

func newRealm(opts ...engine.Option) (rt *engine.Runtime, err error) {
	err = facade(func() error {
		rt, err = engine.New(opts...)
		return err
	})
	return rt, err
}

// boot runs the prelude, evaluates the entry and binds its handler.
func (i *instance) boot(r *Runtime, b *bundle, export string, stdout, stderr io.Writer, ctx context.Context, deadline time.Time) error {
	install, err := i.rt.RunProgram(r.prelude)
	if err != nil {
		return fmt.Errorf("js: running the prelude: %w", err)
	}
	host, err := i.hostObject(stdout, stderr)
	if err != nil {
		return err
	}
	glue, err := i.rt.Call(install, engine.Value{}, host)
	if err != nil {
		return fmt.Errorf("js: running the prelude: %w", err)
	}
	i.answer = glue.Get("answer")
	i.settleWork = glue.Get("settle")
	i.guard = glue.Get("guard")
	i.reserve = glue.Get("reserve")
	if r.realm != nil {
		if err := r.realm(i.rt); err != nil {
			return err
		}
	}

	i.rt.SetModuleHost(b.host())
	imported, err := i.rt.ImportModule("", b.entry, nil)
	if err != nil {
		return err
	}
	ns, err := i.wait(ctx, imported, deadline)
	if err != nil {
		return fmt.Errorf("evaluating %s: %w", b.entry, err)
	}

	bound, err := i.rt.Call(glue.Get("bind"), engine.Value{}, ns, i.rt.ToValue(export))
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrNoHandler, b.entry, err)
	}
	if bound.IsString() {
		return fmt.Errorf("%w: %s: %s", ErrNoHandler, b.entry, bound.String())
	}
	i.serve = bound
	return nil
}

// Serve hands one request to the handler. It answers when the handler's
// response settles; what the handler gave ctx.waitUntil keeps the instance
// checked out after that, until it settles or the deadline hits.
func (i *instance) Serve(ctx context.Context, req *invoke.Request) (*invoke.Response, error) {
	if err := i.enter(ctx); err != nil {
		return nil, err
	}
	deadline := i.deadlineOf(ctx)
	a := i.arm(deadline)

	resp, waiting, err := i.handle(ctx, req, deadline)
	if err != nil || !i.pending(waiting) {
		if err := i.leave(err, a); err != nil {
			return nil, err
		}
		return resp, nil
	}

	if done, ok := invoke.Linger(ctx); ok {
		go func() { done(i.settle(ctx, waiting, deadline, a) != nil) }()
		return resp, nil
	}
	// Driven directly, there is no slot to keep checked out, so the work
	// finishes before the answer goes back.
	if err := i.settle(ctx, waiting, deadline, a); err != nil {
		return nil, err
	}
	return resp, nil
}

func (i *instance) handle(ctx context.Context, req *invoke.Request, deadline time.Time) (resp *invoke.Response, waiting engine.Value, err error) {
	if req == nil {
		req = &invoke.Request{}
	}
	i.current, i.response, i.waits = req, nil, false
	i.seq++
	defer func() { i.current, i.response = nil, nil }()

	run, stop := i.bound(ctx, deadline)
	defer stop()
	err = facade(func() error {
		return i.rt.RunContext(run, func() error {
			// The body goes into the window before the handler runs, and
			// failing to make room for it is the arena refusing it.
			if err := i.fit(req.Body); err != nil {
				return fmt.Errorf("%w: the request does not fit: %w", ErrOutOfMemory, err)
			}
			// The answer reaches Go through respond, before the call returns
			// unless the handler waits on the host. Only then is its promise
			// fetched, since every Value Go holds is pinned until Go collects.
			if _, err := i.rt.Call(i.serve, engine.Value{}); err != nil {
				return err
			}
			if i.response == nil {
				answer, err := i.rt.Call(i.answer, engine.Value{})
				if err != nil {
					return err
				}
				if _, err := i.wait(run, answer, deadline); err != nil {
					return err
				}
			}
			if resp = i.response; resp == nil {
				return errors.New("js: the prelude answered nothing")
			}
			if i.waits {
				waiting, err = i.rt.Call(i.settleWork, engine.Value{})
			}
			return err
		})
	})
	return resp, waiting, err
}

// settle waits for the waitUntil work a request left. It outlives the caller
// but not the deadline, and not Close.
func (i *instance) settle(ctx context.Context, waiting engine.Value, deadline time.Time, a *alarm) error {
	run, stop := i.bound(context.WithoutCancel(ctx), deadline)
	defer stop()
	err := facade(func() error {
		return i.rt.RunContext(run, func() error {
			_, err := i.wait(run, waiting, deadline)
			return err
		})
	})
	return i.leave(err, a)
}

// pairs is headers as the flat name/value list the prelude reads, names
// lower-cased and sorted so one request always reads the same way.
func pairs(headers map[string][]string) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	flat := make([]string, 0, 2*len(names))
	for _, name := range names {
		for _, value := range headers[name] {
			flat = append(flat, strings.ToLower(name), value)
		}
	}
	return flat
}

// wait runs posted work and promise jobs until p settles, the deadline passes
// or ctx ends.
func (i *instance) wait(ctx context.Context, p engine.Value, deadline time.Time) (engine.Value, error) {
	state, result, ok := i.rt.PromiseState(p)
	if !ok {
		return p, nil
	}
	if state == engine.PromiseFulfilled {
		return result, nil
	}

	// Await ends only when its promise settles, so a cancellation has to
	// settle one: p is raced against a rejection posted when ctx ends.
	guarded, err := i.rt.Call(i.guard, engine.Value{}, p)
	if err != nil {
		return engine.Value{}, err
	}
	cancel := guarded.Get("1")
	stop := context.AfterFunc(ctx, func() {
		i.rt.Post(func() {
			// Outside ctx, which is over and would interrupt the very call
			// that ends the wait. If it cannot run, the wait still ends at its
			// deadline.
			i.rt.RunContext(context.Background(), func() error {
				_, err := cancel.Call(engine.Value{})
				return err
			})
		})
	})
	defer stop()

	v, err := i.rt.Await(guarded.Get("0"), deadline)
	if err != nil && ctx.Err() != nil {
		return engine.Value{}, ctx.Err()
	}
	return v, err
}

func (i *instance) pending(v engine.Value) bool {
	state, _, ok := i.rt.PromiseState(v)
	return ok && state == engine.PromisePending
}

// enter takes the gate: one request at a time, whatever concurrency the spec
// asks for, since a realm is used from one goroutine at a time.
func (i *instance) enter(ctx context.Context) error {
	select {
	case i.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-i.life.Done():
		return fmt.Errorf("%w: %s is closed", invoke.ErrNotServable, i.name)
	}
	if i.life.Err() != nil {
		<-i.gate
		return fmt.Errorf("%w: %s is closed", invoke.ErrNotServable, i.name)
	}
	if i.dead != nil {
		<-i.gate
		return fmt.Errorf("%w: %w", invoke.ErrNotServable, i.dead)
	}
	return nil
}

// leave gives the gate back. A call that failed, or whose alarm went off,
// retires the instance: a realm interrupted mid-mutation is not reused, and an
// interrupt that lands after the call would kill the next one.
func (i *instance) leave(err error, a *alarm) error {
	if a.disarm() && err == nil {
		err = engine.ErrTimeout
	}
	if err != nil {
		err = i.fault(err)
		i.dead = err
	}
	<-i.gate
	return err
}

// fault names what ended a call.
func (i *instance) fault(err error) error {
	switch {
	case errors.Is(err, ErrOutOfMemory):
		return fmt.Errorf("%s: %w", i.name, err)
	case errors.Is(err, engine.ErrOutOfMemory):
		return fmt.Errorf("%w: %s: %w", ErrOutOfMemory, i.name, err)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("js: %s: %w", i.name, err)
	case errors.Is(err, engine.ErrTimeout), errors.Is(err, engine.ErrInterrupted), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %s: %w", ErrDeadline, i.name, err)
	}
	return fmt.Errorf("%s: %w", i.name, err)
}

// deadlineOf is the caller's deadline or the spec's timeout, whichever is
// sooner.
func (i *instance) deadlineOf(ctx context.Context) time.Time {
	deadline := time.Now().Add(i.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		return d
	}
	return deadline
}

// bound is ctx ended by the deadline and by Close.
func (i *instance) bound(ctx context.Context, deadline time.Time) (context.Context, func()) {
	ctx, cancel := context.WithDeadline(ctx, deadline)
	stop := context.AfterFunc(i.life, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

// alarm interrupts the realm at the deadline, whatever is running in it. An
// instance has one, armed again for every call: an alarm that went off
// retires the instance, so it never goes off twice.
type alarm struct {
	timer *time.Timer
	fired chan struct{}
}

func (i *instance) arm(deadline time.Time) *alarm {
	a := &i.alarm
	if a.timer != nil {
		a.timer.Reset(time.Until(deadline))
		return a
	}
	a.fired = make(chan struct{})
	a.timer = time.AfterFunc(time.Until(deadline), func() {
		i.rt.Interrupt()
		close(a.fired)
	})
	return a
}

// disarm reports whether the alarm went off, and once it has, waits for its
// interrupt to land, so nothing reaches the realm after the gate is given back.
func (a *alarm) disarm() bool {
	if a.timer.Stop() {
		return false
	}
	<-a.fired
	return true
}

// Close ends whatever is running, waits for it, and closes the realm. It is
// safe to call more than once.
func (i *instance) Close() error {
	i.closing.Do(func() {
		i.end()
		i.gate <- struct{}{}
		i.rt.Close()
	})
	return nil
}
