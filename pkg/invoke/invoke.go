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
	"net"
	"strconv"
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

	// Project, Environment, Kind, Label and Scope are WHAT THIS WORKLOAD IS, as
	// its own configuration spells it — the deployment, the vocabulary's kind,
	// the name its author wrote, and the namespaces it lives in outermost
	// first.
	//
	// THEY ARE NOT Name SPLIT UP. Name is an id joined for logging and cannot
	// answer the authored name or the scope at all, and a consumer parsing it
	// back apart would be reading a format nobody promised. They are what
	// `taubyte:workload/identity` answers with, and a tier that has none of them
	// answers empty rather than guessing.
	Project     string
	Environment string
	Kind        string
	Label       string
	Scope       []string

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

	// Ports are what this workload binds INSIDE its container, by the name its
	// author gave them — `ports: {pg: {number: 5432}}`.
	//
	// A CONTAINER THAT BINDS A FIXED PORT IS THE ORDINARY CASE AND IT DID NOT
	// WORK. The tier allocated one ephemeral port and told the workload about it
	// in $PORT, which serves a program written to be told where to listen and
	// serves nothing else: postgres binds 5432 because that is what postgres
	// does, and mapping an ephemeral host port to the same ephemeral number
	// reached nothing at all. Every image anybody would import — a database, a
	// cache, an off-the-shelf server — is in that second group.
	//
	// EMPTY MEANS THE $PORT CONTRACT, which is still right for a workload built
	// for this platform: it is told a number and binds it. The two are different
	// products and the author picks by declaring ports or not.
	Ports map[string]int

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

	// Egress is where this workload may reach on the network, as the resource
	// declared it.
	//
	// IT IS HERE AND NOT IN Settings because every tier that runs code has one,
	// which is the same test Memory passes: a container gets a firewall, a wasm
	// guest gets an HTTP client that refuses everything else, and a tier that
	// ignored it would be a tier where a workload's reach is the node's problem
	// instead of its own.
	//
	// EMPTY IS THE NODE-WIDE FLOOR ALONE, AND NOT "NOWHERE". The vocabulary
	// calls this "the per-workload allowlist of hosts a workload may reach,
	// layered on top of the node-wide deny floor (pkg/netguard) which it can
	// never widen... absent means the floor alone" — so a resource that named
	// nothing asked for no NARROWING, not for isolation.
	//
	// THIS COMMENT SAID THE OPPOSITE and the tier that believed it refused
	// every function that calls an API, with a message about a list its author
	// never knew to write. The rule lives in pkg/invoke/wasm/egress.go; this is
	// the field it reads.
	Egress []string

	// Settings are the tier-specific values the composer read off the resource.
	//
	// EVERYTHING ELSE IN THIS STRUCT IS SOMETHING EVERY TIER HAS — a memory
	// ceiling, a timeout, an artifact — and this is what the ones that are not
	// go in. A website's serve mode is meaningless to a container tier, so
	// putting it beside Memory would be inviting every tier to grow a field
	// nothing else reads.
	//
	// CHANGING ONE TAKES EFFECT LIKE A DEPLOY, and that falls out of Revision
	// rather than out of this field: a setting comes off the compiled resource,
	// so a different setting is a different compiled object and therefore a
	// different revision, which the key already separates. Nothing here has to
	// be comparable, which is why a map is safe to put in a Spec at all.
	Settings map[string]string

	// Env is what the workload is given. A tier may add to it — the container
	// tier sets PORT — and may not have it overridden.
	Env map[string]string

	// Builtin is the platform program this workload runs, empty for ordinary
	// code.
	//
	// IT IS NOT AN ARTIFACT AND HAS NONE. `source: {kind: builtin}` says the
	// program is one the runtime itself ships — a workflow step's driver, an
	// agent's loop — so there is nothing for a build service to have produced
	// and nothing for a store to hold. A resource carrying one that reached the
	// artifact seam would be asked for the output of a build that never
	// happened.
	Builtin string

	// Reaches is what this workload declared it may touch: the compiled
	// `cache:`, `database:`, `storage:` blocks, one entry per target.
	//
	// ONE FIELD RATHER THAN ONE PER KIND. The vocabulary has seventeen kinds
	// and the access block is keyed by all of them, so a field each would be a
	// struct that grows with the schema — and a tier that runs code cares about
	// exactly the kinds it implements and should filter, not be handed a
	// pre-sorted struct with fifteen empty members.
	//
	// IT IS THE GRANT AND THERE IS NO SECOND PLACE TO WIDEN IT. A tier
	// refusing a target that is not here, and an op that is not on it, is the
	// whole of the authorization a capability gets — the configuration said it
	// and the compiled object carries it.
	Reaches []Grant
}

