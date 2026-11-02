// Package invoke is the seam a compute runtime plugs into, and the invocation
// model every runtime shares (wasm components, containers, warpjs).
//
// An engine starts things and serves requests. When to start them, how many to
// keep, when to stop and how many requests one takes at a time are the same for
// every tier and live once in Pool, tested against a fake engine.
//
// A revision is part of identity: specs differing only by Revision never share an
// instance, so a deploy starts fresh instances while the old ones idle out, and a
// rollback is warm if the old revision has not aged out.
package invoke

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/taubyte/tau/pkg/specs"
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

// Spec is what to run; specs differing in any field are different things.
type Spec struct {
	// Runtime names the tier, as the config vocabulary spells it; a string so a new
	// engine is a registration.
	Runtime string

	// Name is what is run, scoped and flattened by the composer; not parsed here.
	Name string

	// Project, Environment, Kind, Label and Scope are the workload's identity as its
	// configuration spells it (Scope outermost first), what
	// `taubyte:workload/identity` answers with. Not Name split up; a tier lacking them
	// answers empty.
	Project     string
	Environment string
	Kind        string
	Label       string
	Scope       []string

	// Revision is the compiled configuration this spec was read from, immutable and
	// part of identity. A code push keeps it; Artifact says which build runs.
	Revision string

	// Concurrency is how many requests one instance may serve at once. One, the Lambda
	// model, by default: serverless code is often not reentrant.
	Concurrency int

	// Timeout bounds one request. Zero means the pool's default.
	Timeout time.Duration

	// CPU is a ceiling in millicores: 1000 is one core. Zero is no ceiling.
	CPU int64

	// Readiness, when set, is how a tier knows an instance may be given work:
	// an HTTP GET on Path, on the port named Port (empty: the one port it was
	// given), answered 2xx or 3xx. Absent, accepting a connection is ready.
	Readiness *Probe

	// Completes is a workload that runs to completion: each request starts it and its
	// exit is the answer. Carried from schema.Completes so no tier names a kind.
	Completes bool

	// Ports are what this workload binds inside its container, by name
	// (`ports: {pg: {number: 5432}}`). Empty means the $PORT contract.
	Ports map[string]int

	// Memory is the hard limit in bytes from `execution/memory`; zero is the runtime's
	// default. Every tier has one.
	Memory int64

	// Artifact names what to run (an image reference, a component cid), opaque here.
	Artifact string

	// Egress is where this workload may reach, as declared. Empty is the node-wide
	// floor alone, not "nowhere": absent asks for no narrowing (pkg/invoke/wasm/egress.go).
	Egress []string

	// Settings are tier-specific values read off the resource. A changed setting is a
	// changed compiled object and so a new revision, which is why a map is safe here.
	Settings map[string]string

	// Env is what the workload is given. A tier may add to it — the container
	// tier sets PORT — and may not have it overridden.
	Env map[string]string

	// Builtin is the platform program this workload runs (a workflow driver, an
	// agent's loop); it has no artifact.
	Builtin string

	// Reaches is the workload's access block, one Grant per target across all kinds.
	// It is the whole authorization: a tier refuses a target or op not here.
	Reaches []Grant
}

// Grant is one entry of a resource's access block.
type Grant struct {
	// Kind is the vocabulary's own kind name: "cache", "database", "storage".
	Kind string

	// Label is what the author wrote and the guest passes, not the target's id, so one
	// component is portable across environments.
	Label string

	// Handle is opaque here, exactly as Artifact is: the string whatever backs
	// this kind understands as "which one". A composer builds it; this package
	// has no opinion about its shape.
	Handle string

	// Ops are the verbs the resource asked for, as the vocabulary spells them
	// — "get", "put", "delete", "list". The target decides what it actually
	// grants; this is what was requested.
	Ops []string

	// Settings are what the target declared (a cache's `keys/ttl`), read by the
	// composer so the backing never needs the register on the request path. Keys are
	// the vocabulary's paths joined with "/".
	Settings map[string]string

	// Refused, when set, is why this cloud will not serve this target; every op fails
	// with it. Access degrades per target rather than dropping the entry (a "no such
	// cache" typo) or the whole workload.
	Refused string
}

// MountKind is the kind whose grant is a filesystem; both tiers must recognise it,
// since going without one loses data.
const MountKind = specs.KindVolume

