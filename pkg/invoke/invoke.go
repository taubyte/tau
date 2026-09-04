// Package invoke is the seam a compute runtime plugs into, and the invocation
// model every runtime shares.
//
// v2 runs code on more than one tier on purpose: a wasm component is the
// differentiator, a container is what runs the code people already have (and
// what makes an AWS Lambda image importable at all), and warpjs is a third.
// The config vocabulary already chooses between them — `Runtimes(...)` on a kind
// and worker's `runtime` field, both in pkg/tcc/taubyte/v2/schema — so what is
// missing is the runtime side of that choice.
//
// # What an engine is, and what it is not
//
// AN ENGINE STARTS THINGS AND SERVES REQUESTS. It does not decide when to start
// them, how many to keep, when to stop them, or how many requests one may take
// at a time. All of that is identical across wasm, containers and warpjs, and
// all of it is where the interesting failures live — so it is here, once, in
// Pool, tested against a fake engine that starts in nanoseconds.
//
// That split is the whole point. An engine implementation is then small enough
// to be obviously correct: start this artifact, hand it a request, get a
// response, stop it.
//
// # A revision is part of the identity, not a property of it
//
// Two Specs that differ only by Revision are different things and never share
// an instance. This is what makes a deploy a deploy: the new revision's
// instances are started fresh, the old revision's drain and go away on their
// own idle timer, and nothing has to reach into a running process and swap its
// code. It also means a rollback is warm if the old revision has not yet aged
// out, which is the case that matters most.
package invoke

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

var (
	ErrNoRuntimes  = errors.New("invoke: a pool with no runtimes can run nothing")
	ErrNoRuntime   = errors.New("engine: no engine registered for this spec")
	ErrPoolClosed  = errors.New("engine: pool is closed")
	ErrNoRevision  = errors.New("engine: a spec with no revision cannot be told apart from the next deploy")
	ErrNoName      = errors.New("engine: a spec with no name identifies nothing")
	ErrAtCapacity  = errors.New("engine: at capacity")
	ErrNotServable = errors.New("engine: instance is no longer servable")
)

// Spec is what to run. Two specs that are equal run the same code, and two that
// differ in any field are different things.
type Spec struct {
	// Runtime names which tier runs this — the same strings the config
	// vocabulary uses ("wasm", "container"). It is a string rather than an
	// enum because a third engine must be a registration, not a schema change
	// in this package.
	Runtime string

	// Name is what is being run, scoped by whoever composes this (a project,
	// an environment and a resource name flattened by the caller). This
	// package does not parse it.
	Name string

	// Revision is the immutable build identity. See the package doc: it is
	// part of identity, so a new build never shares an instance with the old.
	Revision string

	// Concurrency is how many requests one instance may serve at once.
	//
	// ONE IS THE LAMBDA MODEL AND IT IS THE DEFAULT FOR A REASON. Code written
	// for a serverless platform is routinely not safe to enter twice, and a
	// platform that quietly shares an instance turns that into corruption
	// rather than a queue. A caller that knows its code is reentrant says so.
	Concurrency int

	// Timeout bounds one request. Zero means the pool's default.
	Timeout time.Duration

	// Memory is the hard limit in bytes, from the resource's
	// `execution/memory`. Zero means the runtime's own default.
	//
	// IT IS HERE AND NOT IN A PER-TIER STRUCT because every tier has one: a
	// container gets a cgroup limit, a wasm instance gets a memory ceiling,
	// and a tier that ignored it would be a tier where a runaway workload is
	// the node's problem instead of its own.
	Memory int64

	// Artifact names what to run: an image reference, a component's cid — the
	// string the tier named by Runtime knows how to read.
	//
	// IT IS OPAQUE HERE ON PURPOSE. This package has no opinion about what an
	// image reference looks like, and a Spec that carried a parsed one would
	// be this package holding a registry's grammar.
	Artifact string

	// Env is what the workload is given. A tier may add to it — the container
	// tier sets PORT — and may not have it overridden.
	Env map[string]string
}

func (s Spec) validate() error {
	if s.Name == "" {
		return ErrNoName
	}
	if s.Revision == "" {
		return ErrNoRevision
	}
	if s.Runtime == "" {
		return fmt.Errorf("engine: %q names no engine", s.Name)
	}
	if s.Concurrency < 0 {
		return fmt.Errorf("engine: %q: negative concurrency", s.Name)
	}
	return nil
}

// key is the pool's identity for a spec. Concurrency and Timeout are excluded:
// they are how the pool treats an instance, not what the instance IS, and
// changing a timeout should not force a cold start.
func (s Spec) key() string { return s.Runtime + "\x00" + s.Name + "\x00" + s.Revision }

// Request is one call into an instance.
//
// It is deliberately not net/http: a pubsub delivery and a p2p call are also
// invocations, and an engine that could only be reached over HTTP would make
// the other two second-class. The caller maps its own transport onto this.
type Request struct {
	// Method, Path and Headers carry an HTTP-shaped call. They are empty for
	// invocations that are not.
	Method  string
	Path    string
	Headers map[string][]string

	// Body is the payload. It may be nil.
	Body []byte
}

// Response is what came back.
type Response struct {
	Status  int
	Headers map[string][]string
	Body    []byte
}

// Instance is one started, runnable unit of code.
//
// Serve MUST be safe to call from as many goroutines as the Spec's Concurrency
// allows, and no more — the pool guarantees it will not exceed that.
type Instance interface {
	Serve(ctx context.Context, req *Request) (*Response, error)
	io.Closer
}

// Runtime starts instances. That is the entire contract, and it is small on
// purpose: everything else an engine might be asked to do is the same for all
// of them and lives in Pool.
type Runtime interface {
	// Name is the string the config vocabulary uses for this tier.
	Name() string

	// Start brings up one instance of spec. It is called on the request path
	// when nothing warm is available, so its latency IS the cold start.
	Start(ctx context.Context, spec Spec) (Instance, error)
}