// Grant is one entry of a resource's access block.
type Grant struct {
	// Kind is the vocabulary's own kind name: "cache", "database", "storage".
	Kind string

	// Label is what the author wrote and what the guest passes. It is not the
	// target's id, and that is what makes one component portable between
	// environments: staging's `sessions` and production's are different caches
	// and the same code.
	Label string

	// Handle is opaque here, exactly as Artifact is: the string whatever backs
	// this kind understands as "which one". A composer builds it; this package
	// has no opinion about its shape.
	Handle string

	// Ops are the verbs the resource asked for, as the vocabulary spells them
	// — "get", "put", "delete", "list". The target decides what it actually
	// grants; this is what was requested.
	Ops []string

	// Settings are what the TARGET declared, as the composer read them off it
	// — a cache's `keys/ttl` and `keys/size`, a storage's quota. They are here
	// rather than looked up by whatever backs the kind, because the composer
	// has the compiled project open and the backing does not: a store that had
	// to resolve a handle back into a resource would need the register on the
	// request path.
	//
	// THE KEYS ARE THE VOCABULARY'S OWN PATHS, joined with "/", so a reader
	// finds the attribute by searching the schema for the string.
	Settings map[string]string

	// Refused, when set, is why this cloud will not serve this target — and
	// every op on it fails with this sentence.
	//
	// IT IS A REFUSED GRANT AND NOT A MISSING ONE, and the difference is the
	// whole reason the field exists. A composer that dropped the entry would
	// tell a guest "no such cache", which reads as a typo; one that dropped
	// the WORKLOAD would take a whole resource down because one of the things
	// it reaches cannot be served. Access is declared per target, so it
	// degrades per target, and the author is told which and why at the call
	// that needed it.
	Refused string
}

// Mounts is the kind whose grant is a FILESYSTEM rather than a call.
//
// It is named in this package because both tiers have to recognise it and
// neither serves it: a workload that declared one and did not get it writes to
// somewhere that disappears, which is the one unserved capability that loses
// data instead of failing.
const MountKind = "volume"

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

// Number is a setting the composer wrote as a count: a byte ceiling, an
// instance limit.
//
// ABSENT OR UNPARSEABLE IS ZERO, and every backing reads zero as "no limit"
// rather than as a refusal. The vocabulary makes these fields required, so a
// missing one means the composer did not read it — which is a bug to find in a
// log, where a store that refused every request over a field nobody wrote would
// be an outage.
//
// IT IS HERE AND NOT IN EACH BACKING because four of them had spelled the same
// strconv.ParseInt with the same fallback, one per kind, and a fifth would have
// too. Reading a setting is this type's own job: it is the one that says what
// the keys are.
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

// Dials is an Instance that can hand out a CONNECTION to one of its ports.
//
// ONLY A CONTAINER CAN DO THIS. A component has no listening socket — it is
// entered by a call — so widening Instance would make the wasm tier carry a
// method that could only ever refuse. It is the seam an L4 proxy needs, and the
// reason it is a connection rather than a request is that a proxy carries bytes
// it cannot parse, for as long as the client holds the socket open.
//
// An empty name is the single port a workload that declared none was given.
type Dials interface {
	Dial(ctx context.Context, port string) (net.Conn, error)
}

// Transforms is an Instance that can also be driven as a gateway transformer.
//
// IT IS A SECOND INTERFACE RATHER THAN A SECOND METHOD ON Instance, because a
// container tier genuinely cannot do this: it runs a process that answers
// requests, and there is no second export to call. Widening Instance would make
// every tier carry a method saying so.
type Transforms interface {
	Transform(ctx context.Context, req *Request) (*Outcome, error)
}