// Mounted is every filesystem this workload declared it would have.
func (s Spec) Mounted() []Grant {
	var out []Grant
	for _, g := range s.Reaches {
		if g.Kind == MountKind {
			out = append(out, g)
		}
	}
	return out
}

// Number is a count setting; absent or unparseable is zero, which every backing
// reads as no limit rather than a refusal.
func (g Grant) Number(setting string) int64 {
	n, err := strconv.ParseInt(g.Settings[setting], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// Allows reports whether this grant carries a verb. A refused grant allows
// nothing, whatever its ops say.
func (g Grant) Allows(op string) bool {
	if g.Refused != "" {
		return false
	}
	for _, have := range g.Ops {
		if have == op {
			return true
		}
	}
	return false
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

// key is the pool's identity for a spec. The artifact is in it beside the revision:
// a code push publishes a new artifact under an unchanged configuration, and an
// instance of the old code must not answer for the new. Concurrency and Timeout are
// excluded: they are how the pool treats an instance, not what the instance IS, and
// changing a timeout should not force a cold start.
func (s Spec) key() specKey { return specKey{s.Runtime, s.Name, s.Revision, s.Artifact} }

type specKey struct{ runtime, name, revision, artifact string }

// Request is one call into an instance; not net/http, since pubsub deliveries and
// p2p calls are invocations too.
type Request struct {
	// Method, Path and Headers carry an HTTP-shaped call. They are empty for
	// invocations that are not.
	Method  string
	Path    string
	Headers map[string][]string

	// Body is the payload. It may be nil.
	Body []byte
}

// Probe is an HTTP readiness check: the path, and the declared port by name.
type Probe struct {
	Path string
	Port string
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

// Dials is an Instance that can hand out a connection to one of its ports, for an
// L4 proxy. Only a container can; an empty name is the single $PORT.
type Dials interface {
	Dial(ctx context.Context, port string) (net.Conn, error)
}

// Transforms is an Instance that can also be driven as a gateway transformer; a
// separate interface since a container cannot.
type Transforms interface {
	Transform(ctx context.Context, req *Request) (*Outcome, error)
}

// Routes is an Instance that can also be driven as a gateway router.
type Routes interface {
	Route(ctx context.Context, req *Request) (*Routing, error)
}

// Destination is where a router sent a request: kind, namespace and name; an empty
// Namespace resolves outward from the asking resource. Running it stays the
// gateway's.
type Destination struct {
	Kind      string
	Namespace string
	Name      string
}

// Routing is what a router decided; exactly one field is set. None is a decision,
// unlike a router that could not run.
type Routing struct {
	To     *Destination
	Answer *Response
	None   string
}

// Directs is an Instance that can also be driven as a proxy router, handed a source
// address and bytes.
type Directs interface {
	Direct(ctx context.Context, conn *Connection, peeked []byte) (*Direction, error)
}

// Shapes is an Instance that can also be driven as a proxy transformer, a chunk at
// a time.
type Shapes interface {
	Shape(ctx context.Context, s *Stream) (*Shaping, error)
}

// Connection is what is known about a connection before forwarding: a peer address
// and then bytes.
type Connection struct {
	// Source is the peer address the client dialed from, never anything it sent.
	Source string

	// Host, Protocol and Transport are the name this proxy answers on and how it was
	// looked up, since one router can serve several proxies.
	Host      string
	Protocol  string
	Transport string

	// ServerName is the TLS SNI when this proxy terminated TLS, empty otherwise.
	ServerName string
}

// Endpoint is where a proxy router sent a connection: a Destination plus the port.
type Endpoint struct {
	Kind      string
	Namespace string
	Name      string
	Port      string
}

// Direction is what a proxy router decided; exactly one field is set. More asks for
// that many bytes; Refused closes the connection, the reason going to the operator.
type Direction struct {
	To      *Endpoint
	More    uint32
	Refused string
}

// Enough says the router asked for bytes it has not been shown yet. A `more`
// that wants no more than has already arrived is a router that will never
// decide, and the host half refuses it rather than spinning on one prefix.
func (d *Direction) Enough(seen int) bool { return int(d.More) <= seen }

// Way is which direction bytes are travelling.
type Way uint8

const (
	// Inbound is from the client toward the workload.
	Inbound Way = iota
	// Outbound is from the workload back to the client.
	Outbound
)

// Stream is one chunk for a proxy transformer to shape.
type Stream struct {
	Conn *Connection
	Way  Way

	// State is what the transformer returned last time for this connection and
	// direction, carried by the host so instances need not be pinned per connection.
	State []byte

	// Chunk is what just arrived. Empty when Last is set and nothing arrived
	// with the close.
	Chunk []byte

	// Last says the other end is done writing this direction. It is the
	// transformer's final chance to emit what it has been holding.
	Last bool
}

// Shaping is what a proxy transformer decided; exactly one is set. Refused is a
// decision; a transformer that could not run follows its entry's `fail:`.
type Shaping struct {
	Pass    *Shaped
	Refused string
}

// Shaped is what a proxy transformer passed on and the state it wants back.
type Shaped struct {
	// Out is the bytes to send on. It may be EMPTY — a transformer buffering a
	// frame passes nothing until it has one — and it may be longer or shorter
	// than the chunk it was given.
	Out []byte

	// State is carried by the host and handed back next time. Returning it
	// unchanged keeps it; returning nothing forgets it.
	State []byte
}

// Tool is something a model may ask to have run.
type Tool struct {
	Name        string
	Description string

	// Schema is JSON Schema for the arguments as text: it is recursive, and the world
	// has no recursive records.
	Schema string
}

// ToolCall is the model asking for one.
type ToolCall struct {
	// ID is the provider's own, echoed rather than invented: a model that
	// asked for two at once is told which answer is which by this alone.
	ID   string
	Name string

	// Arguments is model-produced JSON, not validated against the schema.
	Arguments string
}

// Message is one turn of a conversation with a model.
type Message struct {
	// Role is "system", "user", "assistant" or "tool". A format that spells
	// them differently is the backing's problem, not the caller's.
	Role    string
	Content string

	// ToolCallID is set on a `tool` turn: which call this answers.
	ToolCallID string

	// ToolCalls is set on an assistant turn the model produced. Handing it
	// back is what makes a conversation with tools in it resumable.
	ToolCalls []ToolCall
}

// Inference is one call to a model. Temperature and MaxTokens are pointers because
// absent and zero differ.
type Inference struct {
	Messages    []Message
	Temperature *float64
	MaxTokens   *uint32

	// Stop is ADDED to what the resource declares rather than replacing it: a
	// stop sequence an author wrote is a rule about what this model may emit,
	// and a caller does not lift it.
	Stop []string

	// Tools is what the model may ask to have run; the caller runs them.
	Tools []Tool
}

// Usage is what one call cost. The caller is the only party that can attribute
// it to a feature — tau knows the workload and not what it was for.
type Usage struct {
	InputTokens  uint32
	OutputTokens uint32
}

// Answer is what a model said.
type Answer struct {
	Content string

	// StopReason is "stop" when it finished, "length" when it hit the ceiling,
	// "tool-calls" when it wants one run, or whatever else the provider said. A
	// caller that needs to know whether the answer is complete reads this
	// rather than guessing.
	StopReason string

	// ToolCalls is what it wants run before it can answer. A caller that sent
	// no tools never sees one.
	ToolCalls []ToolCall

	Usage Usage
}

// Outcome is what a transformer decided: carry on (possibly changed) or answer
// here. Exactly one is set, so a legitimate 200 answer is unambiguous.
type Outcome struct {
	Forward *Request
	Answer  *Response
}

// Admission is what a route's guard decided, kept in this package so the gateway
// need not import a runtime. Granted is every op the caller gets; empty is a
// decided refusal. Denied is a refusal to decide, its reason for operators only.
type Admission struct {
	Granted []string
	Denied  string
}

// Question is one DNS question to a zone's resolver: an absolute, lower-cased name
// and the type as text ("A", "SRV"), so unknown types can still be asked.
type Question struct {
	Name string
	Type string
}

// Resolution is what a zone's resolver answered, with DNS's three answers:
//
//	Exists=true, Records non-empty   the records
//	Exists=true, Records empty       NOERROR, nothing of that type
//	Exists=false                     NXDOMAIN
//
// Records are zone-file lines relative to the apex, as the file arm holds them.
type Resolution struct {
	Records []string
	Exists  bool
}

// Runtime starts instances; everything else an engine might do lives in Pool.
type Runtime interface {
	// Name is the string the config vocabulary uses for this tier.
	Name() string

	// Start brings up one instance of spec. It is called on the request path
	// when nothing warm is available, so its latency IS the cold start.
	Start(ctx context.Context, spec Spec) (Instance, error)
}