// Routes is an Instance that can also be driven as a gateway router.
//
// A SECOND INTERFACE FOR THE REASON Transforms IS ONE: only the component tier
// serves this world, and widening Instance would make every tier carry a method
// saying it cannot.
type Routes interface {
	Route(ctx context.Context, req *Request) (*Routing, error)
}

// Destination is where a router said a request should go: the vocabulary's own
// kind, the namespace holding it, and its name. An empty Namespace means the
// asking resource's own scope, then its parents.
//
// IT IS AN ADDRESS AND NOT A WORKLOAD. What runs it is the gateway's business,
// which is what keeps placement, metering, timeouts and the transformer chain in
// one place rather than in every router anybody writes.
type Destination struct {
	Kind      string
	Namespace string
	Name      string
}

// Routing is what a router decided. Exactly one field is set.
//
// None is a DECISION and not a failure: the router ran, understood the request,
// and says nothing here handles it. A router that could not be run is a
// different thing, because a routing table that cannot be read is not the same
// as a path that is not in it.
type Routing struct {
	To     *Destination
	Answer *Response
	None   string
}

// Directs is an Instance that can also be driven as a PROXY router.
//
// A SECOND INTERFACE FOR THE REASON Routes IS ONE, and a different one from it
// because the question is different: a gateway router is handed a parsed
// request, and a proxy router is handed a source address and some bytes —
// everything else about a connection is its to recognise.
type Directs interface {
	Direct(ctx context.Context, conn *Connection, peeked []byte) (*Direction, error)
}

// Shapes is an Instance that can also be driven as a PROXY transformer.
//
// IT TAKES A CHUNK AND NOT A REQUEST, because a stream has no point at which
// the whole of it exists to be transformed.
type Shapes interface {
	Shape(ctx context.Context, s *Stream) (*Shaping, error)
}

// Connection is what is known about a connection before a byte of it is
// forwarded, and it is all a proxy can offer: a raw socket carries a peer
// address and then bytes.
type Connection struct {
	// Source is where the client dialed from, as "ip:port". It is the PEER
	// address and never anything the client sent — a proxy has no forwarded
	// header to be lied to with, which is the one advantage of seeing no
	// protocol.
	Source string

	// Host, Protocol and Transport are the name this proxy answers on and how
	// a client looked it up. One router can serve several proxies, so it has
	// to be able to tell which connection it is deciding for.
	Host      string
	Protocol  string
	Transport string

	// ServerName is what the client asked for in its TLS handshake, when this
	// proxy terminated one. Empty when it did not, which is not the same as a
	// client that sent no SNI.
	ServerName string
}

// Endpoint is where a proxy router said a connection should go.
//
// IT IS Destination PLUS A PORT, and the extra field is why it is not that
// type: a workload binds several ports and a connection has to arrive at one of
// them, where an invocation has only the workload.
type Endpoint struct {
	Kind      string
	Namespace string
	Name      string
	Port      string
}

// Direction is what a proxy router decided. Exactly one field is set.
//
// More is a DECISION and not a failure: the router ran and says it cannot tell
// yet, which the proxy answers by waiting for that many bytes. Refused is a
// decision too — the connection is closed and the reason goes to the operator,
// because a raw socket has nowhere to write one.
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

	// State is what this transformer handed back the last time it was called
	// for this connection and this direction. Empty on the first chunk.
	//
	// THE HOST CARRIES IT so the instance does not have to. A transformer that
	// kept its own state would need one instance per connection, pinned to the
	// node the connection landed on for as long as it lives — which throws
	// placement away for the length of a psql session, and pays ~13ms to do it.
	State []byte

	// Chunk is what just arrived. Empty when Last is set and nothing arrived
	// with the close.
	Chunk []byte

	// Last says the other end is done writing this direction. It is the
	// transformer's final chance to emit what it has been holding.
	Last bool
}

// Shaping is what a proxy transformer decided. Exactly one is set.
//
// Refused is a DECISION and not a failure, exactly as a router's is: the
// transformer ran, read the bytes and says the conversation stops. A
// transformer that could not be RUN is a different thing, and what happens then
// is the author's — `fail: closed` or `fail: open`, per entry.
type Shaping struct {
	Pass    *Shaped
	Refused string
}

// Shaped is what a proxy transformer passed on, and what it wants handed back
// on the next chunk of the same connection and direction.
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

	// Schema is JSON Schema for the arguments, as JSON text. It is text
	// because JSON Schema is recursive and the world that carries it has no
	// recursive records — and every provider takes it as JSON anyway.
	Schema string
}

// ToolCall is the model asking for one.
type ToolCall struct {
	// ID is the provider's own, echoed rather than invented: a model that
	// asked for two at once is told which answer is which by this alone.
	ID   string
	Name string

	// Arguments is JSON the MODEL produced, which is not a source that can be
	// trusted to be well-formed. Nothing checks it against the schema — the
	// tool is the only party that knows what it needs.
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

// Inference is one call to a model, in the terms the vocabulary uses.
//
// Temperature and MaxTokens are pointers because ABSENT AND ZERO ARE DIFFERENT
// ANSWERS: temperature 0 is "as close to deterministic as a model gets" and is
// a value an author writes on purpose, where absent means whatever the resource
// declares and then whatever the provider defaults to.
type Inference struct {
	Messages    []Message
	Temperature *float64
	MaxTokens   *uint32

	// Stop is ADDED to what the resource declares rather than replacing it: a
	// stop sequence an author wrote is a rule about what this model may emit,
	// and a caller does not lift it.
	Stop []string

	// Tools is what the model may ask to have run. NOTHING HERE RUNS THEM: the
	// model answers with what it wants called and the caller does the calling,
	// which is what keeps a loop the caller's program rather than a behaviour
	// hidden inside a host function.
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

// Outcome is what a transformer decided: the request carries on, possibly
// changed, or it stops here and this is the answer.
//
// EXACTLY ONE IS SET. They are separate fields rather than a status convention
// because encoding "carry on" as a response would make every real status
// ambiguous — a transformer that legitimately wants to answer 200 could not.
type Outcome struct {
	Forward *Request
	Answer  *Response
}

// Admission is what a route's guard decided about one request.
//
// IT LIVES HERE BESIDE Outcome AND Routing because a gateway speaks these and
// not the tier's own types: the worker interface it calls through is written in
// this package, so a decision that stayed in pkg/invoke/wasm would make the
// edge import a runtime it never runs.
//
// `Granted` is every op the caller gets on whatever the route names, so AN
// EMPTY LIST IS A REFUSAL THAT WAS DECIDED — a bus declares `subscribe` and
// `publish`, and a grant of only `subscribe` is a browser that may listen and
// not send, which a yes/no cannot express. `Denied` is a refusal to DECIDE, and
// its reason is for an operator: what the caller sees is composed by a
// transformer, not by the guard, so a refusal that described the policy would
// describe somebody's deployment to anyone who asked.
type Admission struct {
	Granted []string
	Denied  string
}

// Question is one DNS question put to a zone's resolver.
//
// THE NAME IS ABSOLUTE AND LOWER-CASED, as it arrived on the wire, so a guest
// matching on it does not have to guess whether the apex is included or how it
// was capitalised. Type is the TEXT form — "A", "AAAA", "SRV" — and not the
// number, for the reason an answer is a line: a resolver wanting a type this
// system has not heard of can still be asked about it.
type Question struct {
	Name string
	Type string
}

// Resolution is what a zone's resolver answered.
//
// IT LIVES HERE BESIDE Admission for that type's reason: the DNS server speaks
// this and not the tier's own types, so an answer that stayed in
// pkg/invoke/wasm would make the server import a runtime it never runs.
//
// THE THREE ANSWERS DNS ACTUALLY HAS, which is why this is not a []string:
//
//	Exists=true, Records non-empty — here they are.
//	Exists=true, Records empty     — the name is in this zone and holds nothing
//	                                 of the type asked for. NOERROR with no
//	                                 records, which is what stops a client
//	                                 looking elsewhere for a name that is here.
//	Exists=false                   — no such name in this zone. NXDOMAIN.
//
// Collapsing the middle two would make a resolver unable to say the difference,
// and a server that confuses them misleads everything that asks: NXDOMAIN for a
// name that exists poisons a negative cache, and NOERROR for one that does not
// stops a client trying anywhere else.
//
// Records are zone-file lines relative to the zone's apex — "geo 60 IN A
// 203.0.113.9" — exactly as the file arm holds them, so both arms of a zone
// parse the same way.
type Resolution struct {
	Records []string
	Exists  bool
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
