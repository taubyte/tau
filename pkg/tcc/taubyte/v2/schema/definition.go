// Package schema is the tau v2 configuration vocabulary: the DSL declaration the
// generic tcc interpreter compiles. It is a sibling of taubyte/v1/schema, not a
// revision of it — v1 stays frozen so its parity oracle keeps applying while v2 is
// authored (.notes/v2/edges.md §9.1).
//
// What v2 changes, and why each is a deletion rather than an extension:
//
//   - The function trigger block is GONE. A function declares nothing about how it
//     is reached; reachability is an inbound edge authored on the domain, gateway,
//     stream, channel or cron that reaches it. Same for a website's
//     domains/paths. ".notes/v2/resources.md" §1.3, §1.5.
//   - smartops is deleted. Policy is `authorizers` (request-scoped, per-edge);
//     limits are platform-enforced at dispatch. "resources.md" Deletions.
//   - `libraries` -> `component`. `databases` stayed `database` and grew a `type`:
//     kv, document, sql and vector are data models of one kind rather than four kinds,
//     because what differs between them is the verb set and the engine, both of which
//     the kind can state.
//   - `notification.email` is deleted: inert since it was added, read nowhere.
//   - A `build:` stanza arrives, and it is the one ADDITION in this list. Nothing in
//     the vocabulary could say how a resource is made — `website` said how it is
//     SERVED and never how it is BUILT — so the answer lived in the author's own
//     repository, as `.taubyte/build.sh` plus `.taubyte/config.yaml`. That is a v1
//     shape v2 inherited rather than chose, and it is what made importing a project
//     from anywhere else mean writing a tau-shaped file into somebody's repository.
//     `image:` moved with it: a prebuilt artifact is a SOURCE, so it is a branch of
//     the switch that already answered where content comes from. See buildAttributes.
//   - The scope container is `namespaces`, not `applications`. `applications` was
//     v1's word for a scope; in v2 it is the container compute kind, and the scope
//     is named after what the access, quota and connection rules are written about.
//   - Every resource declares what it reaches as a block per target kind, at the top
//     level of its own document — `databases:`, `storages:`, `secrets:` — not under a
//     `uses:` wrapper. Declared or it does not exist. See verbs.go.
//
// Still absent, but no longer for want of a shape — DefineList arrived with the
// transformer chain, so what is left is the modelling: the `route` embed's per-path
// rules (headers/redirects/rewrites/status), `domain` DNS records, and per-edge
// modifiers (mount, timeout, retry, via).
package schema

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/taubyte/tau/pkg/tcc/interp"
)

// commonAttributes prepends the shared identity block and appends the access block.
// EVERY resource carries one, because it is a single construct between any two
// resources — there is no kind that may only be a target.
func commonAttributes(attrs ...*Attribute) []*Attribute {
	return append(identityAttributes(), attrs...)
}

// resource is every kind in this vocabulary: a plain resource — one flat file, id at
// the document root, scalars parsed, wire keys applied, indexed by name.
//
// Its access groups are attached afterwards, by attachAccess below, because they are
// generated from what every kind declares with Ops() and a kind cannot read the
// vocabulary while the vocabulary is still being built.
func resource(attrs []*Attribute, opts ...any) *Node {
	return DefineIter(attrs, opts...)
}

// attachAccess wires the two halves of access, once the vocabulary exists and can be
// read. It mutates the same nodes the namespace levels share, so every scope gets
// them too.
//
//	every kind          gains the access groups — what it reaches
//	a kind with Ops()   also gains an authorizer — who decides what reaches IT
func attachAccess() {
	groups := accessGroups()
	for _, group := range groups {
		group.Nested = true
	}
	authz := authorizerAttributes()
	for _, g := range V2Resources {
		if len(g.Children) == 0 {
			continue
		}
		iter := g.Children[0]
		if iter.Group {
			continue // a scope container holds resources, not access
		}
		iter.Nested = true
		iter.Children = append(iter.Children, groups...)

		if VerbsOfIter(iter) != nil {
			iter.Attributes = append(iter.Attributes, authz...)
			secAuthorizer(iter)
		}
	}
}

// attachLowerable gives every kind that lowers its two shapes. Authored, it is the flat
// resource already declared in V2Resources. Compiled, that SAME kind and identity is a
// container over the resource groups, with its own authored fields in meta.yaml.
//
// This runs after V2Resources exists because the after shape contains those groups. It
// is still one rule over every lowering kind, not a workflow/agent list.
func attachLowerable() {
	for _, g := range V2Resources {
		if len(g.Children) == 0 {
			continue
		}
		if iter := g.Children[0]; len(interp.LoweringsOf(iter)) > 0 {
			children := append([]*Node{}, iter.Children...)
			// EVERY KIND BUT ITS OWN. A lowering never produces a resource of the kind
			// it is lowering — an agent inside an agent is recursion, which this
			// vocabulary refuses elsewhere too — and including the group anyway is what
			// broke the one thing an agent most obviously does: name ANOTHER AGENT as a
			// tool. `agent: {support: {ops: [invoke]}}` is an access entry, and with the
			// kind's own group in its own after shape the same key was also a directory
			// of contained agents, so the walk tried to promote the entry as a resource
			// and stopped at `id is not a string`. One key, two things (§8b) — and here
			// the second thing cannot exist.
			own, _ := g.Match.(string)
			for _, r := range V2Resources {
				if key, _ := r.Match.(string); key == own {
					continue
				}
				children = append(children, r)
			}
			after := DefineIterGroup(iter.Attributes, children...).With(Leaf("meta"))
			after.Nested = iter.Nested
			g.Children[0] = Lowerable(iter, after)
		}
	}
}

// identityAttributes is the identity block on its own, for the scope container,
// which is a directory rather than a thing an edge can point at.
func identityAttributes() []*Attribute {
	return []*Attribute{
		String("id", IsCID(), Required(), Generated(GenCID), InSection("identity"), Doc("ID", "Content-addressed identifier (CID) of this resource. Stable across renames.")),
		String("name", IsVariableName(), InSection("identity"), Doc("Name", "Unique resource name within its scope. Must be a valid variable name.")),
		String("description", InSection("identity"), Doc("Description", "Free-form, human-readable description of this resource.")),
		StringSlice("tags", InSection("identity"), Doc("Tags", "Arbitrary labels for organizing and filtering this resource.")),
	}
}

var secIdentity = Section("identity", "Identity", "Resource identity and metadata.")

// Category declares what a kind is FOR, on the kind itself. It exists because the
// consumers that group resources — a graph view, a dashboard, `tau graph` — would
// otherwise each keep their own table of "kv is data, domains are ingress", and
// those tables drift from the vocabulary the moment a kind is added.
//
//	internet — makes something reachable from outside the cloud
//	trigger  — fires a workload, from inside
//	compute  — runs code
//	data     — holds state
//	code     — a build artifact
func Category(name string) NodeOption { return GroupAnnotate("category", name) }

// egressAttribute is the per-workload ALLOWLIST of hosts a workload may reach,
// layered on top of the node-wide deny floor (pkg/netguard) which it can never widen.
//
// On EVERY workload kind, not just the container one. It was on `applications` alone,
// which said that a container reaching the internet is a decision and a function
// doing the same is not — and the guest HTTP client is exactly as real as the
// container's socket. A sandbox is the sharpest case: it exists to run code somebody
// else wrote, and it was the one kind with no way to say where that code may call.
// WHAT THIS WORKLOAD MAY REACH THAT NOTHING ELSE SAYS.
//
// A host a declared REFERENCE already names is reachable without being listed here. An
// agent naming `model/opus` may dial the endpoint that model declares; a database naming
// an external engine may reach its host, and a stream naming a kafka may reach its broker. The
// alternative was tried in the fixture and is the reason this sentence exists: the agent
// listed `api.anthropic.com` while the model it names already declared
// `https://api.anthropic.com`, which is the same fact in two resources — and the day
// somebody rotates the model's endpoint, every agent naming it keeps an egress entry for
// a host that is no longer the one being dialled. One place to rotate a model is the whole
// reason that kind exists.
//
// So this list is for the hosts NOTHING in the config names: an API a function calls with
// a URL it built itself, a webhook, a third party with no resource of its own.
func egressAttribute() *Attribute {
	return StringSlice("egress", PerEnv(), InSection("network"),
		Doc("Egress", "Hosts this workload may reach that nothing else in the project names. A host declared by something this resource references — a model's endpoint, an external database's host — is already reachable and does not belong here. Layered on the node-wide deny floor, which it can never widen; absent means the floor alone."))
}

var secNetwork = Section("network", "Network", "How this workload is reached, and what it may reach.")

// wasmAttributes is the shared shape of everything that runs a WASM entrypoint —
// function, worker's wasm half and authorizer. Declared once because the three
// differ in what points AT them, never in how they run.
// hostWorlds are the interfaces a WASM workload can be built against. A function is
// named in four places that hand it four DIFFERENT worlds, and nothing in the function
// said which — so a gateway transformer named on a proxy compiled clean and failed on
// live traffic, holding bytes it tried to read as a request.
//
// Declared rather than inferred, and defaulted to `handler` so the common case writes
// nothing. What makes it enforceable is that every USE is a place tcc can see: an
// authorizer field, a transformer entry, a route handler. Audit compares the two.
//
// The honest limit: this is the config's word for it, not the artifact's. If the build
// can read the world out of the component's WIT imports, that is the better source and
// this becomes the thing checked against it rather than the thing believed.
var hostWorlds = []string{"handler", "authorizer", "http-authorizer", "dns-resolver", "instance-validator", "gateway-transformer", "proxy-transformer", "gateway-router", "proxy-router"}

// HostWorlds is every host interface a workload may be built against.
//
// EXPORTED FOR THE REASON DatabaseEngines IS: whatever has to answer "can this
// tier run that" must be asking the same question the compiler answers, or a
// world lands in the vocabulary and the tiers keep their own copies. It is
// mirrored in seven places under pkg/invoke/wasm today, and they could not ask.
func HostWorlds() []string { return append([]string(nil), hostWorlds...) }

func wasmAttributes(attrs ...*Attribute) []*Attribute {
	return wasmAttributesEntrypoint(Required(), []string{"component", "builtin", "git"}, attrs...)
}

// wasmAttributesEntrypoint is wasmAttributes with the ENTRYPOINT'S DEMAND as an
// argument, because one of the two kinds that runs a WASM component does not
// always run one.
//
// `worker` carries a `runtime` tier, and a container worker has no exported symbol
// to call — its entrypoint is the image's. Requiring `execution/call` of it made a
// legal workload unauthorable, and writing a dummy `call: main` to get past the
// error compiled an object stating two wasm facts about a container.
//
// THE GATE HAD TO BE A PARAMETER RATHER THAN A CONDITION INSIDE THIS HELPER.
// `RequiredWhen("runtime", …)` written here would name an attribute `function` does
// not have, and a condition naming a field that is not there is simply never true —
// so the entrypoint would have stopped being required on the kind that always needs
// it. That is a check narrowed to fit one caller, which is worse than the error it
// removes.
//
// THE SOURCE BRANCHES ARE A PARAMETER FOR THE SAME REASON, one question later. A
// container worker's content can be an image somebody else built; a function's cannot,
// because there is no tier under a function that runs one. Hardcoding the union here
// would put `source: {image: …}` on `function` — a branch that compiles, resolves a
// pull secret, and is read by nothing.
func wasmAttributesEntrypoint(demand AttrOption, sources []string, attrs ...*Attribute) []*Attribute {
	base := []*Attribute{
		String("implements", InSet(hostWorlds...), Default("handler"), InSection("code"),
			Doc("Implements", "Which host interface this was built against. handler: takes a request. authorizer: takes a caller and an operation, answers with ops. gateway-transformer: takes a parsed HTTP request. proxy-transformer: takes bytes on a stream. gateway-router: takes a parsed HTTP request, decides which route or function should handle it. proxy-router: takes bytes on a stream, decides which target should receive the connection.")),
	}
	// A workload's code: this repository by default, one of the project's own
	// components, a platform-shipped program, or another repository entirely. It was
	// three attributes and one string before that — "." or "module/<name>" or
	// "tau/<name>", a kind packed into a value with no room to say which namespace.
	base = append(base, contentSource("code", sources...)...)
	base = append(base,
		String(Key("execution", "call"), demand, InSection("code"), Doc("Entrypoint", "Exported entrypoint symbol invoked in the WASM component.")),
		// A CEILING IS WHAT AN ENVIRONMENT DIFFERS IN. `timeout_preview: 5s` beside
		// `timeout: 20s` is the same field said for one environment — the resource is
		// one resource, with one id and one name, running smaller where it is cheap
		// to. Identity carries no mark and never will: two environments of a workload
		// share it, which is what makes them the same workload.
		Duration(Key("execution", "timeout"), Required(), PerEnv(), InSection("limits"), Doc("Timeout", "Maximum execution time, as a human string (e.g. \"30s\").")),
		Bytes(Key("execution", "memory"), Required(), PerEnv(), InSection("limits"), Doc("Memory", "Maximum memory this workload may use, as a human string (e.g. \"32MB\").")),
		cpuLimit(),
		egressAttribute(),
	)
	return commonAttributes(append(base, attrs...)...)
}

var secCode = Section("code", "Code", "The workload's code source and entrypoint.")
var secLimits = Section("limits", "Limits", "Runtime resource limits.")

var secSource = Section("source", "Source", "Where the code is sourced from.")

// cpuLimit is the other half of what a workload may consume. `execution/memory` says
// what it may HOLD; nothing said what it may BURN, so a busy loop was indistinguishable
// from correct load and the node's other tenants paid for it.
//
// MILLICORES, an integer, because the unit has to be one nobody has to convert in their
// head: 1000 is one core, 250 is a quarter of one, and it is the unit every scheduler
// people have used already speaks. A float "0.25 cores" reads the same and rounds
// differently on every machine that parses it.
//
// Optional, unlike memory. Memory is a hard wall — exceed it and the workload dies, so
// the platform must know it up front. CPU is a rate: absent means the workload is
// scheduled without a ceiling and competes fairly, which is a usable default in a way
// that "no memory limit" is not.
// applicationAttributes is the container kind's own shape. Assembled in a function
// rather than inline because two of its blocks — readiness and liveness — are the same
// five fields twice, and splicing three slices together inside a call argument is
// harder to read than the thing it builds.
func applicationAttributes() []*Attribute {
	// WHAT IT RUNS IS NO LONGER A FIELD HERE. `image:` was Required() at this level,
	// which said a container workload always comes from somewhere else — so an
	// application built from the repository it sits in was unauthorable, and an
	// imported compose service naming a `build:` and no image had nothing to compile
	// to. It is `source: {image: …}` now, one branch of the switch that already
	// answered "where does the content come from" for every other kind, and the build
	// stanza is the other answer. That an application must have exactly one of the two
	// is checkBuild's: the DSL has no single key to hang the exclusion on.
	//
	// The pull secret went with it. It was a pair of attributes three kinds each
	// spliced in beside their own image, and it is one pair now, inside the branch
	// where it is the only thing it can mean.
	out := probeAttributes("readiness", "network", "Whether this instance should be given work.")
	out = append(out, probeAttributes("liveness", "network", "Whether this instance is still working, or should be replaced.")...)
	return append(out,
		// How many consecutive liveness failures before the platform kills the
		// instance. The cost of being wrong is asymmetric: a readiness blip takes an
		// instance out of rotation for a second, a liveness blip destroys its state and
		// its warm caches. Three, because one is a network hiccup and ten is a workload
		// wedged for a minute while traffic was still being sent to it.
		Int(Key("liveness", "failures"), MinInt(1), Default(3), InSection("network"),
			Doc("Liveness Failures", "Consecutive failed liveness checks before this instance is replaced.")),

		Bytes(Key("execution", "memory"), Required(), PerEnv(), InSection("limits"), Doc("Memory", "Maximum memory per instance.")),
		cpuLimit(),
		egressAttribute(),

		// Ceilings, not counts. How many instances run is still the platform's — these
		// bound what it may do, which is a different statement from telling it what to
		// do: one is a limit the operator owns (cost, blast radius, what the workload
		// can survive), the other is a number the system would have to fight.
		Int(Key("limits", "max_instances"), MinInt(1), PerEnv(), InSection("limits"),
			Doc("Max Instances", "The most instances the platform may run of this workload. A ceiling it scales within, not a count.")),
		Int(Key("limits", "concurrency"), MinInt(1), PerEnv(), InSection("limits"),
			Doc("Concurrency", "The most simultaneous requests one instance may be given. Cloud Run and Knative call this concurrency; Fly calls it a hard limit. Above it the platform queues or starts another instance, within max-instances.")),
	)
}

// probeAttributes is how the platform asks a workload a yes/no question about itself.
// Two of them exist, with the SAME shape and different consequences:
//
//	readiness   should this instance be given work?   no -> take it out of rotation
//	liveness    is this instance still working?       no -> kill it and start another
//
// One signal could not answer both, and the difference is not academic: a workload
// warming a cache is NOT ready and must NOT be restarted — restarting it throws away
// the warm-up and starts it again — while one deadlocked on a lock is neither ready nor
// alive, and only a restart fixes it. With a single check the platform has to guess
// which of the two it was told, and it will guess wrong in exactly the case that
// matters.
//
// Either probe can be an http poll or a FUNCTION, because "working" is not always a
// status code: a queue drained, a migration finished, a leader lease still held.
//
// An Either()+Key() switch, like every other either-or in this vocabulary. It could not
// be one at first — a Key() failed to load whenever its block was ABSENT, so the
// construct only worked on a mandatory block and a probe is optional. That was fixed in
// the engine (an absent block and a block naming something unlisted are now different
// answers), and this is the shape it unblocked: the branch is captured, and http and
// function cannot both be filled in.
func probeAttributes(name, section, doc string) []*Attribute {
	return []*Attribute{
		String(Key(name, "kind"), Path(name, Either("http", "function")), Captures(), InSection(section),
			Doc(title(name), doc+" An http poll, or a function that answers.")),
		String(Key(name, "http", "port"), IsVariableName(), InSection(section), ShowWhen(Key(name, "kind"), "http"),
			Doc(title(name)+" Port", "Which of this workload's ports the poll is made on, by name.")),
		String(Key(name, "http", "path"), InSection(section), ShowWhen(Key(name, "kind"), "http"),
			Doc(title(name)+" Path", "HTTP path polled.")),
		String(Key(name, "function", "name"), IsVariableName(), Ref("function", ByName()), InSection(section), ShowWhen(Key(name, "kind"), "function"),
			Doc(title(name)+" Function", "A function that answers for this workload, when \"working\" is not a status code — a queue drained, a migration finished, a lease still held.")),
		String(Key(name, "function", "namespace"), IsNamespacePath(), InSection(section), ShowWhen(Key(name, "kind"), "function"),
			Doc(title(name)+" Function Namespace", "Where that function lives. Omitted means this resource's own scope, then its parents.")),
	}
}

// routerAttributes: a serverless function that does the routing itself, instead of the
// static block beside it.
//
// A gateway with a `route` serves one path; five paths are five gateway documents
// repeating the host, the certificate, cors and the rate limit. A gateway with a
// `router` has none of that: the request goes to the function, and the function decides.
// It is the answer to a site whose routing is a program rather than a table — a
// prefix map, an A/B split, a tenant lookup — and to the one-route-per-document limit
// without inventing a route kind.
//
// THE KIND IS NOT WRITTEN, because there is only one thing a router can be. Nothing
// else in the vocabulary runs code at the edge on a request that has not been routed
// yet, so `router: {name: edge}` says everything: a function, by name, in a namespace.
//
// It is NOT a handler with a different word. A handler is where the request ENDS; a
// router says where it should go and the gateway takes it there, which is why the world
// it is built against is its own.
func routerAttributes(section, world string, at ...string) []*Attribute {
	ns := append(append([]string{}, at...), "namespace")
	nm := append(append([]string{}, at...), "name")
	where := "instead of a static route"
	if len(at) > 1 {
		where = "instead of this route's handler"
	}
	return []*Attribute{
		String(Key(ns...), IsNamespacePath(), InSection(section),
			Doc("Router Namespace", "Where that function lives. Omitted means this resource's own scope, then its parents.")),
		String(Key(nm...), IsVariableName(), Ref("function", ByName(), Serves()), InSection(section),
			Doc("Router", "A function that decides where each request goes. Written "+where+", never beside one; it must be built against the "+world+" world.")),
	}
}

// httpAuthorizerAttributes are the guard on traffic arriving at a name: a
// route's own, or the gateway's for every route it serves.
//
// TWO SITES FOR ONE CONTROL, the way routerAttributes already has two -- a
// gateway-wide `router` and a per-route `route/router`. A gateway that guards
// everything it serves says so once; a route that needs its own says so where
// the route is written, and the route's is the one that answers.
//
// IT IS NOT authorizerAttributes, and the difference is the QUESTION. That one
// hangs off a kind with Ops() and is asked `who: caller` -- a workload, with a
// project, an environment and a scope. Whoever opened a socket to the edge has
// none of those, so a world asked about a workload would be handed a caller
// nobody filled in and would have to guess whether the traffic was HTTP at all.
// This names a function in the `http-authorizer` world, which is asked about
// the REQUEST and answers with the ops it gets.
func httpAuthorizerAttributes(at ...string) []*Attribute {
	nm := append(append([]string{}, at...), "authorizer", "name")
	ns := append(append([]string{}, at...), "authorizer", "namespace")
	where := "any route this gateway serves"
	if len(at) > 0 {
		where = "this route"
	}
	return []*Attribute{
		// NO Serves(), for the reason authorizerAttributes omits it: an inbound
		// block is a way the thing is REACHED, and a guard is asked about
		// traffic rather than being the traffic's destination.
		String(Key(nm...), IsVariableName(), Ref("function", ByName()), InSection("policy"),
			Doc("Authorizer", "A function that decides whether a request to "+where+" may pass, and answers with the ops the caller gets. It must be built against the http-authorizer world. A route's own answers instead of the gateway's.")),
		String(Key(ns...), IsNamespacePath(), InSection("policy"),
			Doc("Authorizer Namespace", "Where that function lives. Omitted means this resource's own scope, then its parents.")),
	}
}

func title(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

func cpuLimit() *Attribute {
	return Int(Key("execution", "cpu"), MinInt(1), PerEnv(), InSection("limits"),
		Doc("CPU", "Ceiling in millicores: 1000 is one core, 250 a quarter of one. Absent means no ceiling — it competes for what is free."))
}

// certificateSecret is a supplied certificate: ONE secret holding the chain and the
// private key, the way a Kubernetes TLS secret does.
//
// It was two inline PEM fields, which put a PRIVATE KEY in the git repository — in a
// vocabulary whose `secret` kind exists to say the config never carries a value, and
// beside `database.engine/credentials`, which already takes a reference. A key that
// has ever been committed is a key that has to be treated as leaked.
//
// One reference rather than a secret for the key and a field for the chain, because
// they are one artifact: they are issued together, they expire together, and rotating
// them in two places means a window where they do not match.
func certificateSecret(section string) []*Attribute {
	return []*Attribute{
		String(Key("certificate", "secret", "namespace"), IsNamespacePath(), InSection(section), ShowWhen(Key("certificate", "source"), "provided"),
			Doc("Certificate Secret Namespace", "Where that secret lives. Omitted means this resource's own scope, then its parents.")),
		String(Key("certificate", "secret", "name"), IsVariableName(), Ref("secret", Inbound()),
			RequiredWhen(Key("certificate", "source"), "provided"), Field("CertSecret"), InSection(section), ShowWhen(Key("certificate", "source"), "provided"),
			Doc("Certificate Secret", "Secret holding the certificate chain and its private key. The key never goes in git.")),
	}
}

// instanceAttributes is how a store says it holds MANY instances rather than one, how
// many of them it may hold, and who decides whether a given instance may exist.
//
// THE BLOCK'S PRESENCE IS THE DECLARATION. There is no `multi: true` beside it: writing
// `instances:` at all is what says there is more than one, and a second field repeating
// it is a fact stated twice that two edits can disagree about — `multi: false` beside a
// validator and a limit is a document saying both things at once, and something has to
// pick a winner. A block cannot contradict itself, so nothing has to. And neither field
// inside it is optional: the instance NAME is the caller's, so a store that holds many
// and names nobody to admit them has no point at which the project gets to say no, and
// one that never says how many is unbounded because nobody chose, which is the same
// defect `database.consensus` is required for.
//
// AN INSTANCE NAME IS A KEY, NOT A PATTERN. It is written by the caller, matched by
// nobody, and looked up exactly: one string names one instance. That is what makes the
// question "which instance is this?" answerable at all — a pattern answers it with a
// SET, so two patterns can both claim a name and the store that gets the write is
// whichever the walk reached first, and no index can be built over a key nothing knows
// the value of until a request arrives.
//
// So this replaces v1's `match`/`regex`, which was this capability spelled as a lookup:
// the data plane knew only a name and walked every config regex-matching it
// (v1's services/hoarder/helpers.go). That made every store a pattern, gave the pattern no
// owner, and answered "may this instance exist" with "does this string match". NOTHING
// IN THIS VOCABULARY CARRIES THAT PAIR ANY MORE — `stream` was the last to hold it, and
// it went because a topic is named by the resource rather than matched by it — and
// nothing gains it back: a limit bounds HOW MANY instances there are and a validator
// decides WHICH, and between them there is nothing a pattern was doing that is still
// wanted.
//
// The shape is the AUTHORIZER's, deliberately (§9, the one modelling rule): the target
// owns the policy. The store names a function; the caller names only the instance it
// wants. And a validator is not a kind — what makes it one is the host interface it is
// built against, which is why `instance-validator` is a word in hostWorlds and not a
// group of its own.
//
// What the validator RETURNS — the instance's ttl, its size, whatever the SDK exposes —
// is not modelled here and must not be: config declares which function decides, and the
// parameters are the decision's, at run time.
func instanceAttributes() []*Attribute {
	return []*Attribute{
		// THE SWITCH IS WHAT MAKES THE REST OF THE BLOCK MANDATORY. A required field
		// inside an optional block cannot be said any other way: Required() fires
		// whether or not the block is there (verified — every resource with no
		// `instances:` failed), and there is no sibling with a default to hang a
		// RequiredWhen on. A Captures() switch answers "was this block written, and with
		// which key", which is exactly the question, and it is the same shape
		// `engine.kind` and the gateway's `routing` already use.
		//
		// Both the validator and the limit hang off it, so the two stand or fall
		// together: this is the only thing in the block that knows it was written, and a
		// field that stopped naming it would stop being demanded at all rather than
		// become optional in some softer way.
		String(Key("instances", "admission"), Path("instances", Either("validator")), Captures(), InSection("instances"),
			Doc("Admission", "How a new instance is let in. validator: a function decides.")),
		String(Key("instances", "validator", "namespace"), Path("instances", Either("validator"), "namespace"), IsNamespacePath(), InSection("instances"),
			Doc("Validator Namespace", "Where that function lives. Omitted means this resource's own scope, then its parents.")),
		String(Key("instances", "validator", "name"), Path("instances", Either("validator"), "name"), IsVariableName(), Ref("function", ByName()),
			RequiredWhen(Key("instances", "admission"), "validator"), InSection("instances"),
			Doc("Validator", "A function called before an instance is opened or created: it decides whether that instance may exist, and returns the parameters it gets.")),
		// HOW MANY, WITH NO DEFAULT AND NO ABSENT STATE. `0` is the answer that means
		// no ceiling, so an unbounded store is a sentence somebody wrote rather than a
		// field they never reached — the same reason `database.consensus` is required
		// and `worker.runtime` has no default. An absent limit reading as unlimited
		// would put the expensive answer on every store nobody thought about, and this
		// one is paid for by the cloud in instances that exist because a caller asked.
		//
		// NOT `limits/max_instances`, which is on the workload kinds and is a different
		// number: that one bounds how many COPIES of one workload the platform may run,
		// and the platform picks the number under it. This bounds how many DISTINCT
		// named instances a store holds, and the callers pick those.
		//
		// PerEnv, because a ceiling is exactly the kind of number a preview deployment
		// wants smaller — the same reason a cache's `size` is.
		Int(Key("instances", "limit"), Path("instances", "limit"), MinInt(0),
			RequiredWhen(Key("instances", "admission"), "validator"), PerEnv(), InSection("instances"),
			Doc("Limit", "The most instances this may hold at once. 0 means no ceiling — which is a choice, not a default, so it has to be written.")),
	}
}

var secInstances = Section("instances", "Instances", "Whether this holds one instance or many, how many, and who admits a new one.")

// Placed marks a kind whose instances HAVE A PHYSICAL FOOTPRINT — something that runs,
// or bytes that sit somewhere — so "where" is a question about the resource itself and
// not about something it names. attachPlacement gives every one of them the region
// block below, which is why this is a mark on the kind rather than a list of kinds
// somewhere else: one rule over whatever declares it, the way Ops() and Category() are.
//
// NOT DERIVED FROM `Category()`, and the four exceptions are the argument. A category
// is what a kind is FOR, which is a coarser question: `website` is compute and runs no
// process — what its bytes cost is the CAS's, exactly as a function's wasm is;
// `model` is compute and is an endpoint somebody else operates, or a `local`
// application that carries its own placement; `channel` is compute and is a name two
// workloads meet at, each placed on its own; `secret` is data and holds no value — the
// secrets service does. Placing any of the four would be a second, weaker statement
// about something already placed elsewhere.
func Placed() NodeOption { return GroupAnnotate("placed", true) }

// PlacedIter reports whether a kind's iterator declares Placed().
func PlacedIter(iter *Node) bool { placed, _ := iter.Meta["placed"].(bool); return placed }

// PlacedKinds is every kind that carries the region block, in declaration order. Read
// back off the schema rather than kept beside it, so there is one answer.
func PlacedKinds() []string {
	var out []string
	for _, g := range V2Resources {
		if len(g.Children) == 0 || !PlacedIter(g.Children[0]) {
			continue
		}
		if key, _ := g.Match.(string); key != "" {
			out = append(out, key)
		}
	}
	return out
}

// Completes declares that a kind RUNS TO COMPLETION: an invocation starts it, its
// exit is the answer, and nothing about it listens. It is what a tier asks
// before deciding how a call reaches a workload — wait for a port, or run a
// process and read how it ended — and it is a statement about the kind, not about
// any one resource, so it lives here beside Placed() and Engines().
func Completes() NodeOption { return GroupAnnotate("completes", true) }

// CompletesOf reports whether a kind declares Completes().
func CompletesOf(kind string) bool {
	for _, g := range V2Resources {
		if key, _ := g.Match.(string); key == kind && len(g.Children) > 0 {
			completes, _ := g.Children[0].Meta["completes"].(bool)
			return completes
		}
	}
	return false
}

// attachPlacement hangs the region block off every kind that declared Placed(), once
// the vocabulary exists. Same shape as attachAccess: a rule that holds for a set of
// kinds is not four lines repeated in each of them, one of which is forgotten when the
// next kind arrives.
//
// It runs BEFORE attachLowerable, which copies a kind's attributes into the container
// shape it takes after lowering — an agent or a workflow that gained the block
// afterwards would carry it only in the shape nobody authors.
func attachPlacement() {
	for _, g := range V2Resources {
		if len(g.Children) == 0 {
			continue
		}
		iter := g.Children[0]
		if !PlacedIter(iter) {
			continue
		}
		iter.Attributes = append(iter.Attributes, regionAttributes()...)
		secPlacement(iter)
	}
}

// regionAttributes is how a resource says WHERE it may run, within what its scopes
// allow. Two declarations, kept apart because they answer different questions: this is
// the PLACEMENT of one resource, and `regions` on the project and on a namespace is the
// ALLOWANCE — the ceiling everything under that scope is placed within.
//
// A REGION NAME IS AN OPAQUE LABEL THE IMPLEMENTATION DEFINES. The vocabulary never
// enumerates regions, never validates a label against a list it cannot see, and carries
// no geography: no InSet, no jurisdiction words, no latency targets. It knows the SHAPE
// — a label, a policy — and the implementation knows the world. An operator MAY define
// labels that are their provider's own region names, so the mapping can be the identity
// and the single-cloud case costs nothing.
//
// SO NOTHING IN THE SCHEMA CHECKS A LABEL, AND NOTHING CAN. Refusing `eu-wst` needs a
// list of regions, and the config will never hold one. The audit below catches it
// wherever a scope declares an allowance to check against — a label outside the ceiling
// is simply not within it — and where no scope declares one, the only place left that
// knows is the implementation: the Regions the audit is given (regions.go) answers
// every label against the regions the platform compiling it actually serves.
//
// WHICH IS THE SEAM `EmitValidation` EXISTS FOR, and it now carries this one. It could
// not before, and both reasons were in `emitAttrValidations` (interp/index.go): it read
// the value with `GetString(a.Name)` and treated a missing key as an error, so an
// OPTIONAL attribute declaring one failed the compile of every resource that omitted it;
// and that read was a flat lookup, so a nested name was not addressable — `region/name`
// is a level in the compiled object, not a key. Its two existing users were a zone's
// `fqdn` and the project's `id`, both required and both top-level, which is why neither
// had been hit. It now walks Segments, the key as declared, and emits nothing for an
// absent optional value.
//
// THE CONSUMER SIDE IS STILL OPEN, and it is named rather than assumed: utils/tcc
// answers `dns` and `project_id` and dispatches by validator name, so a `region`
// validation is carried and not yet checked. Checking it needs the cloud's own list of
// regions, which nothing can answer until a node reports the region it is in.
//
// THE POLICY IS THE VOCABULARY'S OWN SWITCH: a closed set of alternative keys, each
// branch carrying its own fields. `failover` takes EXACTLY TWO, and "the first one is
// main" is spelled by the field NAMES rather than by a position a reader has to know
// the significance of — a two-element list would re-derive it from an index, which is
// the shape §2a warns against. A third region has no key to arrive under, and a missing
// one is a compile error.
//
//	regions:                regions:                regions:
//	    pinned: [eu, us]        cdn: [eu]               failover:
//	                                                        main: eu
//	                                                        standby: us
//
// THE REGIONS VARY PER ENVIRONMENT, ON THE BRANCH ITSELF:
//
//	regions:
//	    pinned: [eu]
//	    pinned_preview: [us]
//
// A branch is a key the DOCUMENT chooses, so the suffix goes on whichever one it
// chose — and the schema names every branch it could be, which is what a finite
// matcher is for. The base branch still names the policy: a switch captures which
// branch was taken and is not itself per-environment, which is the PerEnv contract
// everywhere else too, where `<key>_<env>` sits BESIDE the key and overrides its
// value without replacing its declaration.
//
// A SCOPE'S OWN `regions` IS PER-ENVIRONMENT AS WELL, and it is the cheaper lever:
// `regions_preview: [us]` at the project places every resource that says nothing. `name`, `main` and `standby`
// are `PerEnv()`, so `name_preview: us` sits beside `name: eu` and a preview deployment
// is placed where a preview should be — which is the CEILING axis the per-environment
// rule already marks everywhere else it is spelled, since what a preview deployment
// costs to place is the difference people actually ask for.
//
// The POLICY cannot be suffixed, and not because anyone refused it: the policy IS the
// branch key, and `envPath` returns nil for a path ending in an `Either` — a key the
// document chooses rather than one the schema names. So `failover` stays failover in
// every environment, and a preview of a two-region database is two regions. Varying the
// policy is a shape question, not a mark.
//
// AND THE ALLOWANCE STILL BOUNDS EVERY SPELLING. `checkRegions` reads these fields off
// the COMPILED resource, which is compiled per environment, so `name_preview` outside
// the scope's ceiling is Broken exactly as `name` would be. That is what makes this
// safe to mark: the governance statement is the scope's allowance, and a per-environment
// placement may only move within it.
//
// AN ABSENT BLOCK IS ANYWHERE THE ALLOWANCE PERMITS, which is what almost every
// resource wants, so the common case writes nothing at all and the feature costs only
// the lines that buy something. That is also why the switch is not Required(): a switch
// inside an optional block is demanded only when the block is there.
//
// THERE IS NO `auto` BRANCH, and that there was one is worth recording. `auto` with no
// name meant "anywhere" and `auto` with a name meant "only here" — one word for a
// statement and its opposite, with the data-residency line hanging off which of the two
// a reader guessed. The branch carrying the pin is now named for the pin, its `name` is
// required, and "anywhere" is spelled by writing no block at all: the one spelling that
// cannot be mistaken for a pin.
func regionAttributes() []*Attribute {
	return []*Attribute{
		String(Key("regions", "policy"), Path("regions", Either("pinned", "cdn", "failover")), Captures(), InSection("placement"),
			Doc("Placement", "pinned: these regions only — the data-residency line. cdn: seeded here, then follows demand within the allowance. failover: exactly two, the first one main. Omit the block for anywhere the allowance permits.")),

		// A LIST, BECAUSE A RESOURCE IS IN MANY REGIONS. A database pinned to eu
		// and us has copies in BOTH — the floor is how many in EACH — so the
		// singular was never the shape. It is the same word and the same
		// plurality a scope uses for its allowance, which is the point: one
		// concept, narrowing down.
		StringSlice(Key("regions", "in"), Path("regions", Either("pinned", "cdn")),
			RequiredWhen(Key("regions", "policy"), "pinned"), PerEnv(), EmitValidation("region", "region"), InSection("placement"),
			Doc("Regions", "pinned: every region this is kept in. cdn: the regions it starts in before demand moves it — omit them and the platform seeds the choice.")),

		// FAILOVER KEEPS NAMED FIELDS, and it is the one branch that should. Its
		// two regions are not a set: which one is main is the whole statement, and
		// a two-element list would re-derive that from an index — the shape §2a
		// warns against. A third has no key to arrive under, and a missing one is
		// a compile error.
		String(Key("regions", "main"), Path("regions", "failover", "main"), RequiredWhen(Key("regions", "policy"), "failover"), ShowWhen(Key("regions", "policy"), "failover"), EmitValidation("region", "region"), InSection("placement"),
			Doc("Main", "Where this runs while it can.")),
		String(Key("regions", "standby"), Path("regions", "failover", "standby"), RequiredWhen(Key("regions", "policy"), "failover"), ShowWhen(Key("regions", "policy"), "failover"), EmitValidation("region", "region"), InSection("placement"),
			Doc("Standby", "Where it runs when the main is gone.")),
	}
}

var secPlacement = Section("placement", "Placement", "Where this may run, within what its scopes allow.")

// ---- what runs a workload, and how the thing it runs is made ----------------

// The ENGINES are the tiers this platform can run, and this is their one home.
//
//	wasm       a component-model component — wasip2/wasip3, WIT is the author
//	           contract. The default, and the thing this platform is.
//	container  an OCI image. A serverless engine too, deliberately: it is what
//	           lets a workload somebody already built arrive unchanged.
//	static     a directory of files, served rather than executed.
//
// A THIRD ENGINE IS A VALUE. Adding one is a constant here, an entry in BuildWaysOf
// if it is produced differently from the three, and the word appearing in the
// `Engines(...)` of every kind that may run it. Nothing forks and no consumer
// changes: everything that asks "what runs this" asks EnginesOf, which reads the
// answer back off the schema.
const (
	wasmEngine      = "wasm"
	containerEngine = "container"
	staticEngine    = "static"
)

// workerEngines is the one kind that CHOOSES, so the set is a variable rather than
// two literals spelled the same: `runtime`'s InSet and the kind's Engines() are one
// fact, and a second copy is free to disagree the day a third tier arrives.
var workerEngines = []string{wasmEngine, containerEngine}

// Engines declares WHICH TIERS MAY RUN A KIND, on the kind, beside Icon(),
// Category(), Ops() and Placed(). It exists for §8c's reason and it is not
// decoration: without it every consumer that has to know whether a resource is a
// component or an image keeps its own table — "a function is wasm, an application is
// a container" — and those tables drift the day a kind or a tier is added. A graph
// that colours by tier, a CLI that offers `--engine`, a scheduler picking a runtime:
// all of them ask here.
//
// It is also what the build stanza is derived from. A build produces the artifact the
// engine runs, so which WAYS of producing one a kind may write is a consequence of
// which engines it declares — attachBuild computes it, and there is no second list of
// "kinds that are built" to forget a kind in.
//
// NOT AN ATTRIBUTE, except where there is a choice to make. A function is a WASM
// component wherever it is written; writing `engine: wasm` on every function would be
// a line that can only say one thing. `worker` is the single kind with two answers,
// and it has carried `runtime` for exactly that reason since before this annotation
// existed. The two are the same set, read from one slice.
func Engines(names ...string) NodeOption { return GroupAnnotate("engines", names) }

// EnginesOfIter reads a kind's declared tiers off its iterator.
func EnginesOfIter(iter *Node) []string {
	names, _ := iter.Meta["engines"].([]string)
	return names
}

// EnginesOf answers, for a kind name, which tiers may run it — the one question, so
// nothing asks it twice and nothing keeps a table of its own. Empty for a kind that
// runs nothing: a cache, a zone, a cron, a model somebody else operates.
func EnginesOf(kind string) []string {
	for _, g := range V2Resources {
		if key, _ := g.Match.(string); key != kind || len(g.Children) == 0 {
			continue
		}
		return EnginesOfIter(g.Children[0])
	}
	return nil
}

// BuiltKinds is every kind that carries the build block, in declaration order — which
// is every kind that declares an engine, because an engine is a thing to run and
// something has to make it. Read back off the schema rather than kept beside it, the
// way PlacedKinds is.
func BuiltKinds() []string {
	var out []string
	for _, g := range V2Resources {
		if len(g.Children) == 0 || len(EnginesOfIter(g.Children[0])) == 0 {
			continue
		}
		if key, _ := g.Match.(string); key != "" {
			out = append(out, key)
		}
	}
	return out
}

// The WAYS an artifact is produced. Each is a different KIND of build rather than a
// setting on one, which is why they are the branches of a switch: the fields under
// one mean nothing under another, and strict mode says so per branch
// (`unknown key 'install'; valid keys here: image, output, path`) rather than
// dropping a key nothing reads.
const (
	commandsWay   = "commands"
	scriptWay     = "script"
	dockerfileWay = "dockerfile"
)

// BuildWaysOf says how an artifact for one engine can be produced, and it is PER ENGINE
// because the answers are not alike. A wasm component and a static site are both made
// by running something in a toolchain image and taking what it left behind; an OCI
// image is made by building a Dockerfile and is not made by a shell command at all.
// One shared set would have let `dockerfile:` be written under a website — the
// compiler would have accepted it and nothing would have read it, which is exactly
// the silence the strict-key check exists to end.
//
// A FOURTH WAY IS AN ENTRY HERE. `framework` — a name the platform's own builder
// knows end to end — is the one this vocabulary deliberately does not ship yet; see
// the note on the switch below.
func BuildWaysOf(engine string) []string {
	if engine == containerEngine {
		return []string{dockerfileWay}
	}
	return []string{commandsWay, scriptWay}
}

// buildAttributes — HOW THE CONTENT AT `source:` BECOMES THE THING THAT RUNS.
//
// `source:` says WHERE the code is. Nothing said what turns it into an artifact, so
// the answer lived in the author's own repository as `.taubyte/config.yaml` plus
// `.taubyte/build.sh` — a v1 shape v2 inherited rather than chose. Every other
// platform puts it in the project descriptor, so importing one otherwise leaves two
// options: write a tau-shaped file into a repository we were given READ access to, or
// state the build here. This is the second. Nothing of theirs is touched.
//
// WHAT THIS FIXES IS THE BUILD HALF, and claiming more would be the defect this file
// keeps catching in itself. A descriptor from somewhere else is not a build file: a
// compose service or a fly.toml describes a whole workload — what it listens on, what
// it mounts, what it waits for, how it is restarted, where it runs — and each of those
// is a separate question here, some answered by another block and some not answered at
// all. So no descriptor "becomes importable" because this landed, and a selection
// argument resting on one would be untrue. The claim is the narrow one, and it is
// enough: how an artifact is produced is now something the config can state, on every
// kind that is built, and a descriptor imports the part of itself this covers.
//
// THE MECHANISM DOES NOT CHANGE. pkg/builder already runs the build inside an OCI
// image with its network confined, treats what the repository supplies as untrusted,
// and takes what the build left in its output directory. What changes is where the
// DECLARATION comes from.
//
//	build:                          build:                    build:
//	    commands:                       script:                   dockerfile:
//	        image: node:22                  image: golang:1.23        path: Dockerfile
//	        install: npm ci                 path: .taubyte/build.sh   target: prod
//	        run: npm run build              output: main.wasm
//	        output: dist
//
// THE ENGINE IS NOT IN HERE, and that is the load-bearing cut. A build method
// describes the BUILD ENVIRONMENT, not the tier the product runs on: this platform's
// own builder is a container that produces a `.wasm`, and a Dockerfile emitting a
// component would be an ordinary thing to write. So an engine key here would be a
// second, weaker statement of what the KIND already declares with `Engines(...)` and
// what `worker.runtime` already chooses between — the same fact in two places, and §4
// is about what a second copy is free to do. What a build produces is decided by what
// the workload IS; this block only says how to make it.
//
// WHAT EACH ABSENCE MEANS, because all of them are load-bearing:
//
//   - NO `build:` AT ALL — nothing here builds it, and the repository decides exactly
//     as it does today. That is the zero-config case and it costs no keys. It is also
//     the whole of the answer to "does the config win or does the file win": the
//     block's PRESENCE is the switch, which is the rule `instances:` already states.
//   - `build:` WITH NO BRANCH — `attribute build/kind has an empty key`. A build that
//     names no way of building is not a half-declared build, it is a typo.
//   - NO `output` — whatever the command left in the builder's output directory, which
//     is the contract `.taubyte/build.sh` has always had. A value names a directory or
//     a file in the SOURCE tree and the platform takes it from there, which is what
//     makes an imported `dist` work with no script anywhere. `.` is that tree's own
//     root, and it is a THIRD state rather than a spelling of this one: the build
//     published in place instead of into a directory it was handed.
//   - NO `target` — the last stage of the Dockerfile.
//
// INSTALL AND RUN ARE TWO FIELDS, not one shell line joined with `&&`. They are two
// steps everywhere they come from, and the reason is not cosmetic: the install step is
// what a build cache is keyed on, and folding it into the build command guarantees a
// cold dependency fetch on every push. That is the first thing an author notices after
// importing, and it is measured in minutes.
//
// NO FRAMEWORK PRESET IN THIS VERSION, AND THAT IS THE OWNER'S TO REVISIT (§11). The
// default taken here is LATER. A preset is a NAME whose meaning is a builder that
// knows a framework's whole output — its functions, its prerenders, its routing
// manifest — not sugar over the three fields below. This platform has no such builder,
// so a name in the schema would be a config asserting something untrue, and the
// failure lands in production as a 404 rather than at import as an error. What
// unblocks it is a builder shipping, and the day one does the change is one entry in
// BuildWaysOf plus one attribute: `install`, `run` and `output` are already declared on
// a subset Either() and extend to a `framework` branch without moving. Until then an
// importer that reads a framework name resolves it to these three fields in its OWN
// table, which is where a table nobody in this repository controls belongs — the
// author can read what was decided and edit it, and a stale table ships weekly instead
// of being pinned into a deployed project's schema.
func buildAttributes(ways ...string) []*Attribute {
	// The ways that RUN SOMETHING IN A TOOLCHAIN IMAGE share three facts, so those
	// three are declared ONCE on a subset Either() — the construct `region/name`
	// already uses for the one field two policies share. It is one fact whether the
	// author wrote the commands or named the script that holds them, and it is what
	// keeps `image` and `output` off `dockerfile`, where an image builds itself and
	// the image IS the output.
	var runners []string
	for _, w := range ways {
		if w == commandsWay || w == scriptWay {
			runners = append(runners, w)
		}
	}
	has := func(way string) bool { return slices.Contains(ways, way) }
	inRunner := func(leaf string) AttrOption {
		return Path([]StringMatch{"build", Either(runners...), leaf}...)
	}

	out := []*Attribute{
		// THE SWITCH, and its branch set is the caller's — the same parameterisation
		// contentSource takes, for the same reason: the ways of producing a wasm
		// component are not the ways of producing an image, and a per-kind set is what
		// makes `unknown key 'dockerfile'; valid keys here: commands, script` the error
		// a website's author gets instead of a key that compiles and does nothing.
		//
		// Field("BuildKind") because `Field("Kind")` is taken by `source/kind` on every
		// kind that has both — the disambiguation `host/kind` already had to make.
		String(Key("build", "kind"), Path("build", Either(ways...)), Captures(), Field("BuildKind"), InSection("build"),
			Doc("Build", "How the thing that runs is made. Omit the whole block and this repository decides, as it always has.")),
	}

	if len(runners) > 0 {
		out = append(out,
			// REQUIRED, ON BOTH RUNNING BRANCHES. A command with nothing to run it in
			// is not a build: it is a build whose behaviour changes the day the
			// platform's default toolchain does, silently and for everybody at once.
			// This is `.taubyte/config.yaml`'s `image` field, promoted to the config —
			// and it is why there is no separate toolchain-version block anywhere here.
			// A version pin is a tag.
			//
			// Accessor because the leaf is `image` and so is `source/image`'s, and
			// `worker` declares both — a generated client names a declared field by the
			// last segment of its path, so two of them are one name and it keeps the
			// first. accessor_test does NOT hold this pair: a path with an Either() in
			// it has no single location and the test excludes it (addressableName). So
			// the override is the only thing between the two rather than a second line
			// of defence, which is worth knowing before deleting it.
			String(Key("build", "image"), inRunner("image"), Accessor("BuildImage"),
				RequiredWhen(Key("build", "kind"), runners...), PerEnv(), InSection("build"),
				Doc("Toolchain", "OCI image the build runs in, e.g. \"node:22\" or \"golang:1.23\". The source is mounted into it and the product is taken from the output directory.")),

			// IsRepoLocation, because THREE STATES ARE DISTINCT HERE and the vocabulary
			// has to be able to say each of them. Omitted is the output directory the
			// BUILDER hands the build, which is not in the source tree at all and is the
			// contract `.taubyte/build.sh` has always had. `.` is the source tree's own
			// root: the build published in place. A path is somewhere under it.
			//
			// This was IsRepoPath first, under a comment citing source.go for a rule
			// source.go states the other way round: the root is a second spelling only
			// where ABSENT ALREADY NAMES IT, and here absent names somewhere else. What
			// that cost was narrow and load-bearing: a build that writes beside its own
			// index.html had no spelling here at all, which is a `publish = "."`, an
			// `outputDirectory` of `"."`, and every monorepo whose base directory IS
			// what it serves — the plainest static site there is, and the first thing a
			// static-site import meets.
			String(Key("build", "output"), inRunner("output"), IsRepoLocation(), PerEnv(), InSection("build"),
				Doc("Output", "Where the build left its product, relative to the source — \"dist\", \"build\", \"main.wasm\". \".\" is the source root, for a build that publishes in place. Omitted means the build wrote to the output directory the builder gave it.")),
		)
	}

	if has(commandsWay) {
		out = append(out,
			// A ONE-BRANCH Either(), deliberately, and the same call proxyHandlerKinds
			// made with one handler kind: `framework` is the second branch these two
			// fields are already shaped for, so adding it later is a list entry rather
			// than a reshape of everything written against them.
			String(Key("build", "install"), Path("build", Either(commandsWay), "install"), PerEnv(), InSection("build"),
				Doc("Install", "The dependency step, e.g. \"pnpm install --frozen-lockfile\". A separate field from the build because it is what a build cache is keyed on; omitted means there is nothing to fetch.")),
			String(Key("build", "run"), Path("build", Either(commandsWay), "run"),
				RequiredWhen(Key("build", "kind"), commandsWay), PerEnv(), InSection("build"),
				Doc("Command", "The build step, e.g. \"pnpm build\".")),
		)
	}

	if has(scriptWay) {
		// THE SCRIPT BRANCH IS `.taubyte/build.sh`, DECLARED. It stops being a file the
		// platform goes looking for and becomes a path somebody wrote, which is what
		// removes the precedence question rather than answering it: a script runs
		// because the config named it, and a repository that merely contains one is
		// just a repository. Whether the platform keeps DISCOVERING an undeclared
		// `.taubyte/build.sh` when no `build:` block is written is the owner's (§11);
		// the default taken here is that it does, so nothing already deployed changes,
		// and that discovery is the meaning of the absent block rather than an override
		// of a present one.
		out = append(out, String(Key("build", "script"), Path("build", scriptWay, "path"), IsRepoPath(),
			Accessor("BuildScript"), RequiredWhen(Key("build", "kind"), scriptWay), InSection("build"),
			Doc("Script", "A script in this repository that builds it, e.g. \".taubyte/build.sh\". The escape hatch: everything the other ways cannot say. It runs with its network confined, like any other repository content.")))
	}

	if has(dockerfileWay) {
		// THE ONE HONEST LIMIT IN THIS BLOCK, stated where it bites rather than
		// discovered. Building a Dockerfile is a thing this platform already does —
		// pkg/builder tars a repository's own docker directory and builds an image from
		// it, under a restricted build egress, because a repository's Dockerfile is
		// untrusted for the same reason its script is. What it does with that image
		// today is RUN THE BUILD IN IT. Taking the produced image as the ARTIFACT, and
		// running a workload on an image this cloud built rather than one it pulled, is
		// the half that is an integration and not a field.
		//
		// The branch is here anyway, and deliberately: without it a container workload
		// can only ever be somebody else's image, which is the state that made this
		// whole stanza necessary. The field names the gap instead of hiding it — an
		// author who writes it gets a build that is planned for, and the alternative
		// was an import that silently could not carry the most common thing a container
		// project states about itself.
		out = append(out,
			// Accessor because `build/dockerfile/path` and `build/script/path` end in
			// the same word, and `worker` is the kind that declares both branches — so
			// a client would name one `Path` and silently drop the other. That pair, on
			// that kind, is what accessor_test reports when either override is removed.
			// `source/path` is not part of it: the engine derives that path from the
			// key, and the test names such a field by its key instead.
			String(Key("build", "dockerfile"), Path("build", dockerfileWay, "path"), IsRepoPath(),
				Accessor("Dockerfile"), RequiredWhen(Key("build", "kind"), dockerfileWay), InSection("build"),
				Doc("Dockerfile", "Path to the Dockerfile, relative to the source.")),
			String(Key("build", "target"), Path("build", dockerfileWay, "target"), IsVariableName(),
				ShowWhen(Key("build", "kind"), dockerfileWay), InSection("build"),
				Doc("Stage", "Which stage of a multi-stage Dockerfile is the artifact. Omitted means the last one.")),
		)
	}
	return out
}

var secBuild = Section("build", "Build", "How the source becomes the thing that runs.")

// attachBuild hangs the build block off every kind that declared an engine, once the
// vocabulary exists and can be read. Same shape as attachPlacement and attachAccess,
// and for the same reason: a rule that holds for a set of kinds is not six lines
// repeated in each of them, one of which is forgotten when the seventh arrives.
//
// The branch set is UNIONED over the kind's engines, so `worker` — the one kind with
// two — is the one kind that can write all three ways. Which of them goes with which
// tier is a pair the DSL cannot state (the two blocks are different attributes at
// different paths, so §3's switch has no single key to hang on) and is checkBuild's,
// alongside the other cross-block exclusions checks.go already owes.
//
// It runs BEFORE attachLowerable, which copies a kind's attributes into the shape it
// takes after lowering — a kind that gained the block afterwards would carry it only
// in the shape nobody authors.
func attachBuild() {
	for _, g := range V2Resources {
		if len(g.Children) == 0 {
			continue
		}
		iter := g.Children[0]
		engines := EnginesOfIter(iter)
		if len(engines) == 0 {
			continue
		}
		var ways []string
		for _, e := range engines {
			for _, w := range BuildWaysOf(e) {
				if !slices.Contains(ways, w) {
					ways = append(ways, w)
				}
			}
		}
		iter.Attributes = append(iter.Attributes, buildAttributes(ways...)...)
		secBuild(iter)
	}
}

// handlerKinds are what may serve a route. Closed, because it is the Either() a
// handler's kind switches on: a key outside this set is a compile error rather than
// a route that silently never fires.
// `agent` is in the list because an agent is asked a question and answers it, which is
// a request and a response — the same shape a function has. Leaving it out meant an
// agent could be declared and never reached: nothing could route to one, and the audit
// could not complain either, because a kind no route can name is not one it checks.
// A BUS IS IN HERE BECAUSE IT IS REACHED THE WAY EVERYTHING ELSE IS. It used to
// own a name of its own -- hostAttributes and AnswersAt, like a gateway -- and the
// gateway found it by walking the hosts index instead of by matching a route. That
// was a SECOND INGRESS: two ways to own one name, with collision handling in
// ServeHTTP for an author who used both, and a bus that skipped everything a route
// carries -- path, methods, CORS, rate limit, and the transformer chain, which is
// to say a bus could not be authorized or rate-limited at all.
//
// It goes through the gateway to gain a websocket, and inherits the whole pipeline
// by being a handler like any other.
var handlerKinds = []string{"function", "application", "website", "gateway", "mcp", "agent", "bus"}

// externalStreamEngines are the pubsub clusters reached over the network, and
// streamEngines is those plus tau's own. Closed, for the same reason a database's are:
// each one is a wire protocol and a client library, which is a release rather than a
// config value, and a closed set is what makes naming a non-pubsub here impossible
// rather than merely wrong.
var externalStreamEngines = []string{"kafka", "nats"}

// storageTypes is what a storage may be. It was written inline in the two
// places that needed it, which is how a closed set becomes two closed sets.
var storageTypes = []string{"object", "streaming"}

// StorageTypes is every data model a storage may be.
func StorageTypes() []string { return append([]string(nil), storageTypes...) }

var streamEngines = append([]string{tauEngine}, externalStreamEngines...)

// StreamEngines is every engine a stream may name, tau's own first.
//
// THE STREAM TWIN OF DatabaseEngines, and its absence is why the one consumer
// that asks "can this cloud open that" answers with a bare literal instead.
func StreamEngines() []string { return append([]string(nil), streamEngines...) }

// TauEngine is what this cloud's own engine is called, wherever a consumer has
// to tell it apart from a server somebody else runs.
func TauEngine() string { return tauEngine }

// Connected reports whether a kind is reached over a SOCKET rather than
// dispatched as a call: every tier it may run on is the container tier, so an
// instance is a process with ports and not a component with an entrypoint. A
// channel's bytes go to such a workload's port, and to anything else as an
// invocation.
func Connected(kind string) bool {
	engines := EnginesOf(kind)
	if len(engines) == 0 {
		return false
	}
	for _, e := range engines {
		if e != containerEngine {
			return false
		}
	}
	return true
}

// channelServerKinds are what may answer a channel. A container gets the bytes on one of
// its own ports; a function is dispatched with the connection in hand. Both are real, and
// nothing else is: a website is HTTP by definition and a gateway is an ingress.
var channelServerKinds = []string{"application", "function"}

// proxyHandlerKinds are what may sit behind an L4 proxy. Only a container: a raw
// connection is held open and streamed, and a function is request-and-answer with no
// connection to hold. A website or an mcp is HTTP by definition, so a route serves
// them and this never does.
//
// It is still an Either() with one branch rather than a flat block, so a proxy's
// handler reads exactly like a gateway route's, and a second kind is a list entry
// rather than a reshape.
var proxyHandlerKinds = []string{"application"}

// hostAttributes is how anything reachable from the internet gets its name.
//
// THE GENERATED PART IS NOT IN THE CONFIG, and that is the whole point. Every cloud
// serves a zone of its own — `<project label>.<cloud fqdn>` — and a project gets a
// label inside it.
//
// The two halves are NOT alike, and the difference is what makes this usable. The
// project label is DERIVED from the project id, which is a CID and already unique, so
// it is a pure function anyone holding the config can compute — see ProjectLabel. Only
// the cloud's own fqdn is unknown until deploy.
//
// v1 wrote a whole generated domain into the config, which tied a project to the one
// cloud that string described: the same repo deployed to a second cloud claimed a name
// it did not have there. Deriving the label keeps the stable half stable — the same
// project has the same label everywhere — while leaving the half that genuinely
// belongs to the cloud out of git.
//
//	host:
//	    generated:
//	        name: shop        # shop.<project>.<cloud>, whatever those turn out to be
//
//	host:
//	    zone:
//	        in:
//	            name: example_com
//	        name: shop        # shop.example.com
//
// Making it a ZONE rather than a domain is what makes the rest work. The cloud is
// authoritative for it, so it can publish whatever the resource needs under that name
// without anyone authoring a record: A and AAAA for a gateway, and for an L4 proxy the
// SRV record that carries the port. A "generated domain" — one flat name handed out —
// could not have carried an SRV label at all.
//
// It also pays for certificates: the cloud holds a wildcard for its own zone, so a
// generated name needs no per-name issuance and no `certificate` block.
//
// Omitting `name` means the apex of that zone.
func hostAttributes(section string) []*Attribute {
	return []*Attribute{
		String(Key("host", "kind"), Path("host", Either("generated", "zone")), Captures(), Field("HostKind"), InSection(section),
			Doc("Host", "generated: a name under the cloud's own zone, resolved when it is deployed. zone: a name in a zone you own and delegated here.")),
		String(Key("host", "name"), Path("host", Either("generated", "zone"), "name"), IsVariableName(), InSection(section),
			Doc("Name", "The label within that zone, e.g. \"shop\". Omit it for the zone apex.")),
		// INBOUND: the zone is what ANSWERS for this name. The reference is declared
		// here because this is where the name is chosen, but the relationship runs the
		// other way, and an arrow from here would say the gateway reaches the zone.
		String(Key("host", "zone", "in", "namespace"), IsNamespacePath(), InSection(section),
			Doc("Namespace", "Where that zone lives. Omitted means this resource's own scope, then its parents.")),
		String(Key("host", "zone", "in", "name"), Path("host", Either("generated", "zone"), "in", "name"), IsVariableName(), Ref("zone", Inbound()), RequiredWhen(Key("host", "kind"), "zone"), InSection(section), ShowWhen(Key("host", "kind"), "zone"),
			Doc("Zone", "The zone this name lives in, as \"zone/<name>\". Only for a zone you own; a generated name has no zone to choose.")),
	}
}

// A DATABASE ANSWERS TWO QUESTIONS, AND IT USED TO BE ASKED ONE.
//
// What DATA MODEL it is — keys and values, documents, SQL, vectors — and WHICH ENGINE
// backs it. The two are independent: tau's own engine serves all four, and somebody
// else's Postgres, MySQL or Mongo serves the ones that have an external product. Asking
// only the second question is what left `kv` unauthorable — a durable key-value store
// had no word in this vocabulary and the nearest thing was `cache`, whose own
// documentation says it is not a system of record — while `vector` became a KIND of its
// own for no reason except that its verbs differ, which is a property of the data model
// and not of the resource.
//
// So the document names the type, and the type block names the engine:
//
//	sql:                                kv:
//	    postgres:                           distributed:
//	        host: db.example.com                size: 32MB
//	        credentials: { name: pg }
//
// A WRONG PAIR IS UNREPRESENTABLE, which is the property the old shape had and the one
// thing that could not be given up. `engine: kafka` was refused because the engine is a
// KEY from a closed set rather than a free string; a document
// database on Postgres is refused by the same check, because the set of engine keys is
// declared PER TYPE:
//
//	unknown key 'postgres'; valid keys here: distributed, mongo
//
// One `engine:` key beside one `type:` key could not have done that — each value is
// legal on its own and only the pair is wrong, which is a rule somebody has to remember
// to write and a document that compiles until they do. The cost is that the engine is
// four keys rather than one (`kv/engine`, `sql/engine`, …), read as `<type>/engine`;
// that is the same shape `storage` already has, where `object` and `streaming` each own
// their block.

// tauEngine is tau's own. On a database it is pkg/kvdb, a Merkle-CRDT replicated by the
// storage service; on a stream it is the swarm's own gossipsub. It is the one engine
// every kind with an engine axis has, and the only one that needs no external system.
const tauEngine = "distributed"

// externalEngineAttributes is the connection an engine reached over the network carries,
// and it is ONE helper because a database's Postgres and a stream's Kafka are the same
// three facts: where it is, which secret opens it, and how the wire is protected. It was
// written twice before it was written once, and the two copies had already drifted — a
// database's credential reference had its namespace outside the engine branch and its
// name inside, which is one reference authored in two places.
//
// `at` is the path the engine block hangs off — a database's TYPE block, or nothing at
// all for a kind whose only axis IS the engine — and `named` is the same prefix for the
// compiled keys, which need not be the authored path. `switchKey` is the Captures()
// attribute that says which branch was taken, because "required for an external engine"
// is a condition on that and nothing else.
//
// A field only one caller needs is that caller's to append. A database has a `database`
// name within the server; a stream has no such thing, and a helper that emitted one
// anyway would be putting an inert key on every stream.
func externalEngineAttributes(at []StringMatch, named []string, engines []string, switchKey AKey, section string, shown AttrOption) []*Attribute {
	key := func(rest ...string) AKey { return Key(append(append([]string{}, named...), rest...)...) }
	path := func(rest ...string) AttrOption {
		p := append(append([]StringMatch{}, at...), Either(engines...))
		for _, r := range rest {
			p = append(p, r)
		}
		return Path(p...)
	}
	return []*Attribute{
		String(key("host"), path("host"), RequiredWhen(switchKey, engines...), PerEnv(), InSection(section), shown,
			Doc("Host", "Where the external server is reached, as host or host:port.")),
		String(key("credentials", "namespace"), path("credentials", "namespace"), IsNamespacePath(), InSection(section), shown,
			Doc("Namespace", "Where that secret lives. Omitted means this resource's own scope, then its parents.")),
		String(key("credentials", "name"), path("credentials", "name"), IsVariableName(), Ref("secret", Inbound()),
			RequiredWhen(switchKey, engines...), InSection(section), shown,
			Doc("Credentials", "Secret holding the credentials to connect with, as \"secret/<name>\".")),

		// SAFE BY DEFAULT, EXPLICIT TO WEAKEN: absent means verify-full, and an author
		// who wants credentials on the wire in the clear has to write `disable` and see
		// the word. Deliberately not RequiredWhen: a value here is a WEAKENING, so the
		// safe state has to be the one an author reaches by writing nothing.
		String(key("tls"), path("tls"), InSet("verify-full", "require", "disable"), PerEnv(), InSection(section), shown,
			Doc("TLS", "How the connection is protected. Omitted means verify-full. verify-full: encrypted, with the server's certificate and hostname checked. require: encrypted, certificate unverified - a network attacker can still be the server. disable: plaintext, credentials included.")),
	}
}

// databaseType is one data model. ONE TABLE, because five things read it — the type
// switch's Either, each type's engine Either, the kind's Ops(), the audit that refuses
// an op the target's type does not have, and the JSON Schema an editor completes from —
// and a second list of any of them drifts the day a type is added.
// syncType is one coordination primitive: what may be done to one, and what one has
// to say about itself.
//
// THE KIND IS `sync` AND THE TYPE IS THE THING. A queue, an append-only log and — when
// they have a shape and an engine — a lock, a counter or a barrier are one kind for the
// same reason `sql`, `kv`, `document` and `vector` are: an author asks one question and
// the type answers it in a different shape, and a kind each would be four copies of the
// identity, access, placement and quota machinery.
//
// WHAT IS IN AND WHAT IS OUT, stated so the kind does not become a drawer: DOES THE
// RESOURCE DECIDE WHO WINS. A queue decides which consumer gets an item. A log decides
// what order two concurrent appends happened in. A lock would decide who holds it. A
// `stream` and a `bus` decide nothing — everything attached gets everything sent — so
// they are fan-out and they stay their own kinds. Arbitration in, broadcast out.
type syncType struct {
	name string
	// ops is what may be done to one of this type. THE PER-KIND VERB RULE IS NOT GIVEN
	// UP: Ops() carries the union, because an access block is loaded before anything
	// knows which resource it names, and checkSyncOps holds every entry to the verbs of
	// the type it actually reaches — the same seam checkDatabaseOps is.
	ops Verbs
	// extra is what only this primitive has to say — a queue's delivery budget, a log's
	// retention. A field every type would carry belongs in syncType.attributes.
	extra func(syncType) []*Attribute
	doc   string
}

var syncTypes = []syncType{
	// Claim / visibility-timeout / ack / DLQ — not pop-and-delete. It was written here
	// as the thing a runtime still owed, back when a pop deleted the item before
	// returning it and a consumer that crashed lost the job with no trace; pkg/queue
	// implements the whole of it now — a claim is exclusive and invisible for a
	// timeout, an unacknowledged item comes back, attempts are counted with a backoff,
	// and one that gives up is buried in the dead letter.
	{
		name:  "queue",
		ops:   Verbs{DeliveredOp("receive"), WriteOp("send")},
		extra: queueAttributes,
		doc:   "Work handed from one workload to another. A claim is exclusive, an unacknowledged item comes back, and one that gives up lands in a dead letter.",
	},

	// IT WAS A DECLARED PLACEHOLDER WITH NO FIELDS AT ALL, written that way on purpose:
	// this vocabulary deletes inert things, so a kind with nothing in it either gets
	// removed or gets completed by somebody guessing, and the note existed to stop both
	// until the shape was given. This is the shape.
	//
	// `append` alone is the claim it makes. There is deliberately no `put` and no
	// `delete`: a record that can be overwritten or removed is not an append-only log,
	// and the verb set is the one place this vocabulary states that where every consumer
	// reads it back.
	//
	// `read` is a PULL — the reader goes and asks, forward from a cursor. A FOLLOW is
	// the other direction and would be a third verb, a DeliveredOp the way a queue's
	// `receive` is: the platform hands each new record over as it lands. It needs a
	// dispatcher rather than a host function and it is not here yet.
	{
		name:  "log",
		ops:   Verbs{WriteOp("append"), ReadOp("read")},
		extra: logAttributes,
		doc:   "Records appended to the end and read forward. Nothing rewrites what is already there.",
	},
}

// SyncTypes is every coordination primitive `sync` may be, in declaration order.
//
// SyncTypeOf ANSWERS ABOUT ONE AND THIS ENUMERATES THEM, which is the half a
// consumer switching per type needs: without it a fourth primitive falls to a
// default arm and travels with none of its own fields.
func SyncTypes() []string { return syncTypeNames() }

func syncTypeNames() []string {
	out := make([]string, 0, len(syncTypes))
	for _, t := range syncTypes {
		out = append(out, t.name)
	}
	return out
}

// SyncTypeOf is the type table row one name selects. Exported because the composer asks
// the same question the compiler answers — see DatabaseEngines for why that matters.
func SyncTypeOf(name string) (Verbs, bool) {
	for _, t := range syncTypes {
		if t.name == name {
			return t.ops, true
		}
	}
	return nil, false
}

// syncOps is every verb any type declares, deduplicated — what an access block may
// spell before anything has resolved which resource it names.
func syncOps() []Verb {
	seen := map[string]Verb{}
	out := []Verb{}
	for _, t := range syncTypes {
		for _, v := range t.ops {
			if had, ok := seen[v.Name]; ok {
				if had != v {
					panic(fmt.Sprintf("sync: verb %q is declared two ways: %+v and %+v", v.Name, had, v))
				}
				continue
			}
			seen[v.Name] = v
			out = append(out, v)
		}
	}
	return out
}

// syncAttributes is the kind's shape: the type switch, then one block per type.
func syncAttributes() []*Attribute {
	out := []*Attribute{
		// THE TYPE IS NOT WRITTEN, it is the block that IS written — the same construct
		// `database` and `storage` use, and the reason one of these cannot be of two
		// types or of none.
		String("type", Path(Either(syncTypeNames()...)), Captures(), InSection("sync"),
			Doc("Type", "Which coordination primitive this is. It is the key everything else hangs off: a sync resource is one block named after its type.")),
	}
	for _, t := range syncTypes {
		out = append(out, t.attributes()...)
	}
	return out
}

func (t syncType) attributes() []*Attribute {
	shown := ShowWhen("type", t.name)
	out := []*Attribute{
		// WHAT HAPPENS TO TWO WRITES THAT ARRIVE AT ONCE. It is the same axis and the
		// same two values `database.consensus` carries, for the same reason: `raft` is
		// one holder at a time, so a write costs a round trip and nothing is silently
		// discarded; `crdt` is the coordination-free replication tau already runs, where
		// every node takes the write the moment it arrives and concurrent writes merge.
		//
		// BOTH VALUES NAME A MECHANISM, and the set used to name only one of them: it
		// read `single-writer` beside `crdt`, a promise beside an algorithm, two
		// different kinds of word in one InSet — and the described half was the one a
		// reader had to decode. Naming both costs the freedom to swap the log out from
		// under a config that says `raft`, and that cost is taken rather than hidden
		// behind a description. What differs is still the PROMISE, and it is the Doc
		// string's to carry: a raft log has one total order and a dense position, a crdt
		// log converges on an order and two readers may transiently disagree about it.
		// That is why the ABI carries an opaque cursor rather than an offset — an offset
		// is a promise only one of these values can keep.
		//
		// REQUIRED FOR THE TYPE THAT IS WRITTEN, and not unconditionally: an
		// unconditional Required() is demanded on every type's block at once, so a log
		// would owe a `queue/consensus` too. Required at all for the reason
		// `database.consensus` is — silence is a choice, and whichever answer it carried
		// would be wrong for whoever never read the line.
		//
		// NO `gossip`, and the cache's third value is the one deliberately missing. A
		// gossiped write can be dropped outright rather than merged, and a dropped item
		// is work that never ran or a record that never happened. A cache can take that;
		// nothing here can.
		String(Key(t.name, "consensus"), Path(t.name, "consensus"), Accessor(title(t.name)+"Consensus"),
			InSet("crdt", "raft"), RequiredWhen("type", t.name), InSection("sync"), shown,
			Doc("Consensus", "What happens to concurrent writes. raft: one holder at a time, so a write costs a round trip, there is one total order, and nothing is silently discarded. crdt: every node takes the write the moment it arrives and they merge, with nothing refused and nothing waited for — the order converges rather than being decided.")),
	}
	if t.extra != nil {
		out = append(out, t.extra(t)...)
	}
	return out
}

// queueAttributes is the delivery budget: what the queue promises about timing, retries
// and where an item that gave up goes.
func queueAttributes(t syncType) []*Attribute {
	shown := ShowWhen("type", t.name)
	k := func(rest ...string) AKey { return Key(append([]string{t.name}, rest...)...) }

	return []*Attribute{
		Duration(k("visibility_timeout"), Default("30s"), DefaultWhen("type", t.name), PerEnv(), InSection("delivery"), shown,
			Doc("Visibility Timeout", "How long a claimed item stays invisible before it becomes deliverable again.")),

		// HOW LONG AN ITEM WAITS BEFORE ANYONE MAY HAVE IT. Every queue gets it — it is
		// SQS's DelaySeconds and it is useful on its own — and it is also what a
		// workflow's `wait` step becomes: a step that pauses is a queue that holds its
		// work back, so the pause is a property of the thing that does the holding
		// rather than a string some driver parses.
		Duration(k("delay"), InSection("delivery"), shown,
			Doc("Delay", "How long an item waits before it becomes deliverable at all, as a human string (e.g. \"30s\"). Absent means immediately.")),
		Int(k("max_attempts"), Default(5), DefaultWhen("type", t.name), InSection("delivery"), shown,
			Doc("Max Attempts", "Deliveries before the item moves to the dead-letter queue.")),

		// HOW REDELIVERY IS SPACED. The queue owns it because the queue is what
		// redelivers — a workflow step's `backoff` is copied onto the queue it becomes,
		// and every other queue user gets the same three fields for free. Absent means
		// the visibility timeout alone governs.
		Duration(k("retry", "interval"), InSection("delivery"), shown,
			Doc("Retry Interval", "Delay before a failed delivery becomes deliverable again. Absent means the visibility timeout alone governs.")),
		Float(k("retry", "factor"), InSection("delivery"), shown,
			Doc("Retry Factor", "Multiplier applied to the interval per further attempt: the delay for attempt n is interval times factor^(n-1), capped by the ceiling. Absent or 1 means a fixed interval.")),
		Duration(k("retry", "ceiling"), InSection("delivery"), shown,
			Doc("Retry Ceiling", "The delay never grows past this. Absent means it is not capped.")),

		// A REFERENCE IS A BLOCK. The kind is on the Ref, so writing it into the value is
		// the same fact twice; and `<kind>/<name>` has nowhere to say WHICH namespace, so
		// it silently means "somewhere up from here".
		//
		// THE REF NAMES THE KIND AND NOT THE TYPE, because a Ref cannot say "a sync of
		// type queue" — so checkDeadLetterIsAQueue holds it, over the compiled object
		// where the target's type is a field on it. Same seam checkSyncOps uses, and the
		// same reason: the generic layer carries values at paths and has no word for a
		// type.
		String(k("dead_letter", "namespace"), IsNamespacePath(), InSection("delivery"), shown,
			Doc("Dead Letter Namespace", "Where that queue lives. Omitted means this queue's own scope, then its parents.")),
		String(k("dead_letter", "name"), IsVariableName(), Ref("sync"), InSection("delivery"), shown,
			Doc("Dead Letter Queue", "Queue that receives items which exhausted max-attempts.")),
	}
}

// logAttributes is what is KEPT. Both are Kafka's two axes and both mean drop-oldest —
// which is the difference between a log's ceiling and a bucket's: a full bucket refuses
// the write, a full log forgets the front.
//
// ABSENT MEANS FOREVER, and that is the safe direction rather than a silent choice: a
// ledger keeps everything, and an author who never read this line gets a log that
// remembers rather than one that quietly threw away the record they needed.
func logAttributes(t syncType) []*Attribute {
	shown := ShowWhen("type", t.name)
	k := func(rest ...string) AKey { return Key(append([]string{t.name}, rest...)...) }

	return []*Attribute{
		Duration(k("retention", "age"), PerEnv(), InSection("retention"), shown,
			Doc("Retention Age", "How long a record is kept, as a human string (e.g. \"168h\"). Absent means forever.")),
		Bytes(k("retention", "size"), PerEnv(), InSection("retention"), shown,
			Doc("Retention Size", "How much the log keeps before the oldest records are dropped, as a human string (e.g. \"1GB\"). Absent means it is not capped.")),
	}
}

type databaseType struct {
	name string
	// external is the servers that speak this model over the network. Closed for the
	// same reason the engine key is a key: each one is a wire protocol and a driver,
	// which is a release rather than a config value. Empty means tau's own engine is
	// the only thing here that speaks it.
	external []string
	// ops is what may be done to a database of this type. THE PER-KIND VERB RULE IS NOT
	// GIVEN UP BY FOLDING FOUR MODELS INTO ONE KIND: Ops() carries the union, because an
	// access block is loaded before anything knows which database it names, and
	// checkDatabaseOps holds every entry to the verbs of the type it actually reaches.
	ops Verbs
	// extra is what only this model has to say about itself — a vector's width and
	// metric. A field every type would carry belongs in databaseType.attributes.
	extra func(databaseType) []*Attribute
	doc   string
}

var databaseTypes = []databaseType{
	// PERSISTENT, REPLICATED, AND A SYSTEM OF RECORD — which is exactly what `cache` is
	// not, and the reason this type exists. The engine is pkg/kvdb: writes go to disk,
	// the storage service holds a replica on more than one node before a write is
	// acknowledged, and nothing expires. There is no `ttl` here and there will not be
	// one: an entry that vanishes on its own is what a cache is for, and a record that
	// deletes itself is not a record.
	//
	// WHAT `consensus: crdt` DOES NOT PROMISE, because the engine does not: concurrent
	// writes to ONE key are merged last-writer-wins, so one of them is discarded with
	// no error. There is no compare-and-swap and no transaction, and no kind here has
	// one to offer either: `consensus: raft` is as far as this vocabulary
	// goes, and it ORDERS the writes to a key rather than letting a caller make one
	// conditional on what is already there. The rule that falls out is the one
	// AGENTS.md states for every kvdb layout in this repository: never a key two
	// writers can write differently; put the discriminator in the key and let the
	// prefix scan be the index. It was every distributed database's story until the
	// field existed; it is now the story of the ones that ask for it by name.
	{
		name: "kv",
		ops:  Verbs{ReadOp("get"), ReadOp("list"), ReadOp("watch"), WriteOp("put"), WriteOp("delete")},
		doc:  "Keys and values, kept until something deletes them.",
	},

	// Documents fetched by id, and asked questions by their fields. tau's own engine for
	// it is not shipped yet — the same position `sql: distributed` is in, which is
	// musql — and the type is declared anyway because what an author writes does not
	// change when the engine lands.
	{
		name:     "document",
		external: []string{"mongo"},
		ops:      Verbs{ReadOp("get"), ReadOp("query"), WriteOp("put"), WriteOp("delete")},
		doc:      "Documents, fetched by id or found by a query over their fields.",
	},

	// `exec` is a write because the host cannot classify arbitrary SQL.
	{
		name:     "sql",
		external: []string{"postgres", "mysql"},
		ops:      Verbs{ReadOp("query"), WriteOp("exec")},
		doc:      "Tables, rows and SQL.",
	},

	// It was its own KIND until 2026-08-31, on the argument that its verbs are not a
	// database's — which is true and is a fact about the data model, not about the
	// resource. `query`/`exec` cannot describe `search`/`upsert`, and they do not have
	// to: verbs are per TYPE here, and the union an access block may spell is held to
	// the target's own type by checkDatabaseOps.
	{
		name:  "vector",
		ops:   Verbs{ReadOp("search"), WriteOp("upsert"), WriteOp("delete")},
		extra: vectorIndexAttributes,
		doc:   "Embeddings, searched by nearest neighbour. What an agent remembers, and what a retrieval step searches.",
	},
}

// engines is every key that may name this type's engine: tau's own first, then whatever
// external servers speak the model.
func (t databaseType) engines() []string { return append([]string{tauEngine}, t.external...) }

// DatabaseEngines is every engine each data model may name, keyed by the model.
//
// IT IS READ OFF THE TABLE RATHER THAN KEPT BESIDE IT, for the reason EnginesOf
// is: whatever has to answer "can this cloud open one of those" — a composer
// registering adapters, a test asking whether any engine is unadapted — must be
// asking the same question the compiler answers, or a fifth engine lands in the
// vocabulary and nothing notices that nothing opens it.
//
// tau's own engine is in every list, and it is the node's to register rather
// than a composer's: it is backed by the swarm the node is already on.
func DatabaseEngines() map[string][]string {
	out := make(map[string][]string, len(databaseTypes))
	for _, t := range databaseTypes {
		out[t.name] = t.engines()
	}
	return out
}

func databaseTypeNames() []string {
	out := make([]string, 0, len(databaseTypes))
	for _, t := range databaseTypes {
		out = append(out, t.name)
	}
	return out
}

func databaseTypeOf(name string) (databaseType, bool) {
	for _, t := range databaseTypes {
		if t.name == name {
			return t, true
		}
	}
	return databaseType{}, false
}

// databaseOps is every verb any type declares, deduplicated, in table order — what an
// access block may spell before anything has resolved which database it names.
//
// It PANICS when two types give one verb different effect classes. The effect is the
// join key every cross-cutting query groups by, so a `delete` that is a write on a kv
// and a read on a vector would make the union meaningless; catching it here makes it a
// build failure rather than a report that quietly answers wrong.
func databaseOps() []Verb {
	seen := map[string]Verb{}
	out := []Verb{}
	for _, t := range databaseTypes {
		for _, v := range t.ops {
			if had, ok := seen[v.Name]; ok {
				if had != v {
					panic(fmt.Sprintf("database: verb %q is declared two ways: %+v and %+v", v.Name, had, v))
				}
				continue
			}
			seen[v.Name] = v
			out = append(out, v)
		}
	}
	return out
}

// databaseAttributes is the kind's shape: the type switch, then one block per type.
func databaseAttributes() []*Attribute {
	out := []*Attribute{
		// THE TYPE IS NOT WRITTEN, it is the block that IS written — the same construct
		// `storage` uses for object-versus-streaming, and the reason a database cannot
		// be of two types or of none.
		String("type", Path(Either(databaseTypeNames()...)), Captures(), InSection("engine"),
			Doc("Type", "Which data model this is. It is the key everything else hangs off: a database is one block named after its type.")),
	}
	for _, t := range databaseTypes {
		out = append(out, t.attributes()...)
	}
	return out
}

func (t databaseType) attributes() []*Attribute {
	shown := ShowWhen("type", t.name)
	out := []*Attribute{
		String(Key(t.name, "engine"), Path(t.name, Either(t.engines()...)), Captures(), InSection("engine"), shown,
			Doc("Engine", fmt.Sprintf("Which engine backs it: %s. %s is tau's own, replicated across the swarm; the rest are external servers reached over the network.",
				strings.Join(t.engines(), ", "), tauEngine))),

		// THE SIZE IS TAU'S OWN ENGINE'S, and it is written inside that branch. An
		// external server's disk is not this project's to declare, and a key nothing
		// reads is the defect `notification.email` was deleted for.
		//
		// The path names `distributed` literally rather than matching the whole engine
		// set, so `postgres: { size: 1GB }` is an unknown key instead of a number filed
		// under a branch nobody took. A LITERAL PATH IS ALSO WHAT A LOWERING NEEDS:
		// interp's authoredPath has no single location for a path with a matcher in it,
		// so a field an agent's memory sets has to be reachable by fixed segments.
		// `Accessor()` BECAUSE A DECLARED PATH IS ADDRESSED BY ITS LEAF. A generated
		// client names a field after the last segment of the path it reads, and these
		// four paths end in the same word, so without an override the four are one
		// name and three of them are dropped in silence — see
		// TestNoTwoAttributesCollapseToOneAccessor, which is what holds this for
		// everything the per-type loop declares. The type is the discriminator, so it
		// is what the name carries.
		Bytes(Key(t.name, "size"), Path(t.name, tauEngine, "size"), Accessor(title(t.name)+"Size"),
			RequiredWhen(Key(t.name, "engine"), tauEngine), PerEnv(), InSection("engine"), shown,
			Doc("Size", "Maximum on-disk size, as a human string (e.g. \"1GB\").")),

		// WHAT HAPPENS TO TWO WRITES THAT ARRIVE AT ONE KEY AT ONCE. `crdt` is the
		// coordination-free replication tau already runs — pkg/kvdb, a Merkle-CRDT,
		// where every node takes a write the moment it arrives and concurrent writes to
		// one key merge last-writer-wins with nothing refused and nothing waited for.
		// `raft` is pkg/raft: one elected leader holds the key, so a write costs a round
		// trip through it, there is one total order, and nothing is silently discarded.
		// Both values name the mechanism they run, and both answer the only half of this
		// an author can be held to — what the store does with the second write.
		//
		// THE NAME IS THE QUESTION, NOT ONE OF THE ANSWERS. It was `crdt: bool`, so
		// `crdt: false` had to say what the database is NOT and the reader had to
		// supply what it is. `consensus` names the axis and each value answers it,
		// which is also what makes room for a third answer that is neither.
		//
		// REQUIRED, WITH NO DEFAULT — the same fix `worker.runtime` above carries, and
		// for a stronger reason than that one. A bool has an absent state and it is a
		// silent choice: absent-means-coordinated is the expensive answer on every
		// database nobody thought about, and absent-means-crdt is wrong for a ledger.
		// Whichever way it was set, the author who never read this line got the other
		// one. Required removes the question instead of answering it — the two answers
		// are different products the way a database's `engine` is, and a field that
		// decides whether a concurrent write survives is worth one line of saying
		// which.
		//
		// UNDER `distributed` AND NOWHERE ELSE, for the reason the size is: a Postgres
		// has whatever consistency Postgres has, so a claim on that branch is one this
		// project cannot honour. The literal path makes `postgres: { consensus: crdt }`
		// an unknown key rather than a value filed under a branch nobody took — which
		// is also why "required" here is RequiredWhen on the engine branch and not
		// `Required()`: an unconditional one is demanded on every type's block at once
		// (`shown` is presentation, and the engine does not read it), so a kv database
		// would owe an `sql/distributed/consensus` too.
		//
		// A BRANCH WITH FIELDS OF ITS OWN WOULD BE AN `Either()` SWITCH INSTEAD, the
		// shape `engine` above has. Neither value has any today, so this is the scalar
		// choice; a lease timeout or a replica count under `raft` is what
		// would move it, and that is the owner's call rather than this field's.
		//
		// TWO VALUES WHERE `cache.consensus` HAS THREE, AND THE MISSING ONE IS
		// `gossip`. A gossiped write can be dropped outright rather than merged, and a
		// dropped write in a system of record is a dropped fact that nothing
		// re-derives. A cache can take that — losing an entry costs a recomputation —
		// which is why the set is per kind rather than shared, and why this one will
		// not gain the third value.
		String(Key(t.name, "consensus"), Path(t.name, tauEngine, "consensus"), Accessor(title(t.name)+"Consensus"),
			InSet("crdt", "raft"), RequiredWhen(Key(t.name, "engine"), tauEngine),
			InSection("engine"), shown,
			Doc("Consensus", "What happens to concurrent writes to one key. crdt: every node takes a write the moment it arrives and the writes merge last-writer-wins, with nothing refused and nothing waited for. raft: one holder at a time, so a write costs a round trip and nothing is silently discarded.")),
	}

	if len(t.external) > 0 {
		// AN EXTERNAL ENGINE CARRIES ITS OWN CONNECTION — a host, a credential and a
		// tls mode, written here. There is no second resource standing between the
		// database and the server it is, which is what a `connector` was and is why
		// that kind is gone. The three facts every external engine needs are the
		// shared helper's; the fourth is a database's alone.
		out = append(out, externalEngineAttributes(
			[]StringMatch{t.name}, []string{t.name}, t.external, Key(t.name, "engine"), "engine", shown)...)
		out = append(out,
			String(Key(t.name, "database"), Path(t.name, Either(t.external...), "database"), PerEnv(), InSection("engine"), shown,
				Doc("Database", "The database name within that server. Defaults to this resource's name.")))
	}

	if t.extra != nil {
		out = append(out, t.extra(t)...)
	}
	return out
}

// vectorIndexAttributes is what an index needs and no other data model does. It sits in
// the type block BESIDE the engine key rather than inside it: the width of a vector is
// the embedding model's, not the engine's, and it stays the same number whichever engine
// stores it.
func vectorIndexAttributes(t databaseType) []*Attribute {
	shown := ShowWhen("type", t.name)
	return []*Attribute{
		// DIMENSIONS ARE FIXED AT CREATION and required for every vector database: an
		// index is built for a width, every vector in it has that width, and changing it
		// is a new index and a re-embed of everything. A default would be a guess at
		// which embedding model someone is using.
		Int(Key(t.name, "dimensions"), MinInt(1), RequiredWhen(Key(t.name, "engine"), t.engines()...),
			InSection("engine"), shown,
			Doc("Dimensions", "The width of every vector stored here, which the embedding model decides — 1536 for OpenAI's text-embedding-3-small, 1024 for Cohere v3. Fixed: changing it is a new index and a re-embed.")),

		// NO Default(), AND THAT IS FORCED RATHER THAN PREFERRED. A default fires
		// whenever the value is absent, whatever the rest of the document says — so
		// `Default("cosine")` on a field of the vector block writes `metric: cosine`
		// onto every kv, document and sql database in the project, which is a claim
		// about a branch nobody took. The vocabulary already had the answer beside it:
		// `tls` says what omitting it means and defaults nothing.
		//
		// The metric has to match how the model was TRAINED, which is why it is named
		// rather than tuned: cosine for a model trained on normalized embeddings, dot
		// for one that is not, euclidean where magnitude means something. Getting it
		// wrong does not error — it just ranks badly.
		String(Key(t.name, "metric"), InSet("cosine", "dot", "euclidean"), InSection("engine"), shown,
			Doc("Metric", "How closeness is measured. Omitted means cosine. Match the embedding model: cosine for normalized embeddings, dot where magnitude carries meaning, euclidean for raw distance.")),
	}
}

// A kind declares Ops() when something can reach it. A kind without them is not a
// target: nothing "uses" a cron, a zone or a component — a cron fires, a zone answers
// DNS, a component is built into a workload and named by its source. Their absence is
// the statement, not an oversight.
//
// V2Resources is the v2 resource vocabulary. The enum is closed: tcc's validation,
// completion, JSON-Schema emission and decompilation all need every type's shape at
// compile time. There is no open KIND any more — `connector.type` was the one free
// string and the kind is gone. What stays open is a workload's `egress` allowlist plus
// a `secret`, which is the same openness without asking the author to name a vendor as
// a resource first, and node.Register for services (resources.md, Extensibility
// decision).
var V2Resources = []*Node{

	// ---- compute ----

	// A container workload that listens on HTTP and is always addressable. The name
	// is free in v2 because the SCOPE container is `namespaces` here — `applications`
	// was v1's word for a scope, and tcc itself is agnostic about both.
	//
	// There is NO port. The platform assigns one, hands it to the workload as an
	// environment variable, and tunnels it over a p2p stream — so a port in the
	// config would be a number the workload is told to ignore, and a second place for
	// the real one to disagree with.
	//
	// There is NO replica count either, for the same reason as everywhere else: how
	// many instances run, and where, is the platform's job — it places, it reconciles,
	// it re-places when a node dies.
	//
	// And it declares no ops, so nothing reaches it directly. HTTP arrives through a
	// gateway route, which is the one way in; an application that could also be named
	// as a target would be a second, unrouted door into the same workload.
	//
	// `egress` is the per-workload ALLOWLIST; the node-wide denylist floor
	// (pkg/netguard) is not representable here and is never relaxable by a resource.
	DefineGroup("application",
		resource(
			commonAttributes(append(contentSource("code", "image", "git"), applicationAttributes()...)...),
			Nested(portsGroup(), variablesGroup(runLifetime, buildLifetime, bothLifetimes)),
			GroupDoc("A long-running container that listens on HTTP. Mount a volume for anything it must keep."), Icon("box"), Category("compute"), Placed(), Engines(containerEngine),
			secIdentity, secAccess, secConfig, secNetwork, secCode, secLimits,
			Resource("application", "Application", "Application", "application"),
		)),

	// A function is now only code plus limits. Nine attributes, one section, three
	// conditional sections and the domains foreign key left with the trigger block.
	//
	// An AUTHORIZER is one of these, not a kind of its own: it was declared with
	// exactly a function's fields and nothing else, and what distinguishes it is the
	// host interface it is built against — it is handed the operation and answers
	// yes or no — which is a property of the artifact, not of the config.
	DefineGroup("function",
		resource(
			wasmAttributes(),
			Nested(variablesGroup(runLifetime, buildLifetime, bothLifetimes)),
			GroupDoc("A serverless function. It declares no trigger: what may reach it is an inbound edge."), Icon("bolt"), Category("compute"), Placed(), Engines(wasmEngine),
			secIdentity, secAccess, secConfig, secNetwork, secCode, secLimits,
			Ops(WriteOp("invoke")), // invoke is write: a call may do anything the callee can
			Resource("function", "Function", "Function", "function"),
		)),

	// On-demand, long-running once triggered, not HTTP-listening. The only type
	// where the runtime tier is an authored field (resources.md §1.2).
	DefineGroup("worker",
		resource(
			wasmAttributesEntrypoint(RequiredWhen("runtime", wasmEngine),
				[]string{"component", "builtin", "git", "image"},
				// REQUIRED, WITH NO DEFAULT, and the default is what had to go rather
				// than a preference that was given up. `RequiredWhen` reads the
				// discriminator FROM THE DOCUMENT — requiredHere's own comment says an
				// absent one means the condition cannot hold — so a defaulted `wasm`
				// would have gated the entrypoint on a value the loader never sees, and
				// every worker that wrote no `runtime:` line would have stopped being
				// asked for one. Silently, which is the failure this fix is about.
				//
				// The two answers are also different products, the way a database's
				// `engine` is: one is a component this platform builds and runs, the
				// other an image somebody else built. A tier that decides which half of
				// the fields exist is worth one line of saying which.
				// THE SET IS THE KIND'S OWN Engines(), read from one slice. `runtime` is
				// the CHOICE between the tiers this kind declares, and declaring the
				// same two words twice is what an annotation and an InSet spelled apart
				// would be. `image:` is gone from here: which tier runs it and where the
				// artifact comes from are two questions, and the second one is
				// `source:`, which this kind already had.
				String("runtime", InSet(workerEngines...), Required(), InSection("code"), Doc("Runtime", "Which tier runs this worker: wasm or container.")),
			),
			Nested(variablesGroup(runLifetime, buildLifetime, bothLifetimes)),
			GroupDoc("An on-demand workload that runs to completion. Not HTTP-listening."), Icon("gear"), Category("compute"), Placed(), Completes(), Engines(workerEngines...),
			secIdentity, secAccess, secConfig, secNetwork, secCode, secLimits,
			Ops(WriteOp("invoke")),
			Resource("worker", "Worker", "Worker", "worker"),
		)),

	// Interactive container: exec, filesystem, a generated FQDN. idle_timeout is
	// separate from max_lifetime on purpose — an idle agent session and a runaway
	// one are different failures (resources.md §1.7).
	DefineGroup("sandbox",
		resource(
			commonAttributes(append(contentSource("code", "image", "git"),
				Duration(Key("lifetime", "idle"), PerEnv(), InSection("limits"), Doc("Idle Timeout", "Stop the sandbox after this long with no activity.")),
				Duration(Key("lifetime", "max"), PerEnv(), InSection("limits"), Doc("Max Lifetime", "Hard ceiling on how long a sandbox may live.")),
				egressAttribute(),
				Bytes(Key("execution", "memory"), Required(), PerEnv(), InSection("limits"), Doc("Memory", "Maximum memory the sandbox may use.")),
				cpuLimit(),
			)...),
			Nested(variablesGroup(runLifetime, buildLifetime, bothLifetimes)),
			GroupDoc("An interactive container with exec and filesystem access, for agent and build sessions."), Icon("terminal"), Category("compute"), Placed(), Engines(containerEngine),
			secIdentity, secAccess, secConfig, secNetwork, secCode, secLimits,
			Ops(WriteOp("exec"), ReadOp("read"), WriteOp("write")),
			Resource("sandbox", "Sandbox", "Sandbox", "sandbox"),
		)),

	// One field and one outbound edge — the thinnest resource in the vocabulary.
	// A schedule has to be nameable because an edge needs two addressable ends,
	// and that buys "what runs on a schedule" as a graph query (resources.md §1.6).
	DefineGroup("cron",
		resource(
			commonAttributes(
				String("schedule", Required(), InSection("identity"), Doc("Schedule", "Cron expression, e.g. \"0 3 * * *\".")),

				// A cron expression alone does not name a moment. "0 3 * * *" is a
				// different instant in every zone, and a fleet spread across regions
				// would fire it at whichever local time each node happened to hold —
				// so the schedule has to carry the zone it is written in.
				//
				// UTC by default because it is the only zone with no daylight saving:
				// a 2am job in a DST zone runs twice on one night a year and not at all
				// on another, and that is a property of the zone rather than of the
				// schedule, so the default should not have it.
				String("timezone", Default("UTC"), InSection("identity"),
					Doc("Timezone", "IANA zone the schedule is written in, e.g. \"Europe/Paris\". UTC by default — the only zone where every day has 24 hours.")),

				// What happens when the clock comes round and the last run is still
				// going. All three are right somewhere — skip for a reconcile that will
				// catch up on its own, queue for a billing run where every period must
				// happen exactly once, overlap for anything idempotent and cheap — so
				// the config says which rather than the runtime picking for everyone.
				//
				// `skip` by default because it is the only one that cannot make things
				// worse: queue turns a slow job into an unbounded backlog, and overlap
				// runs a job against itself.
				String(Key("on_overlap"), InSet("skip", "queue", "allow"), Default("skip"), InSection("firing"),
					Doc("On Overlap", "skip: drop this tick, the run in flight continues. queue: hold it and run when the current one ends. allow: start it anyway, alongside.")),

				// Every node in a fleet reading the same `0 3 * * *` fires at 03:00:00
				// exactly, which is a thundering herd at whatever the schedule points
				// at. A window spreads the starts across it.
				Duration("jitter", InSection("firing"),
					Doc("Jitter", "Spread each start randomly across this window, e.g. \"30s\". Absent means fire exactly on the tick.")),
			),
			GroupDoc("A schedule. It fires whatever it edges to; the target declares nothing."), Icon("clock"), Category("trigger"),
			secIdentity, secAccess,
			Section("firing", "Firing", "What happens when the clock comes round."),
			Resource("cron", "Cron", "Cron", "cron"),
		)),

	DefineGroup("component",
		resource(
			commonAttributes(contentSource("source", "git")...),
			// A component has no running process, so the only lifetime a variable of
			// its own can have is `build` — a private module registry's token, and
			// nothing else. variablesGroup takes the legal set for the same reason
			// contentSource takes the legal branches.
			Nested(variablesGroup(buildLifetime)),
			GroupDoc("A WASM component built from source, referenced as a workload's code. Component Model: it declares its imports and exports in WIT."), Icon("book"), Category("code"), Engines(wasmEngine),
			secIdentity, secAccess, secConfig, secSource,
			Resource("component", "Component", "Component", "component"),
		)),

	// Build -> zip -> storage -> serve, unchanged. What left: domains, paths and
	// the serving section. A website is served because a domain edges to it.
	// A built static site, and how it answers a path it does not have — which is the
	// one thing every static host has to be told and this kind was not asking.
	//
	// SPA AND 404 ARE THE SAME DOCUMENT WITH DIFFERENT STATUS, which is why `mode`
	// exists instead of a single "fallback document" field. A single-page app must
	// answer an unmatched path with index.html and **200**, because the router in the
	// page renders the route from the URL; a multi-page site must answer with its 404
	// page and **404**, because the page genuinely is not there. Same file in both
	// cases if you write it that way, opposite meanings — and the wrong one is
	// invisible in a browser and obvious to a crawler, a monitor, or anything that
	// reads status codes.
	//
	// It is the same pair every static host encodes: S3's IndexDocument and
	// ErrorDocument, Netlify's `/* /index.html 200`, Cloudflare Pages' "single-page
	// app" switch, Vercel's rewrites.
	DefineGroup("website",
		resource(
			commonAttributes(append(contentSource("source", "git"),
				String(Key("serve", "index"), Default("index.html"), Delegable(), InSection("serving"),
					Doc("Index Document", "What a request for a directory returns, e.g. \"/docs/\" -> /docs/index.html.")),
				String(Key("serve", "mode"), InSet("static", "spa"), Default("static"), Delegable(), InSection("serving"),
					Doc("Mode", "static: an unmatched path returns the not-found document with 404. spa: it returns the index document with 200, because the page's own router renders the route.")),
				String(Key("serve", "not_found"), Delegable(), InSection("serving"), ShowWhen(Key("serve", "mode"), "static"),
					Doc("Not Found Document", "Returned with 404 for a path the site does not have. Omitted means a bare 404.")),
			)...),
			Nested(variablesGroup(buildLifetime)),
			GroupDoc("A static website built from a git repository. What serves it is an inbound domain edge."), Icon("globe"), Category("compute"), Engines(staticEngine),
			secIdentity, secAccess, secConfig, secSource,
			Section("serving", "Serving", "What the site returns for a path it does not have."),
			Resource("website", "Website", "Website", "website"),
		)),

	// ---- data ----

	// A cache. It never gets CAS, and the reason is its own verb list rather than
	// somewhere else to go: they are get, list, watch, put and delete, and nothing in
	// this vocabulary makes a write conditional on what is already at the key. It is
	// NOT a database: entries have a lifetime, losing one costs a recomputation, and
	// nothing here is a system of record.
	//
	// WHERE IT SITS ON THE CAP LINE IS `consensus`'s ANSWER, NOT THE KIND'S. It used to
	// be the kind's — "eventually consistent, AP" was written here and in the GroupDoc,
	// and it was true of everything this kind could be. It is not true of a
	// `raft` cache, which costs a round trip and cannot complete a write on the
	// side of a partition that has lost the holder. The one thing that IS true of all
	// three is what the rest of this paragraph says: an entry is not a record.
	DefineGroup("cache",
		resource(
			commonAttributes(append(instanceAttributes(),
				Bytes(Key("size"), Required(), PerEnv(), InSection("keys"), Doc("Size", "Maximum size, as a human string (e.g. \"32MB\").")),
				Duration(Key("ttl"), PerEnv(), InSection("keys"), Doc("Time-To-Live", "Entry lifetime. A floor, not a guarantee: peers may disagree about existence for up to the clock skew.")),

				// WHERE THE DATA LIVES. Whether every peer keeps its own copy, or the
				// data sits on one node and the others fetch it.
				//
				// A BOOL, WHERE `consensus` BELOW IS A REQUIRED ENUM, AND THE
				// DIFFERENCE IS THE POLARITY OF THE ABSENT STATE rather than a taste
				// for one construct. Here the absent value is node-local, which is the
				// cheap answer and the obvious one, and replication is what an author
				// opts in to. `tls` is the same shape pointed the other way — safe by
				// default, explicit to weaken — and this is safe-and-cheap by default,
				// explicit to widen.
				//
				// NOT WHO MAY READ IT, WHICH IS THE ACCESS BLOCK'S QUESTION and is
				// answered per resource and per verb by whatever reaches in. A cache
				// every peer may read can still be stored on one node and fetched over
				// the network, or replicated so each peer reads locally.
				//
				// NO MECHANISM AND NO REPLICA COUNT. How copies are kept is the
				// platform's to change, and a field for something nothing performs is
				// the inert key this vocabulary deletes things for.
				//
				// HOW IT MEETS `consensus`, STATED BECAUSE IT IS NOT SETTLED. The two
				// are not independent: `crdt` and `gossip` both mean every peer takes
				// the write where it lands, so both IMPLY a copy per peer, and only
				// under `raft` is "where do the bytes live" still a question
				// — one holder for writes, with or without replicas to read from. So
				// this field carries information on one branch of three, and on the
				// other two it either restates what the value already said or
				// contradicts it. Whether that makes it redundant, or a second axis
				// worth keeping for the branch where it bites, is the owner's call.
				// Neither refusing the pair nor deleting the field is done here: the
				// first would decide the question in the schema and the second would
				// drop a field one commit old. It is written down instead.
				Bool(Key("replicated"), InSection("keys"),
					Doc("Replicated", "Whether every peer keeps its own copy of this cache. Omitted means the data lives on one node and other peers reach it over the network. Only a question under raft consensus: crdt and gossip both put a copy on every peer.")),

				// WHAT HAPPENS TO TWO WRITES THAT ARRIVE AT ONE KEY AT ONCE. The same
				// question `database.consensus` asks, named the same way and answered
				// from a wider set, and the author never names the mechanism.
				//
				//	raft	one holder of the key at a time, so a write costs a round
				//		trip and nothing is silently discarded
				//	crdt	every node takes the write the moment it arrives and
				//		concurrent writes merge last-writer-wins: nothing is refused
				//		and nothing is waited for, and the write that loses the merge
				//		is gone with no error
				//	gossip	the write is taken locally and spread best-effort, so an
				//		entry can fail to arrive or be dropped outright
				//
				// THREE VALUES WHERE A DATABASE HAS TWO, AND THE DIFFERENCE IS NOT
				// SLOPPINESS. `gossip` is eventually consistent AND LOSSY: it can drop
				// an entry that no merge ever saw. That is affordable exactly where
				// losing an entry costs a recomputation, which is the line this kind's
				// own GroupDoc already draws — and it is unaffordable in a system of
				// record, where a dropped write is a dropped fact and no re-derivation
				// gets it back. So `database` will not gain this value and `cache` has
				// it because the whole premise of the kind is that a miss is cheap.
				//
				// REQUIRED, AND THAT IS WHERE IT PARTS FROM `replicated` ABOVE.
				// `replicated`'s silence is defensible because absent means nothing
				// extra happens — the data sits where it was written, which is what
				// doing nothing gets you. There is no such value here: two writes
				// arrive at one key whether or not anybody chose, and the store does
				// one of these three things. A field whose absence still has an answer
				// is a choice made by silence, and whichever answer the silence carried
				// would be wrong for somebody who never read the line — `gossip` puts
				// lossy on every cache nobody thought about, `raft` puts a
				// round trip on all of them. Required removes the question rather than
				// answering it, which is the same fix `database.consensus` and
				// `worker.runtime` carry, and the same defect the original `crdt: bool`
				// had.
				//
				// AT THE DOCUMENT ROOT, beside `size` and `ttl`, because this kind has
				// no engine axis and none is invented for it. A database writes its
				// consensus inside `<type>/distributed/` because an external Postgres
				// has whatever consistency Postgres has and a claim on that branch is
				// one the project cannot honour. Nothing external backs a cache, so
				// there is no branch the answer could be wrong on.
				//
				// EVERY VALUE NAMES ITS MECHANISM, by the same rule the database
				// states. All three are words the ecosystem already carries, so the set
				// reads without the Doc string; the cost is that swapping the log out
				// from under `raft` would make a config lie, and it is taken rather
				// than hidden behind a description.
				String(Key("consensus"), InSet("raft", "crdt", "gossip"), Required(), InSection("keys"),
					Doc("Consensus", "What happens to concurrent writes to one key. raft: one holder at a time, so a write costs a round trip and nothing is silently discarded. crdt: every node takes the write the moment it arrives and the writes merge last-writer-wins, with nothing refused and nothing waited for. gossip: the write is taken locally and spread best-effort, so an entry may fail to arrive or be dropped — cheapest, and fine where losing one costs a recomputation.")),

				// A REFERENCE, never the material. "key material or reference" let the
				// same field be either, and the safe reading was the one nothing enforced.
				String(Key("encryption", "key", "namespace"), IsNamespacePath(), InSection("encryption"),
					Doc("Namespace", "Where that secret lives. Omitted means this resource's own scope, then its parents.")),
				String(Key("encryption", "key", "name"), IsVariableName(), Ref("secret", Inbound()), NoAccessors(), InSection("encryption"),
					Doc("Encryption Key", "Secret holding the key entries are encrypted with, as \"secret/<name>\".")),
			)...),
			GroupDoc("A key-value cache served to the project's peers, with a lifetime on every entry. Not a system of record: losing an entry costs a recomputation, never a fact."), Icon("bolt"), Category("data"), Placed(),
			secIdentity, secAccess,
			Section("keys", "Keys", "Capacity, lifetime, where the data lives, and what happens to two writes at one key."),
			secInstances,
			Section("encryption", "Encryption", "Encryption at rest."),
			Ops(ReadOp("get"), ReadOp("list"), ReadOp("watch"), WriteOp("put"), WriteOp("delete")),
			Resource("cache", "Cache", "Cache", "cache"),
		)),

	// A DATABASE, AND THE ONLY KIND HERE THAT IS A SYSTEM OF RECORD. Four data models
	// live under it — see databaseTypes above for what the two switches are and why the
	// wrong pair cannot be written.
	//
	// `cache` is the one neighbour that is deliberately NOT a type here, and the line
	// between them is durability rather than shape: a cache entry has a lifetime, losing
	// one costs a recomputation, and nothing in it is a record. `kv` is the same pair of
	// verbs over a store that keeps what it is given.
	DefineGroup("database",
		resource(
			commonAttributes(append(databaseAttributes(), instanceAttributes()...)...),
			GroupDoc("A database: keys and values, documents, tables or vectors — on tau's own replicated engine, or on an external server reached over the network."), Icon("database"), Category("data"), Placed(),
			secIdentity, secAccess,
			Section("engine", "Engine", "Which data model it is, which engine backs it, and its settings."),
			secInstances,
			// THE UNION, and it is not a hole in the per-kind verb rule. An access block
			// is loaded before anything has resolved which database it names, so this is
			// the widest set the loader can hold it to; checkDatabaseOps is what holds
			// the entry to the verbs of the type it actually reaches, over the compiled
			// object where the target is known. Same seam CheckLoweredOps already uses:
			// the generic layer carries values at paths and has no word for a verb.
			Ops(databaseOps()...),
			Resource("database", "Database", "Database", "database"),
		)),

	// NO `sql` KIND, deliberately. It was declared here with exactly one authored
	// field — a capacity number — because everything else about it (one writer,
	// SQLite file format, WAL-mode refusal, savepoints) is runtime behaviour, not
	// configuration. The thing a user would actually author is the schema and its
	// migrations, and that is undesigned and blocked on musql's differential
	// harness against real C SQLite. A kind with nothing to author is the same
	// defect as `notification.email`: a lie with a type signature. It comes back
	// when there is something to write in it.

	DefineGroup("storage",
		resource(
			commonAttributes(append(append([]*Attribute{
				String("type", Path(Either(storageTypes...)), Captures(), InSection("storage"), Doc("Type", "Storage kind: object or streaming (the key selects the type block).")),
			}, instanceAttributes()...),
				Bool(Key("object", "versioning"), NoSetter(), InSection("storage"), Doc("Versioning", "Keep historical versions of objects (object storage).")),
				Duration(Key("streaming", "ttl"), Field("Ttl"), Accessor("TTL"), NoSetter(), PerEnv(), InSection("storage"), Doc("Time-To-Live", "Lifetime for streamed entries. A floor, not a guarantee.")),
				Bytes(Key("size"), Path(Either(storageTypes...), "size"), Required(), PerEnv(), InSection("storage"), Doc("Size", "Maximum storage size, as a human string (e.g. \"1GB\").")),
			)...),
			GroupDoc("Object or streaming storage, content-addressed, served to the project's peers."), Icon("bucket"), Category("data"), Placed(),
			secIdentity, secAccess,
			Section("storage", "Storage", "Storage kind and capacity."),
			secInstances,
			Ops(ReadOp("get"), ReadOp("list"), ReadOp("head"), WriteOp("put"), WriteOp("delete")),
			Resource("storage", "Storage", "Storage", "storage"),
		)),

	// A quota'd local directory whose contents are continuously checkpointed into the
	// CAS as a snapshot chain. The chain IS the backup product.
	//
	// IT CAN BE SHARED, AND THE FILESYSTEM IS WHAT ARBITRATES. There is no lease field
	// here and there never was one: "single-writer" and "leased" were prose asserting a
	// constraint nothing in the schema expressed, which is the shape this vocabulary
	// deletes fields for pointed the other way — a claim with no mechanism under it.
	//
	// The position, stated so it is not re-argued: POSIX already answers concurrent
	// access. `flock`, `fcntl` record locks, atomic rename, `O_APPEND` — a filesystem
	// that offers POSIX semantics offers those, and a workload that needs to coordinate
	// with another one has them. A lease bolted on above would be a SECOND answer to a
	// question the layer below already answers, and a weaker one: it is coarser (a
	// whole directory, where a lock is a byte range), it cannot see the operations it
	// would be ordering, and a holder still has to use the POSIX primitives correctly
	// for anything finer than "nobody else at all". So two workloads mounting one
	// volume read-write is a shape this vocabulary permits, and so is one workload the
	// platform may run ten instances of. Whether their access is CORRECT is theirs to
	// arrange, with the tools the filesystem gives them.
	//
	// `mount-ro` and `mount-rw` stay two verbs. Read-only and read-write are still
	// different asks — one of them cannot damage anything — and an accessor saying
	// which is the same statement every other kind's ops make.
	//
	// rpo-budget and on-budget-exceeded exist because RPO is NOT the checkpoint
	// interval — it is the age of the last fully uploaded, pointer-committed
	// snapshot, and it is unbounded while checkpoints cannot keep up with churn.
	// An unbounded RPO with no ceiling is silent data loss with a dashboard
	// (resources.md §2.4).
	DefineGroup("volume",
		resource(
			commonAttributes(
				Bytes(Key("quota"), Required(), PerEnv(), InSection("storage"), Doc("Size", "Hard quota for the directory, as a human string (e.g. \"10GB\").")),
				Duration(Key("checkpoint", "interval"), Default("60s"), PerEnv(), InSection("checkpoint"), Doc("Checkpoint Interval", "How often the volume is walked, chunked and uploaded.")),
				// WHAT A RESTORE ACTUALLY GIVES BACK, WITH MORE THAN ONE WRITER: a
				// CRASH-CONSISTENT moment, not a quiesced one. A checkpoint walks the
				// directory, chunks it and uploads it while writers keep writing, so a
				// snapshot can hold one file from before an operation and its neighbour
				// from after — the same guarantee a filesystem-level backup of any live
				// directory has, and the same one a machine that lost power leaves
				// behind. It is enough for anything that recovers on its own (a journal,
				// a WAL, a log a reader re-reads) and it is not a consistent point in
				// time across several writers, which nothing here promises and nothing
				// here can: quiescing would mean stopping every writer for the length of
				// a walk. Worth saying beside the budget, because the budget is where an
				// author reasons about recovery and the old text implied a single writer,
				// which would have made a restore sound more coherent than it is.
				Duration(Key("checkpoint", "rpo_budget"), PerEnv(), InSection("checkpoint"), Doc("RPO Budget", "Ceiling on the age of the last committed snapshot before on-budget-exceeded fires. What a snapshot holds is a crash-consistent moment — writers are not stopped for the walk — so a restore gives back what a power cut would.")),
				String(Key("checkpoint", "on_budget_exceeded"), InSet("alert", "freeze-writes"), Default("alert"), InSection("checkpoint"), Doc("On Budget Exceeded", "What to do when the committed-snapshot age passes rpo-budget. Nothing happens without a budget to pass.")),
			),
			GroupDoc("A quota'd directory with full POSIX semantics, checkpointed into the CAS. More than one workload may mount it read-write; the filesystem arbitrates. Bind mounts are refused."), Icon("drive"), Category("data"), Placed(),
			secIdentity, secAccess,
			Section("storage", "Storage", "Capacity."),
			Section("checkpoint", "Checkpoint", "Durability out of band from the write path. A snapshot is crash-consistent, not quiesced."),
			Ops(Mounts(ReadOp("mount-ro")), Mounts(WriteOp("mount-rw"))),
			Resource("volume", "Volume", "Volume", "volume"),
		)),

	// A declaration and a reference, never a value. The value lives outside git in
	// the secrets service, keyed <project>/<environment>/<key>, so rotation needs no
	// push. Read access is an edge with one verb; there is no write verb from a
	// workload, ever (resources.md §2.5, edges.md §3.3).
	DefineGroup("secret",
		resource(
			commonAttributes(
				String("source", InSet("value", "oidc"), Default("value"), InSection("identity"), Doc("Source", "value: stored in the secrets service. oidc: minted per request as a short-lived token, nothing to rotate or leak.")),
				Duration("rotation", InSection("identity"), Doc("Rotation", "How often the value is expected to rotate. Advisory; rotation itself never requires a new release.")),
			),
			GroupDoc("A named reference to a secret value held outside git. The config never carries the value."), Icon("key"), Category("data"),
			secIdentity, secAccess,
			// There is no write verb from a workload, ever — so an arrow INTO a secret
			// draws a flow that cannot happen. What moves is the value, from the secret
			// to whoever named it, which is what Arrives states.
			Ops(Arrives(ReadOp("get"))),
			Resource("secret", "Secret", "Secret", "secret"),
		)),

	// NO `entity` KIND, and this is the note that stops it coming back as a field at a
	// time. It was a named, durably-stored, single-writer compute instance — a WASM
	// entrypoint, a per-instance storage ceiling, a `durable` bit journalling every host
	// call so an interrupted turn replays, and a quorum lease as the fence. Read as a
	// list of fields it is defensible. Read as a claim it is not: every one of those
	// fields describes HOW A RUNTIME RUNS SOMETHING, and none of them is a shape a
	// compiler can hand to a deployer.
	//
	// ORCHESTRATION IS RUNTIME. That is the whole of it. Where a conversation lives, when
	// a turn is durable, which replica holds the lease this second, what replays after an
	// interruption — these are decided while the work runs, by the thing running it, and
	// they change between one invocation and the next. Config states what EXISTS and what
	// may reach it. A kind whose content is entirely how execution is sequenced is the
	// vocabulary answering a question it is not the one asked, and the answer goes stale
	// the moment the runtime picks differently.
	//
	// WHAT ARGUED FOR IT, and why each argument moved out:
	//
	//   - SESSION STATE. `agent.sessions.size` lowered into one, so a conversation was a
	//     named instance with a fence around it. A thread is the loop's, at run time —
	//     see the note where that field stood.
	//   - A LINEARIZABLE COUNTER. `database` and `cache` each cited this kind as the place
	//     compare-and-swap lives. It never was: an entity's verbs were `get` and `call`,
	//     and a safe counter was always code somebody wrote INSIDE one. Those two notes
	//     pointed at a runtime capability and called it a vocabulary one.
	//   - PER-KEY ORDERING. `queue`'s doc sent an author here for it. `consensus:
	//     raft` is what orders writes to a key, it is declared on the kinds that
	//     have it, and a second spelling of the same fact is the parallel table §10 is
	//     about.
	//   - "A DURABLE, ADDRESSABLE THING WITH CODE IN IT". That is a `function` with the
	//     state it reaches declared beside it, which is every other workload here.
	//
	// WHAT IT COSTS, said out loud, and it is two things rather than one.
	//
	// The first is compare-and-swap: nothing in this vocabulary offers it — no kind
	// declares a conditional-write verb, and none did before either. The vocabulary is
	// SILENT on it rather than pointing somewhere, which is the honest state and is why
	// the three notes that pointed here were rewritten rather than repointed.
	//
	// The second is larger and is the one to notice. Deleting the kind decided that the
	// runtime sequences execution; it did NOT decide that no such thing runs. If a durable
	// addressable instance does exist out there, a workload has no way to say it reaches
	// one — the access block admits the kinds that declare verbs and there is no
	// eighteenth slot — so there is no ops grant, no dependency edge, no blast radius in
	// the explorer and nothing an authorizer can be asked about. A runtime that grew one
	// and a config that cannot name it is a real gap, not a tidy one.
	//
	// WHAT WOULD BRING SOMETHING BACK is deliberately not answered here. A conditional-write
	// verb on a kind that already stores something is one shape; a compute kind that holds
	// its own state is another, and it is sized as work elsewhere. Which one — or neither —
	// is §11's, and a note is not the place to settle it. What this note fixes is only that
	// the vocabulary stopped naming a kind it does not have.

	// COORDINATION, and the type says which shape of it. A queue hands work over
	// exactly once; an append-only log fixes what order things happened in. They were
	// two kinds until 2026-09-05, on the argument that their verbs are not each other's
	// — which is true and is a fact about the primitive, not about the resource. Verbs
	// are per TYPE here, and the union an access block may spell is held to the
	// target's own type by checkSyncOps, exactly as `database` does for its four data
	// models.
	//
	// WHAT THIS BUYS is the next one: a lock, a counter, a barrier each become a row in
	// syncTypes rather than a kind with its own identity, access, placement and quota
	// machinery. None of them is here — a type with nothing to author is the defect
	// `notification.email` was deleted for — but the slot they land in now exists.
	DefineGroup("sync",
		resource(
			commonAttributes(syncAttributes()...),
			GroupDoc("Coordination between workloads: a queue that hands work over exactly once, or an append-only log that fixes what order things happened in. What these have in common is that the resource decides who wins — which is what a stream and a bus deliberately do not."), Icon("list"), Category("data"), Placed(),
			secIdentity, secAccess,
			Section("sync", "Sync", "Which primitive it is, and what happens to concurrent writes."),
			Section("delivery", "Delivery", "Claim, retry and dead-letter behaviour."),
			Section("retention", "Retention", "What is kept, and for how long."),
			// THE UNION, and it is not a hole in the per-kind verb rule — see syncOps.
			Ops(syncOps()...),
			Resource("sync", "Sync", "Sync", "sync"),
		)),

	// ---- routing and naming ----

	// A zone YOU own and delegated here. The cloud also serves a zone of its own —
	// `<generated>.<cloud fqdn>` — which is never declared, because neither half of it
	// is the project's to choose: see hostAttributes.
	//
	// A DNS zone this cloud is authoritative for. Separate from `gateways` because it
	// answers a different question: a gateway says which workload serves a path under
	// a name, a zone says what the name resolves to AT ALL — MX for mail, TXT for SPF
	// and domain verification, CNAME for a third-party service, A for a box that is
	// not tau. None of that is routing, and none of it should force a second DNS
	// provider when dns is already an authoritative server.
	//
	// `records` is a list of ZONE-FILE PRESENTATION LINES — "www 300 IN A 1.2.3.4" —
	// and that is a deliberate trade with a stated cost. It is the interchange format
	// every provider imports and exports, so a Route 53 or Cloudflare zone moves in
	// and out without translation, and it is expressible in the DSL today. What it
	// gives up is per-field validation, completion and error locations on the name,
	// type, TTL and value — which a record as a nested object would give back, and is
	// the same missing shape the route rules need.
	//
	// A gateway's fqdn inside a zone this cloud serves needs no record here; that one
	// is produced, not authored.
	DefineGroup("zone",
		resource(
			commonAttributes(
				String("fqdn", IsFqdn(), Required(), Field("Fqdn"), Accessor("FQDN"), EmitValidation("zone", "dns"), InSection("identity"), Doc("Zone", "The zone apex this cloud answers for, e.g. \"example.com\". Delegate it with NS records at your registrar. The cloud's OWN zone is not declared here — it is generated, and a resource reaches it with host.generated.")),
				// A FILE, A RESOLVER, OR BOTH -- and NOT a switch between them,
				// which is what this was and why it was wrong. The two answer
				// different halves of one zone: a file is what an author knows
				// when they deploy, including the NS records that DELEGATE a
				// subtree somewhere else, and a resolver is for the names whose
				// answer is not known until they are asked. A zone that has both
				// serves the file and falls back to the function, which is the
				// commonest real shape: a handful of fixed records, a delegated
				// subdomain, and one label per tenant that no file could list.
				//
				// So neither is Required and neither excludes the other. A zone
				// with NEITHER is still a zone: it declares an fqdn this cloud
				// is authoritative for, and what it answers is what it routes --
				// which is every zone in the corpus today.
				// THE LINES THEMSELVES, which is what the node serves today.
				//
				// A file is the shape an author wants and it is not yet the shape
				// anything reads: the lines have to reach the compiled object, and
				// nothing in the lowering vocabulary reads a file -- Sets takes Attr,
				// Text and Concat, all of which are values the object already holds.
				// Until that term exists, or the build emits the file as an artifact
				// this node fetches, a zone answers with what is written here.
				StringSlice("records", InSection("records"),
					Doc("Records", "Zone-file lines, e.g. \"www 300 IN A 1.2.3.4\", \"@ 3600 IN MX 10 mail.example.com.\".")),

				String("file", IsRepoPath(), Inlined("records"), InSection("records"),
					Doc("Zone File", "Path to a zone file in this repository, e.g. \"dns/example.com.zone\". Served with this zone's fqdn as $ORIGIN. Use NS records in it to delegate a subtree elsewhere.")),

				// ASKED ONLY FOR WHAT THE FILE DOES NOT ANSWER, so adding one to
				// a zone that has a file cannot change an answer the file
				// already gives -- a record an author can read in their own
				// repository is not overridden by code. The precedence is that
				// way round and not the other for the same reason a route's
				// authorizer beats the gateway's: the more specific statement
				// wins, and a file is as specific as a statement gets.
				String(Key("resolver", "name"), IsVariableName(), Ref("function", ByName(), Serves()), InSection("records"),
					Doc("Resolver", "A function asked for names this zone's file does not answer. Built against the dns-resolver world. Without a file it answers the whole zone.")),
				String(Key("resolver", "namespace"), IsNamespacePath(), InSection("records"),
					Doc("Resolver Namespace", "Where that function lives. Omitted means this resource's own scope, then its parents.")),
			),
			GroupDoc("A DNS zone served authoritatively by this cloud: a zone file you own, and a function asked for the names it does not carry."), Category("internet"), Icon("dns"),
			secIdentity, secAccess,

			Section("records", "Answers", "What this zone answers with: a zone file, or a function."),
			Resource("zone", "Zone", "Zone", "zone"),
		)),

	// A gateway is a NAME, the policy at that name, and ONE route. That last part is
	// the limitation to know about, and this comment used to describe the opposite.
	//
	// The intended shape is the Kubernetes Gateway API's: an HTTPRoute names its parent
	// through `parentRefs` and the Gateway's listener carries `allowedRoutes`, so an
	// infrastructure team owns the gateway and application teams add routes without
	// editing a file they do not own — and many routes merge onto one gateway.
	// https://gateway-api.sigs.k8s.io/reference/api-types/httproute/
	//
	// What is here is the other thing: the route is embedded, so a site with five paths
	// is five gateway documents repeating host, certificate, cors and rate-limit, with
	// nothing saying which of them owns the name. A route as a named object is that
	// gap; mounting one gateway beneath another is the workaround
	// and it does not resolve the policy duplication.
	//
	// `fqdn` is optional, and that is what collapsed the old `domains` kind into this
	// one: a named gateway answers on the internet, an unnamed one is reachable only
	// by being routed to from another gateway, which is what makes gateways compose.
	// A gateway is a NAME, the policy at that name, and what it serves.
	//
	// The handler is a switch on the target KIND, which is a closed set, so it is the
	// same construct `storages.type` and `git-provider` already use: an Either() in the
	// attribute path with Key() capturing which branch was taken, and the branch's own
	// fields hanging off the same Either.
	//
	//	handler:
	//	    function:                 # or application / website / gateway / mcp
	//	        namespace: /shop/eu   # omitted means the project root
	//	        name: checkout
	//
	// `fqdn` is optional, and that is what collapsed the old `domains` kind into this
	// one: a named gateway answers on the internet, an unnamed one is reachable only by
	// being mounted beneath another (a handler whose kind is `gateway`).
	DefineGroup("gateway",
		resource(
			// A gateway with no host block is reachable only by being mounted beneath
			// another one, which is what collapsed the old `domains` kind into this one.
			append(append(append(append(append(append(identityAttributes(), hostAttributes("naming")...),
				routerAttributes("route", "gateway-router", "router")...),
				routerAttributes("handler", "gateway-router", "route", "router")...),
				// EITHER A ROUTER OR ROUTES, and inside a route either a handler or a
				// router of its own. Both are the switch this vocabulary already has:
				// the Either lists the alternatives, Captures() records which was
				// written, and the loader refuses a document that opens two of them or
				// none — which is the whole of "exactly one", with nothing to check
				// afterwards.
				String(Key("routing"), Path(Either("route", "router")), Captures(), Required(), InSection("route"),
					Doc("Routing", "route: this gateway serves one path, declared below. router: a function decides where everything goes, and there are no static routes.")),
				// NOT Required: the switch inside an optional block is demanded only when
				// the block is there, which is what parentExists answers. Required here
				// would insist on a route even when a router routes everything.
				String(Key("route", "target"), Path("route", Either("handler", "router")), Captures(), InSection("handler"),
					Doc("Target", "handler: where the request ends. router: a function that says where it should go.")),
				String(Key("certificate", "source"), InSet("generated", "provided"), Default("generated"), Field("CertSource"), InSection("naming"), ShowWhen(Key("host", "kind"), "zone"), Doc("Certificate", "generated: issued on demand for this name. provided: from the secret below. A host under the cloud's own zone needs neither — that zone has a wildcard.")),
				certificateSecret("naming")[0], certificateSecret("naming")[1],

				// THE NESTING IS DECLARED BY Path, never spelled into the key. The register is
				// a CRDT and the compiled object is a tree, so route -> handler ->
				// namespace has to be three mergeable levels; a key that spelled the
				// path itself would be one opaque leaf that can only be replaced whole,
				// which is why "/" in a key is a build failure (engine/keys.go).
				String(Key("route", "path"), RequiredWhen(Key("routing"), "route"), InSection("route"), Doc("Path", "The path served, e.g. \"/checkout\" or \"/\". Required when this gateway declares a route; a gateway with a router has no paths of its own.")),
				String(Key("route", "match"), InSet("prefix", "exact", "regex"), Default("prefix"), InSection("route"), Doc("Match", "How the path is compared: prefix, exact, or regex.")),
				// A handler mounted at /docs receives /docs/x unless this is set. For an
				// APPLICATION that is usually right — it routes on the full path and may
				// need the prefix to build links. For a WEBSITE it is usually wrong: the
				// site was built for /, so /docs/x is a file it does not have, and every
				// page 404s while the config looks correct.
				//
				// False by default because passing the request through unchanged is the
				// answer that surprises nobody; the audit reports a website mounted at a
				// prefix without it.
				Bool(Key("route", "strip_prefix"), InSection("route"),
					Doc("Strip Prefix", "Remove the matched path prefix before handing the request on. A website mounted anywhere but / usually wants this.")),
				StringSlice(Key("route", "methods"), EachInSet(HttpMethods...), InSection("route"), Doc("Methods", "HTTP methods answered. Omit for any. GET/HEAD/OPTIONS read, the rest write.")),

				// the switch: which kind handles it, and where that resource lives
				// ONE HANDLER, AND NO WEIGHT. A route names its target and sends
				// everything it matches there. The Gateway API gives each backendRef a
				// `weight`, which is how every canary and blue/green rollout in the
				// category is expressed, and there is nowhere here to put one — the
				// handler is a single block, not a list with a per-target field.
				// Traffic splitting is not expressible in this vocabulary.
				String(Key("route", "handler", "kind"), Path("route", "handler", Either(handlerKinds...)), Captures(), Field("HandlerKind"), InSection("handler"), Doc("Handler", "Which kind serves this path (the key selects the handler block).")),
				String(Key("route", "handler", "namespace"), Path("route", "handler", Either(handlerKinds...), "namespace"), InSection("handler"), Doc("Namespace", "Path to the namespace holding the handler, e.g. \"/shop/eu\". Omitted means the project root.")),
				// REQUIRED ONCE THERE IS A HANDLER. `handler: {agent: {}}` named a kind
				// and no resource, and compiled: a route asserting a target that does
				// not exist, which is the same thing as a proxy forwarding to nothing.
				// Not Required() outright, because a gateway whose routing is a router
				// has no handler block at all — the condition is which branch of the
				// route's own switch was taken. `stream` says it unconditionally,
				// because a stream always serves something.
				String(Key("route", "handler", "name"), Path("route", "handler", Either(handlerKinds...), "name"), RequiredWhen(Key("route", "target"), "handler"), IsVariableName(), Ref("", KindFrom(Key("route", "handler", "kind")), ByName(), Serves()), InSection("handler"), Doc("Name", "The handler resource's name within that namespace.")),
				String(Key("route", "handler", "port"), Path("route", "handler", Either(handlerKinds...), "port"), IsVariableName(), InSection("handler"), Doc("Port", "Which of the handler's ports to reach, by name. A workload listening on more than one has to be told which, and the number is never written here — the platform assigns the external one, and there may be several instances of the workload on one host.")),

				// The name this gateway answers on, whatever it turns out to be. A
				// generated host is `<label>.<project>.<cloud fqdn>` and the cloud's
				// half is not knowable when the config is written, so an author cannot
				// put their own origin in the list below — which made cors usable with
				// a zone you own and not with the default path.
				Bool(Key("cors", "allow_self"), PerEnv(), InSection("policy"),
					Doc("Allow Own Origin", "Allow the origin this gateway itself answers on. The only way to say it when the name is generated.")),
				StringSlice(Key("cors", "origins"), PerEnv(), InSection("policy"), Doc("CORS Origins", "Origins allowed by CORS.")),
				StringSlice(Key("cors", "methods"), EachInSet(HttpMethods...), InSection("policy"), Doc("CORS Methods", "Methods allowed by CORS.")),
				Int(Key("rate_limit", "rpm"), PerEnv(), InSection("policy"), Doc("Rate Limit", "Requests per minute.")),
				Int(Key("rate_limit", "burst"), PerEnv(), InSection("policy"), Doc("Burst", "Requests allowed above the sustained rate.")),
				String(Key("rate_limit", "key"), InSection("policy"), Doc("Rate Limit Key", "What the limit is counted per: \"ip\" or \"header:<name>\".")),
			), httpAuthorizerAttributes()...), httpAuthorizerAttributes("route")...),
			Nested(transformersGroup()),
			GroupDoc("A name, the policy at it, and what it serves — one static route, or a function that routes."), Category("internet"), Icon("route"),
			AnswersAt("host"),
			secIdentity, secTransformers,
			Section("naming", "Naming", "The name served, and where its certificate comes from."),
			Section("route", "Route", "The path served and how it is matched."),
			Section("handler", "Handler", "What serves it."),
			Section("policy", "Policy", "Who may reach this, and how often — the authorizer, CORS and rate limiting."),
			Resource("gateway", "Gateway", "Gateway", "gateway"),
		)),

	// The same front door for everything that is not HTTP: a raw TCP, UDP or SCTP
	// connection accepted at the edge and streamed over p2p to a workload, the way a
	// gateway route already carries a request. A gateway can only route what it can
	// parse — a path, a method, a Host header — and Postgres, Redis, MQTT and a game
	// server offer none of those.
	//
	// THIS RESOURCE DECLARES NO PORT AND NO ADDRESS. Both are allocated, and clients
	// are told through DNS — which is the only reason the "nobody writes a port
	// number" rule survives contact with L4. For HTTP the address is the name plus the
	// path and the port is an implementation detail; for a raw connection the port IS
	// the address, so it has to come from somewhere, and a number in this file would
	// be the first authored value in the whole config with a GLOBAL collision domain.
	// Two projects wanting 5432 on one edge address is not an error either project's
	// schema can see.
	//
	// So the proxy gets its own address, and the platform publishes both records:
	//
	//	db.shop.example.com.              30 IN AAAA 2001:db8::7
	//	_postgres._tcp.db.shop.example.com. 30 IN SRV 0 0 5432 db.shop.example.com.
	//
	// SRV (RFC 2782) is the record that carries a port, and it is the precise answer.
	// But close to nothing performs SRV lookups on its own — MongoDB made it explicit
	// with `mongodb+srv://`, XMPP and SIP do it, and psql, redis-cli and most client
	// libraries do not. A dedicated address is what makes that survivable: scarcity was
	// only ever per-address, so on its own name the protocol's conventional port is
	// free again and `psql -h db.shop.example.com` connects with nothing looked up by
	// hand. `service` is what names that convention.
	//
	// The v4 address space is the real cost here, not anything in this schema. On v6
	// an address per proxy is free.
	//
	// TWO RUNTIME CAVEATS the config layer cannot fix, recorded here because they are
	// invisible at authoring time:
	//
	// UDP over a libp2p stream is not transparent. Streams are reliable and ordered,
	// which is the property a UDP application chose to do without — tunnelled, packet
	// loss becomes a latency spike and head-of-line blocking arrives for free. It
	// works; it does not behave the way the application was written against.
	//
	// SCTP has that problem worse, since multi-streaming and multi-homing are the
	// entire reason to pick it and neither survives a tunnel. Both stay in the enum:
	// which protocols the runtime actually carries is the runtime's statement to make,
	// and pretending the vocabulary is where that gets decided helps nobody.
	//
	// Like a gateway's, an fqdn inside a zone this cloud serves needs no record in
	// `zones` — those are produced, not authored.
	DefineGroup("proxy",
		resource(
			append(append(append(identityAttributes(), hostAttributes("listener")...),
				routerAttributes("forward", "proxy-router", "router")...),
				String(Key("routing"), Path(Either("forward", "router")), Captures(), Required(), InSection("forward"),
					Doc("Routing", "forward: every connection goes to the workload named below. router: a function picks one per connection.")),
				// THERE IS NO `protocol` FIELD, AND THERE WAS. It was a set of three —
				// tcp, udp, sctp — and two of them were never servable: a tunnel here is
				// a reliable ordered stream, so carrying a datagram protocol over one
				// turns packet loss into a latency spike and hands it head-of-line
				// blocking, which is the property that application chose to do without.
				// Offering a word the platform can only refuse is a config that lies.
				//
				// With one value left it was a field nobody could get wrong, so it is
				// not a field. The transport is a property of this KIND rather than of
				// one resource, and it is stated in `service`'s own documentation below
				// and spelled once by the service that implements the kind.
				//
				// `srv` went with it: a derived field exists so that several consumers
				// cannot glue two authored values together differently, and there is
				// only one authored value now.
				String("service", IsVariableName(), InSection("listener"),
					Doc("Service", "The IANA service this speaks, e.g. \"postgres\". Publishes _<service>._tcp beside this name in DNS, and is how a client that cannot look up SRV knows which conventional port to dial. Defaults to the resource's name.")),

				// Only `terminate` earns its keep: the proxy holds the certificate so a
				// plaintext workload can answer on a public name, reusing exactly the
				// gateway's cert machinery. There is no `passthrough` mode because there
				// is nothing to pass through to a decision — one proxy owns its address,
				// so nothing is multiplexed and no SNI is read.
				String(Key("tls", "mode"), InSet("none", "terminate"), Default("none"), Field("TLSMode"), InSection("listener"),
					Doc("TLS", "none: an opaque pipe, whatever the protocol does about TLS is its own business. terminate: the proxy answers TLS and hands plaintext to the workload.")),
				// F14: DEFAULTED, not RequiredWhen. `tls: terminate` on a GENERATED host
				// needs no certificate — the cloud holds a wildcard for its own zone,
				// which is the gateway's rule and there is no reason for a proxy to have
				// a different one. RequiredWhen(Key("tls", "mode"),"terminate") demanded the
				// field for exactly that case, while ShowWhen hid it from the editor: a
				// compile blocked on a field the author could not see.
				String(Key("certificate", "source"), InSet("generated", "provided"), Default("generated"), Field("CertSource"), InSection("listener"), ShowWhen(Key("host", "kind"), "zone"),
					Doc("Certificate", "generated: issued on demand for this name. provided: from the secret below. A host under the cloud's own zone needs neither — that zone has a wildcard.")),
				certificateSecret("listener")[0], certificateSecret("listener")[1],

				// Same shape as a gateway route's handler, one kind wide.
				// FORWARD, not "handler". A gateway HANDLES a request — it parses one,
				// matches a path, and something answers it. A proxy handles nothing: it
				// accepts a connection and forwards the bytes. Calling both "handler"
				// borrowed a word from the layer above and made an L4 listener read as
				// if it understood what was going through it.
				String(Key("forward", "kind"), Path("forward", Either(proxyHandlerKinds...)), Captures(), Field("HandlerKind"), InSection("forward"),
					Doc("Forward To", "Which kind the connection is forwarded to (the key selects the block).")),
				String(Key("forward", "namespace"), Path("forward", Either(proxyHandlerKinds...), "namespace"), IsNamespacePath(), InSection("forward"),
					Doc("Namespace", "Path to the namespace holding the workload, e.g. \"/shop/eu\". Omitted means the project root.")),
				// Required once the proxy forwards at all — see the gateway's handler
				// name. A router-only proxy has no forward block to fill in.
				String(Key("forward", "name"), Path("forward", Either(proxyHandlerKinds...), "name"), RequiredWhen(Key("routing"), "forward"), IsVariableName(), Ref("", KindFrom(Key("forward", "kind")), ByName(), Serves()), InSection("forward"),
					Doc("Name", "The workload's name within that namespace.")),

				// THE WORKLOAD'S OWN PORT, by name — the one it binds inside its
				// container. It is the only port in this file, and deliberately: the
				// port CLIENTS dial is allocated by the platform and published in DNS,
				// because at L4 the port is the address and an authored one would
				// collide across projects. See the note on this kind.
				String(Key("forward", "port"), Path("forward", Either(proxyHandlerKinds...), "port"), IsVariableName(), InSection("forward"),
					Doc("Port", "Which of the workload's own named ports to stream to. Not the port clients dial — that one is assigned and published in DNS.")),

				// A gateway counts REQUESTS per minute. A raw stream has no requests in
				// it, so the same words do not transfer and the limits here are the two
				// quantities a connection-oriented listener actually has:
				//
				//   rate         how fast new connections may arrive
				//   concurrent   how many may be open at once
				//
				// The second is the one that matters for a protocol that holds a socket
				// open — a database client pool, a subscriber — where the connection
				// rate stays near zero and the count is the resource. Neither is a
				// substitute for the other, which is why both are here and neither has a
				// default: a ceiling nobody chose is a ceiling nobody can defend.
				Int(Key("rate_limit", "connections_per_minute"), MinInt(1), PerEnv(), InSection("limits"),
					Doc("Connections / minute", "How fast new connections may be accepted from one source. Absent means unlimited.")),
				Int(Key("rate_limit", "burst"), MinInt(1), PerEnv(), InSection("limits"),
					Doc("Burst", "Connections allowed above the sustained rate.")),
				Int(Key("rate_limit", "concurrent"), MinInt(1), PerEnv(), InSection("limits"),
					Doc("Concurrent", "How many connections may be open at once from one source. The limit that matters when a protocol holds its socket open.")),
				String(Key("rate_limit", "key"), InSet("ip", "subnet"), Default("ip"), InSection("limits"),
					Doc("Counted Per", "ip: each address separately. subnet: per /24 and /64, which is what stops one host with an address range from being many clients.")),
			),
			Nested(transformersGroup()),
			GroupDoc("An allocated address that accepts a raw connection and streams it to a workload over p2p — to a fixed one, or to whichever a function picks. For everything a gateway cannot parse."), Category("internet"), Icon("plug"),
			AnswersAt("host"),
			secIdentity, secTransformers,
			Section("listener", "Listener", "The protocol carried, the name it is reached by, and whether TLS stops here."),
			Section("forward", "Forward", "The workload on the other end."),
			Section("limits", "Limits", "What one source may open, and how fast."),
			Resource("proxy", "Proxy", "Proxy", "proxy"),
		)),

	// Both halves are declared: it edges to its consumer with `subscribe`, the producer
	// edges back with `publish`. Two statements because they are two capabilities —
	// collapsing them into `write` erases a real boundary.
	//
	// THE NAME HAS MOVED TWICE AND THIS IS WHY. v1 called it `messaging`, which named a
	// DOMAIN rather than a thing: every other kind here is a noun for the thing itself
	// — a database, a queue, a cache — and nobody says "a messaging". It was `channel`
	// after that, which is a noun and was still wrong in a way that only shows once
	// there is a second conduit kind beside it: in Go, and in Redis, a channel is
	// point-to-point with a single receiver, so the word mis-cues fan-out to exactly
	// the audience that writes Go. `stream` is the industry's word for durable fan-out
	// — Kinesis Data Streams, NATS JetStream, Redpanda — and `channel` moved to the 1:1
	// byte conduit below, where it is right.
	//
	// The neighbourhood is what the name has to survive: `queue` fans in to one
	// claimant, `stream` fans out to every subscriber, `bus` fans out and forgets, and
	// `channel` is two workloads and a socket. Nobody has to be told which is which.
	//
	// IMPLEMENTATION NOTE: v1's `services/substrate/components/pubsub/` is v1's home for
	// this. It is not being renamed, so someone reading `p2p/stream.go` while writing a
	// `stream` resource is looking at the wrong kind — that file is the `channel` kind.
	//
	// IT HAS AN ENGINE, and until 2026-08-31 it did not: a stream was always the
	// swarm's own gossipsub, and the only way to reach Kafka was a separate resource
	// that consumed it and republished what it read as CloudEvents over that gossipsub.
	// Which throws away every reason to have used Kafka — ordering, durability,
	// consumer groups and replay all die at the republication, and a subscriber gets
	// fire-and-forget however the stream was fed. So the engine is a key from a closed
	// set, exactly as it is on a database, and the topic LIVES where the engine says.
	//
	//	distributed:                     kafka:
	//	    local: true                      host: broker:9092
	//	                                     credentials: { name: k }
	//
	// One axis rather than a database's two, so the engine block is the document root's
	// rather than a type block's. The rule is the one the vocabulary already runs on:
	// the block you write is named after the choice, and a switch captures which block
	// that was. `storage` does it for object-versus-streaming and `database` does it
	// twice.
	//
	// `nats` here is JETSTREAM, which is NATS's persistence layer — streams, durable
	// consumers, replay. Core NATS is the ephemeral n:n bus and is `bus`'s business,
	// not this kind's. The engine is spelled `nats` rather than `jetstream` because
	// that is the product an author has credentials for; whether the key should say
	// `jetstream` instead is the owner's call.
	//
	// WHAT THE KEY BUYS, PLAINLY, because everything above says where the topic LIVES
	// and nothing says what each answer keeps. `kafka` and `nats` are logs: ordering,
	// consumer groups, replay, a subscriber that was not there still getting what it
	// missed — which is why each REQUIRES both a host and a credential, the log being
	// somebody else's cluster. `distributed` is the swarm's own gossipsub and is none
	// of that: no offsets, no consumer groups, no replay, delivery to whoever is
	// subscribed at the time, and it requires nothing at all. So it is the same
	// delivery a `bus` gives, and saying so HERE is the point — what this key buys is
	// not that a stream is durable, it is that MOVING the topic onto a log is an engine
	// block rather than a rewrite of everything that publishes to it. It is on this
	// kind rather than beside the comparison on `bus` because the axis is this one's,
	// and §10 says a thing is declared once, on the thing that has it.
	//
	// `local` moves under `distributed`, where it always belonged: "restrict this to
	// the local node" is a thing the swarm's own gossipsub does and a thing a Kafka
	// cluster has no notion of, so it must not be a key a kafka-backed stream can
	// write. The cost of the engine block being required, taken knowingly: the common
	// case writes `distributed: {}` rather than nothing. Whether an ABSENT engine
	// should mean tau's own — the way an absent `tls` means verify-full — is the
	// owner's call; the default here is explicit, because an engine read as "" that a
	// consumer has to know means distributed is the fallback chain §10 refuses.
	//
	// THE BRIDGES ARE GONE. `bridges/mqtt/enable` and `bridges/websocket/enable` were
	// two booleans bolted onto a pubsub topic. MQTT is deleted outright and nothing has
	// asked for it back; websocket is not lost but rehomed — on a `bus`, attaching a
	// browser and receiving from now on is the point of the kind rather than a flag on
	// a different one.
	//
	// AND SO IS `match`/`regex`. It was v1's `messaging` pair, carried here because it
	// was carried there, and this was the last kind holding it. A STREAM IS ITS NAME:
	// one resource, one topic, spelled `orders` in the file it is written in and
	// `orders` in every access block that reaches it. That is already the only spelling
	// anything used — the index is by name, an access entry is keyed by name, and every
	// fixture wrote `match:` equal to the resource's own name — so what the pair added
	// over the name was the ability for it to DISAGREE with the name, and for two
	// streams to both claim one topic with the walk order deciding which got the
	// message.
	//
	// It is the rule `instanceAttributes` already states for a store, one level up: a
	// name is a key, not a pattern, because a pattern answers "which one" with a SET,
	// and a set cannot be indexed, cannot be resolved, and cannot be told apart from
	// another set that overlaps it. What is NOT done here is the other half of that
	// construct — this kind gains no `instances:` block, because one stream is one topic
	// and there is no caller-supplied name to admit. Whether a stream should ever hold
	// many topics is a different question and is the owner's.
	DefineGroup("stream",
		resource(
			commonAttributes(append([]*Attribute{
				String("engine", Path(Either(streamEngines...)), Captures(), InSection("engine"),
					Doc("Engine", "Which engine holds the topic: distributed is the swarm's own gossipsub; kafka and nats are external clusters reached over the network.")),
				Bool("local", Path(tauEngine, "local"), InSection("engine"), ShowWhen("engine", tauEngine),
					Doc("Local", "Restrict the stream to the local node / project scope. Only tau's own engine has a notion of this.")),
			}, externalEngineAttributes(nil, nil, externalStreamEngines, Key("engine"), "engine", ShowWhen("engine", externalStreamEngines...))...)...),
			GroupDoc("A pubsub stream: what is published to it reaches every subscriber. On the swarm's own gossipsub, or on an external Kafka or NATS cluster."), Icon("message"), Category("trigger"),
			secIdentity, secAccess,
			Section("engine", "Engine", "Which engine holds the topic, and its settings."),
			Ops(DeliveredOp("subscribe"), WriteOp("publish")), // never collapse these into "write"
			Resource("stream", "Stream", "Stream", "stream"),
		)),

	// AN N:N BUS THAT CAN NEVER REWIND, and the "never" is the word doing the work,
	// because it is a CEILING rather than a state. Both kinds fan out to everyone
	// attached, and on tau's own engine they deliver identically — a `distributed`
	// stream keeps nothing either, so DURABILITY IS NOT THE DISTINCTION and saying it
	// was made this comment disagree with the fixture beside it. What separates them
	// is what each can BECOME: a stream names an engine, so the same topic moves onto
	// Kafka or JetStream and IS a log without a line changing in anything that
	// publishes to it, and a bus names none and cannot be moved. That is why there are
	// two kinds rather than one with a flag: a flag would make "could this ever be
	// replayed?" a property somebody edits, when it is a property of what the thing IS.
	//
	// (NATS is on both sides of that line and the halves are easy to swap: JetStream is
	// NATS's PERSISTENCE layer and belongs to `stream`; core NATS is the ephemeral n:n
	// bus and is this shape.)
	//
	// IT HAS NO ENGINE AXIS, and the absence is the statement — but NOT because every
	// engine a `stream` names is durable. The reason is that a bus has nothing to
	// choose: a stream's engine key asks "the swarm, or a cluster you hold credentials
	// for?", and `kafka` and `nats` each require a host AND a credential because the
	// log is somebody else's machine — while the ephemeral n:n shape is what the swarm
	// already IS. A key with one legal value states nothing an author can act on.
	//
	// A WEBSOCKET IS AN ADDRESS, NOT A BOOLEAN. v1 spelled it `bridges.websocket.enable`
	// on the pubsub kind — a flag that turned something on and named nowhere for a
	// browser to point at, so the URL came out of a host call at run time and existed
	// nowhere a reader could see. Writing `host:` is what gives this bus a websocket,
	// and it is the same `host:` a gateway and an L4 proxy already declare: a name
	// under the cloud's own zone, or one in a zone the project owns. That keeps §10a's
	// rule — the config never names the cloud it happens to be running in — and it
	// gives `ReachableNames` something to answer with, which a boolean could not.
	// A bus with no `host:` is reachable from inside the project and from nowhere else.
	//
	// AND NEITHER IT NOR `stream` HAS `match`/`regex` ANY MORE. v1's `messaging` carried
	// that pair, `stream` carried it here because it was inherited, and it is gone from
	// both: a pattern with no owner, answering "may this instance exist" with "does this
	// string match". A bus is the sharpest case, because the thing attaching is a
	// BROWSER: the instance name is the caller's, so the project has to have a point at
	// which it says no. That point is `instances:`, the construct every store here
	// already uses, and it is why a bus has that block where a stream — one resource,
	// one topic, named by its own name — needs none.
	//
	// The verbs are `stream`'s, and deliberately the same ones: what you DO to a bus is
	// what you do to a topic, and the kinds differ in what happens afterwards. Two
	// spellings for one operation would make every cross-cutting query ask twice.
	DefineGroup("bus",
		resource(
			commonAttributes(instanceAttributes()...),
			GroupDoc("An ephemeral n:n message bus: everything attached gets what is sent, and nothing is kept. Point a gateway route at it and a browser can attach over websocket."), Icon("cable"), Category("trigger"),
			secIdentity, secAccess,
			secInstances,
			Ops(DeliveredOp("subscribe"), WriteOp("publish")), // never collapse these into "write"
			Resource("bus", "Bus", "Bus", "bus"),
		)),

	// NO `auth` FIELD. It was `none | oauth`, and `oauth` promised to "validate tokens
	// minted by a NAMED authorization server" with nowhere to name one — no issuer, no
	// audience, no keys. A security-shaped claim nothing could honour, which is the
	// inert-field defect this vocabulary deleted `notification.email` for, and worse:
	// an author who read it would believe the server was protected.
	//
	// Deleted rather than completed, because the prerequisite is itself blocked — an
	// authenticated MCP has to be routed on the `Mcp-Method` / `Mcp-Name` headers and
	// this vocabulary cannot match on a header yet.
	//
	// THE SHAPE IT COMES BACK AS, so the return is a field and not a redesign: an
	// `auth` block whose oauth branch requires ONE field, `issuer` — an https
	// authorization-server identifier — with `scopes` optional. The AUDIENCE is never
	// authored: it is the canonical URL this server is reached at, which ends in the
	// cloud's fqdn and so cannot be written here for the same reason a cors origin
	// cannot; the platform derives it from the gateway route that serves it.
	// `jwks-uri` is not authored either — it is discovered from the issuer's own
	// metadata, and a second copy in config is a key source that can go stale.
	//
	// Thin on purpose: the tools are already functions. Stateless Streamable HTTP,
	// POST only, no sessions. It compiles to route-table rows that additionally
	// predicate on the Mcp-Method / Mcp-Name headers the 2026-07-28 spec mirrors out
	// of the body — routing never reads the JSON-RPC body. The protocol-version
	// guard is a MUST and comes before the router, or header routing trusts
	// attacker-controlled headers (resources.md §5.4).
	DefineGroup("mcp",
		resource(
			commonAttributes(
				String(Key("tools", "cache_scope"), InSet("none", "public", "principal"), Default("none"), InSection("tools"), Doc("Cache Scope", "How widely tools/list may be cached. public is cacheable across the whole swarm.")),
				Duration(Key("tools", "cache_ttl"), PerEnv(), InSection("tools"), Doc("Cache TTL", "How long a cached tools/list stays valid.")),
			),
			GroupDoc("An MCP server grouping functions as tools. Stateless Streamable HTTP; subscriptions are declined in v1."), Icon("plug"), Category("internet"),
			secIdentity, secAccess,
			Section("tools", "Tools", "tools/list caching."),

			// WHAT REACHES AN MCP SERVER, and the verbs are the protocol's own methods:
			// `tools/list` asks what there is and `tools/call` runs one. An agent naming
			// this kind is the point — an MCP server IS a tool registry, and an agent's
			// tools are what it declares access to, so without these an agent could only
			// reach one by leaving the cloud through its own gateway route to call
			// something that lives inside it.
			//
			// `call` is a WRITE and `list` is a READ, which is the same reading `invoke`
			// gets on a function: asking what tools exist reveals a catalogue, running
			// one may do anything the tool can.
			//
			// Two the protocol has and this does not: `resources/read` and `prompts/get`.
			// Neither has a field on this kind yet — an mcp groups functions, and nothing
			// here declares a resource or a prompt to serve — so a verb for them would
			// grant access to something nobody can publish.
			Ops(ReadOp("list"), WriteOp("call")),
			Resource("mcp", "MCP", "MCP", "mcp"),
		)),

	// ---- in-project addressing ----

	// The kind v1 called `services`, back with a name that says what it is. A CHANNEL:
	// a named, ordered, bidirectional byte conduit between exactly two workloads inside
	// the project, carried by libp2p, with no edge in the path.
	//
	// `channel` BECAUSE IT IS 1:1, and that is the whole argument for the name. Go's
	// `chan` is point-to-point with a single receiver per value, which is exactly what
	// this is and exactly what fan-out is not — so the word that mis-cued the semantics
	// on the pubsub kind is the right one here. It was called `stream` until
	// 2026-08-31, and `stream` moved to the fan-out kind because that is the industry's
	// word for durable fan-out (Kinesis Data Streams, NATS JetStream, Redpanda).
	//
	// A NOTE FOR ANYONE READING THE IMPLEMENTATION: v1's directories are now inverted
	// relative to this vocabulary. v1's `services/substrate/components/p2p/stream.go` is
	// what this kind describes, and v1's `services/substrate/components/pubsub/` is what the
	// `stream` kind describes. Those are v1's and v1 is not being renamed; the v2
	// successors take the names this vocabulary uses.
	//
	// It exists because every other inbound path in this vocabulary is an INGRESS. A
	// gateway route parses HTTP arriving from outside; a proxy accepts a connection
	// from outside. A function that wants to talk to the project's own Postgres, or to
	// a sidecar that speaks a protocol nobody standardised, had exactly two options:
	// go out to the edge and back in, or nothing. This is the nothing, filled in.
	//
	// NOT `p2p`, which names the transport rather than the thing, and not `services`,
	// which is a word every reader fills in differently.
	//
	// THE PROTOCOL IS NAMESPACED BY THE PLATFORM, not by the author. What is written
	// here is a name inside the project; what goes on the wire has the project's
	// identity prepended, so two projects that both call a channel `sync` do not meet.
	// An author who could write the full wire protocol could collide with another
	// tenant on purpose, which is the same reason a proxy authors no port.
	//
	// Deliberately thin, and stated as such: v1's whole declaration was one field. What
	// serves the channel is named here, the way a proxy names what it forwards to,
	// because a channel nothing answers is a name that dials into a void.
	//
	// The icon stays `stream`: an icon is a picture of the thing, and what travels here
	// is still a stream of bytes whatever the kind is called.
	DefineGroup("channel",
		resource(
			commonAttributes(
				String("protocol", Required(), IsVariableName(), InSection("channel"),
					Doc("Protocol", "The name this channel is addressed by within the project, e.g. \"sync\". What goes on the wire has the project's identity prepended, so two projects using the same name never meet.")),

				// Same shape as a proxy's `forward` and a gateway route's handler: the
				// resource that answers, by kind, namespace and name.
				String(Key("serve", "kind"), Path("serve", Either(channelServerKinds...)), Captures(), InSection("channel"),
					Doc("Served By", "Which kind answers this channel (the key selects the block).")),
				String(Key("serve", "namespace"), Path("serve", Either(channelServerKinds...), "namespace"), IsNamespacePath(), InSection("channel"),
					Doc("Namespace", "Path to the namespace holding it, e.g. \"/shop/eu\". Omitted means this scope, then its parents.")),
				String(Key("serve", "name"), Path("serve", Either(channelServerKinds...), "name"), Required(), IsVariableName(), Ref("", KindFrom(Key("serve", "kind")), ByName(), Serves()), InSection("channel"),
					Doc("Name", "Its name within that namespace.")),
				String(Key("serve", "port"), Path("serve", Either(channelServerKinds...), "port"), IsVariableName(), InSection("channel"),
					Doc("Port", "Which of the workload's own named ports the channel is delivered to. Containers only; a function is dispatched, not connected.")),
			),
			GroupDoc("A named byte channel between two workloads inside the project, over libp2p. The way in that is not an ingress."), Icon("stream"), Category("compute"),
			secIdentity, secAccess,
			Section("channel", "Channel", "The name it answers to, and what answers it."),
			Ops(WriteOp("dial")), // dial is write: what travels is opaque
			Resource("channel", "Channel", "Channel", "channel"),
		)),
	// ---- models ----

	// WHERE INFERENCE HAPPENS, and on whose account. Its own kind because it is its own
	// thing: the same model answers for three agents and a function, it has a
	// credential that rotates, an endpoint that moves, and parameters somebody tunes —
	// and none of that is a property of whoever is calling it. It was a STRING on the
	// agent (`model: claude-opus-5`) with the credential beside it, which made every
	// agent carry a copy of the same four facts and gave the credential nowhere to live
	// but the caller.
	//
	// LOCAL AND REMOTE ARE THE SAME KIND, because the caller does not care: a model is
	// an endpoint that speaks a wire format. `remote` is a provider's, `local` is a
	// workload in this project serving one — which is how a self-hosted vLLM or ollama
	// arrives without a second kind for it, and reuses the container story rather than
	// inventing an inference one.
	//
	// FORMAT is not the provider. Half the world serves the OpenAI wire format,
	// including things that are not OpenAI, so what a caller has to know is the shape of
	// the request and not whose logo is on it.
	DefineGroup("model",
		resource(
			commonAttributes(
				String("identifier", Required(), PerEnv(), InSection("model"),
					Doc("Identifier", "What this model is called where it is served, e.g. \"claude-opus-5\" or \"llama-3.1-70b\". The resource's own name is yours; this one is theirs.")),
				String("format", InSet("openai", "anthropic", "ollama"), Required(), InSection("model"),
					Doc("API Format", "The wire format the endpoint speaks. Not the provider: plenty of things that are not OpenAI serve the OpenAI format, and the caller needs the shape of the request.")),

				// `inference`, not `serve`. A stream already says `serve:` and means
				// something else by it — WHICH KIND answers — and two keys with one
				// spelling is how a check written for one starts firing on the other.
				// It did: the port check read this switch's `local` as a resource kind.
				String(Key("inference", "kind"), Path("inference", Either("remote", "local")), Captures(), InSection("model"),
					Doc("Inference", "remote: a provider reached over the network. local: a workload in this project serving it (the key selects the block).")),
				String(Key("inference", "remote", "endpoint"), IsURL(), RequiredWhen(Key("inference", "kind"), "remote"), PerEnv(), InSection("model"),
					Doc("Endpoint", "Base URL of the provider, e.g. \"https://api.anthropic.com\".")),
				String(Key("inference", "remote", "credentials", "namespace"), IsNamespacePath(), InSection("model"),
					Doc("Namespace", "Where that secret lives. Omitted means this resource's own scope, then its parents.")),
				String(Key("inference", "remote", "credentials", "name"), IsVariableName(), Ref("secret", Inbound()), RequiredWhen(Key("inference", "kind"), "remote"), InSection("model"),
					Doc("Credentials", "Secret holding the provider credential, as \"secret/<name>\". It lives here rather than on each caller, so rotating it is one edit.")),
				String(Key("inference", "local", "namespace"), IsNamespacePath(), InSection("model"),
					Doc("Namespace", "Where that application lives. Omitted means this resource's own scope, then its parents.")),
				String(Key("inference", "local", "name"), IsVariableName(), Ref("application"), RequiredWhen(Key("inference", "kind"), "local"), InSection("model"),
					Doc("Workload", "The application in this project that serves this model.")),
				String(Key("inference", "local", "port"), IsVariableName(), InSection("model"),
					Doc("Port", "Which of that workload's named ports inference is reached on.")),

				// TYPED, not a map. The DSL has no arbitrary-keyed values, and that is
				// the right constraint here rather than a limit worked around: these are
				// the four every format has, they have ranges worth checking, and a
				// provider-specific fifth belongs to whoever added the provider.
				Float(Key("parameters", "temperature"), PerEnv(), InSection("parameters"),
					Doc("Temperature", "How much the model is allowed to vary its answer. 0 is as close to deterministic as a model gets.")),
				Float(Key("parameters", "top_p"), PerEnv(), InSection("parameters"),
					Doc("Top P", "Nucleus sampling: consider only the most likely tokens summing to this probability.")),
				Int(Key("parameters", "max_tokens"), MinInt(1), PerEnv(), InSection("parameters"),
					Doc("Max Tokens", "Ceiling on one answer. A cost ceiling as much as a length one.")),
				StringSlice(Key("parameters", "stop"), InSection("parameters"),
					Doc("Stop Sequences", "Strings that end an answer when the model produces them.")),
			),
			GroupDoc("Where inference happens and on whose account: an endpoint, a wire format, a credential, and the parameters it is called with."), Icon("model"), Category("compute"),
			secIdentity, secAccess,
			Section("model", "Model", "What it is, what it speaks, and where it is served."),
			Section("parameters", "Parameters", "How it is called."),
			Ops(WriteOp("infer")), // infer is write: it costs money and answers differently every time
			Resource("model", "Model", "Model", "model"),
		)),

	// ---- agents ----

	// An LLM loop with tools, declared rather than written. It is a workload like any
	// other — it is invoked, it has ceilings, it reaches things — and exactly one part
	// of it is new: the model.
	//
	// ITS TOOLS ARE ITS ACCESS BLOCK. That falls out of the vocabulary rather than
	// being designed: an agent may call the functions it declares `invoke` on, read the
	// databases it declares `query` on, and nothing else. A separate `tools:` list
	// would be a second grant path that the authorizer on the target never sees, which
	// is the door §9 exists to keep shut. So an agent's blast radius is a graph query
	// that already works.
	//
	// `steps` is not a performance knob. An agent that calls a tool, reads the result
	// and decides again has no natural end, and the failure mode is not a slow request
	// — it is a loop that spends money until someone notices. A ceiling nobody chose
	// is the one thing worse than a ceiling that is wrong, so it is required.
	DefineGroup("agent",
		resource(
			commonAttributes(append([]*Attribute{
				// ONE model, named as a resource. The endpoint, the credential, the wire
				// format and the parameters are its business, not this agent's — which
				// is what stopped every agent carrying a copy of the same four facts and
				// gave the credential somewhere to live that is not the caller.
				// `model` is a kind AND this attribute's name, and both live under the
				// same key: this block, and the access group generated for the kind.
				// They coexist because a key an attribute declares is not an entry of
				// the group sharing its name (engine/node.go, claimedUnder).
				// With the name, because a model in another scope is one answer, not two.
				String(Key("model", "namespace"), IsNamespacePath(), PerEnv(), InSection("model"),
					Doc("Model Namespace", "Where that model lives. Omitted means this agent's own scope, then its parents.")),
				// MARKED, and it is the one reference that is. A cheaper model in preview
				// is the most-asked-for difference there is, and a reference genuinely
				// resolves inside one environment's compile — so "the graph is readable
				// without picking an environment" is a preference here rather than an
				// invariant, and this is where it is spent. Every other reference stays
				// environment-invariant: what a resource is SET to may vary, what it
				// REACHES may not.
				String(Key("model", "name"), IsVariableName(), Ref("model"), Required(), PerEnv(), InSection("model"),
					Doc("Model", "The model this agent answers with.")),

				// A FILENAME, not the prose. A system prompt is pages of text, and a
				// 40KB string in a yaml document is unreadable, unreviewable line by
				// line, and mixed in with configuration. What it is relative to is the
				// agent's `source` — this repository by default — so the same base that
				// tells a workload where its code is tells an agent where its words are.
				//
				// AND "the resource's own location" IS A TEMPLATE, not a convention:
				// `LayoutOf().Content` is `{kind}/{name}`, so an agent with no `source`
				// keeps its words in `agent/<name>/`. A workload's build has a
				// convention it can fall back on and an agent has no build at all, so
				// without that field the base was a phrase with no referent and every
				// consumer would have invented the same rule privately.
				String("instructions", IsRepoPath(), InSection("model"),
					Doc("Instructions", "The file holding the system prompt, e.g. \"instructions.md\". Relative to this agent's source, which is this repository at the agent's own location unless it says otherwise.")),

				// MEMORY. Declared here and stored in a vector index, which is what the
				// lowering produces: an agent that remembers is an agent with somewhere
				// to put embeddings, and that somewhere is a resource like any other —
				// it has a size, it is searched, and something may be granted access to
				// it. Declaring it inside the agent rather than making the author wire
				// one up is the difference between "remembers" being a property of the
				// agent and being an assembly job.
				//
				// The dimensions are the EMBEDDING model's, not the chat model's, which
				// is why the embedder is named beside them: the two are different models
				// and an agent needs both.
				// AN EMBEDDER IS A MODEL, so it is named the same way. Different model
				// from the one that answers — the widths and the pricing are different —
				// which is exactly why memory needs its own reference rather than
				// borrowing the agent's.
				String(Key("memory", "embedder", "namespace"), IsNamespacePath(), InSection("memory"),
					Doc("Namespace", "Where that model lives. Omitted means this resource's own scope, then its parents.")),
				String(Key("memory", "embedder", "name"), IsVariableName(), Ref("model"), InSection("memory"),
					Doc("Embedding Model", "The model that turns what this agent sees into vectors, as \"model/<name>\". Naming it is what turns memory on.")),
				Int(Key("memory", "dimensions"), MinInt(1), RequiredWhen(Key("memory", "embedder", "name")), InSection("memory"),
					Doc("Dimensions", "The width the embedding model produces — 1536 for text-embedding-3-small. Fixed once there is anything in memory.")),
				// NO DEFAULT, and required only when there is memory to size. A default
				// here fired for every agent, so one that names no embedder compiled
				// claiming a gigabyte of memory it does not have — a value that is
				// misleading when the branch is not taken, which is the one thing §7
				// says not to give a default to.
				Bytes(Key("memory", "size"), RequiredWhen(Key("memory", "embedder", "name")), PerEnv(), InSection("memory"),
					Doc("Memory Size", "How much the agent may remember, as a human string.")),

				// NO `sessions` BLOCK. It was one field, a per-conversation byte
				// ceiling, and its Doc said naming it "is what gives this agent
				// sessions; without it every invocation starts cold" — so the field
				// was the config deciding whether a conversation is kept, and where.
				// That is ORCHESTRATION, and orchestration is the runtime's: what a
				// thread is, where its turns live, when one ends and what survives it
				// are decisions the loop makes while it runs, not shape a compiler can
				// give a resource. Memory beside it is the contrast that makes the line
				// visible: an embedder and a width are a vector index this vocabulary
				// can BUILD, and a thread is not.
				//
				// So it went the way `notification.email` did, and for the same reason
				// rather than a related one: nothing here performed it. It comes back
				// the day the runtime says it reads a ceiling off the compiled agent —
				// as `limits.sessions`, beside the other ceilings the loop is handed,
				// and PerEnv() like every other ceiling, which this one was refused
				// only because it doubled as a switch.

				// REQUIRED, and it bounds DECISIONS. It is not a spend ceiling and no
				// longer claims to be one: every turn re-sends the conversation, so what
				// a step costs grows as the loop runs and the total is superlinear in
				// this number. What bounds money is a token ceiling, which this does not
				// half-promise. The field it would be is `limits.tokens`, per
				// invocation: the loop can enforce it from what every model response
				// already returns, and tokens are portable in a way a currency figure
				// is not — prices move, cached and output tokens bill differently, and
				// a local model has no price at all.
				Int(Key("limits", "steps"), MinInt(1), Required(), PerEnv(), InSection("limits"),
					Doc("Max Steps", "How many times this agent may call a tool and decide again in one invocation. Required: an agent with no ceiling is a loop that runs until something else stops it.")),
				Int(Key("limits", "concurrent"), MinInt(1), PerEnv(), InSection("limits"),
					Doc("Concurrent", "How many invocations may run at once.")),

				// HOW LONG ONE INVOCATION MAY TAKE, end to end. The step ceiling bounds
				// how many times the loop may decide again; this bounds the wall clock,
				// and the two are different ways to run away. Defaulted, because the
				// value is carried onto the loop this agent becomes and a produced
				// workload has to have one — it used to be five minutes written into the
				// lowering, which is a number no author could see or change.
				Duration(Key("limits", "timeout"), Default("5m"), PerEnv(), InSection("limits"),
					Doc("Timeout", "Ceiling on one whole invocation, as a human string (e.g. \"5m\"). Counts the model calls, the tools and the waiting between them.")),
				Bytes(Key("limits", "memory"), Default("128MB"), PerEnv(), InSection("limits"),
					Doc("Memory", "How much memory the loop may use, as a human string (e.g. \"128MB\"). An agent handing large tool results between the model and its tools needs more than one juggling short answers.")),
				egressAttribute(),
				// An agent's content is prose, and it lives where the resource lives:
				// this repository by default, a path in it, or another repository.
				// `component` and `builtin` are not branches here — an agent is not built.
			}, contentSource("source", "git")...)...),
			// RUN ONLY: an agent declares no engine because nothing builds it — its
			// content is prose — so a variable of its own has one lifetime and the set
			// says which.
			Nested(variablesGroup(runLifetime)),
			GroupDoc("An LLM loop with tools. What it may call is what it declares access to — there is no second list."), Icon("agent"), Category("compute"), Placed(),
			secIdentity, secAccess, secConfig, secNetwork, secLimits, secSource,
			Section("model", "Model", "Which model answers, and what it is told."),
			Section("memory", "Memory", "What it remembers, and how wide the vectors are."),
			Ops(WriteOp("invoke")),

			// AN AGENT LOWERS, like a workflow: nothing in the worker runs "agents".
			// The loop is a function, and what it remembers is a vector index — both
			// resources the platform already runs, so an agent adds a vocabulary and no
			// component.
			//
			// The function's SOURCE is the platform's own (`builtin: agent`), the one
			// thing here an author does not supply: the loop — call the model, hand it
			// the tools it may reach, run one, decide again, stop at the ceiling — is the
			// same program for every agent, and the parts that differ are the fields
			// above. An agent whose loop was the author's code would be a `function`.
			Lowers(
				Into("function",
					// The loop is what CALLS the tools this agent declares, so opened,
					// the box reads model-and-memory-and-tools around it.
					Acts(),
					Named(Concat(Attr("name"), Text("_infer"))),
					Carries(Text("the loop: call the model, run a tool, decide again")),
					Sets(Key("source", "builtin"), Text("agent")),
					Sets(Key("execution", "call"), Text("infer")),
					Sets(Key("execution", "timeout"), Attr(Key("limits", "timeout"))),
					Sets(Key("execution", "memory"), Attr(Key("limits", "memory"))),
					Sets("implements", Text("handler")),
				),

				// Only when there is an embedder to fill it: an agent that remembers
				// nothing gets no index rather than an empty one.
				//
				// A DATABASE OF TYPE `vector`, since 2026-08-31, and the type is not a
				// term the lowering states: writing anything under `vector/` is what
				// selects the block, and the switch captures it. Both fields it sets
				// have LITERAL paths for exactly this reason — interp's authoredPath has
				// no single authored location for a path with a matcher in it.
				Into("database",
					When(Key("memory", "embedder", "name")),
					Named(Concat(Attr("name"), Text("_memory"))),
					Carries(Concat(Text("what "), Attr("name"), Text(" remembers"))),
					// One term, one field: the value is CARRIED, so the index's size is
					// the agent's own byte count and comes back out again as `2GB`.
					Sets(Key("vector", "dimensions"), Attr(Key("memory", "dimensions"))),
					Sets(Key("vector", "size"), Attr(Key("memory", "size"))),
					// AND THE CONSENSUS, WHICH THE PRODUCED KIND NOW REQUIRES — a
					// lowering owes every field of what it builds, and this one is
					// fixed text rather than carried because the agent has no such
					// field to carry it from. `crdt`: a memory index is derived from
					// what the loop saw and is re-embeddable from it, so the write a
					// merge discards costs a re-embed and never a fact — and a loop
					// writing every turn should not pay a round trip to do it. Giving
					// the agent its own knob is a different change; what would ask for
					// one is an agent whose memory IS the record of something.
					Sets(Key("vector", "consensus"), Text("crdt")),
					// The loop is what searches it, and that is an entry on the loop:
					// `database: { <name>_memory: { ops: [search] } }`, exactly what an
					// author would have written.
					Reaches("search", From("function", Concat(Attr("name"), Text("_infer")))),
				),
			),
			Resource("agent", "Agent", "Agent", "agent"),
		)),

	// A state machine over things that already exist. Steps run in the order written;
	// each does one thing, may be retried, and may hand off to another step when it
	// finally fails.
	//
	// IT LOWERS. A workflow is not a component — nothing in the worker runs
	// "workflows". Each step becomes a queue holding its work and a DRIVER consuming it,
	// failures land in a dead-letter queue, and the run's own state is the message that
	// travels between them. Every one of those is a
	// primitive this vocabulary already has and the platform already runs, which is
	// what makes a workflow a compile-time construct rather than a new subsystem to
	// operate. See Lowers() below, and interp/lowering.go for the expansion.
	//
	// LINEAR, for now, and that is a real limit rather than a simplification: there is
	// no Choice, no Parallel, no Map. Those are the shapes that make Step Functions
	// what it is, and each needs something the list element cannot hold yet: a
	// condition is an expression, and Parallel and Map are lists inside a list.
	DefineGroup("workflow",
		resource(
			commonAttributes(
				Duration("timeout", PerEnv(), InSection("run"),
					Doc("Timeout", "Ceiling on one whole run, as a human string (e.g. \"1h\"). Absent means a run may take as long as its steps do.")),
				Int("concurrency", MinInt(1), PerEnv(), InSection("run"),
					Doc("Concurrency", "How many runs may be in flight at once. Absent means unlimited.")),

				// WHERE A START LANDS. Required, because a pipeline whose first step is
				// "the one written first" makes the order of a list load-bearing in a
				// way nothing else here does, and because `entry` below has to name
				// something. checkWorkflows verifies it is a step of this workflow.
				String("start", Required(), IsVariableName(), InSection("run"),
					Doc("Start", "The step a start lands on.")),
			),
			Nested(stepsGroup(), entryGroup()),
			GroupDoc("An ordered pipeline of functions with retries. Each step runs as its own queue; nothing new has to be deployed to serve it. There is no run state to inspect yet."), Icon("workflow"), Category("trigger"), Placed(),
			secIdentity, secAccess,
			Section("run", "Run", "Ceilings on one run and on all of them."),
			Section("steps", "Steps", "What runs, in order."),
			Ops(WriteOp("start")),

			// THE LOWERING, declared rather than computed. tcc expands it (interp/
			// lowering.go); this says what a workflow becomes and nothing about how.
			//
			Lowers(
				// ONE QUEUE PER STEP: a queue is where a retry budget lives, and the
				// budget is the step's — `reserve` may be worth four attempts and
				// `charge` exactly one. Sharing a queue would make the whole pipeline
				// share whichever number was written last.
				Into("sync",
					PerEach("steps"),
					// THE NAME STATES ITS PARTS. `{name}_{each.name}` produced the same
					// `checkout_reserve` and could not be read backwards — recovering
					// `reserve` meant guessing which underscore was the separator. This
					// says which one, so the step's name is carried by the queue's and a
					// shell does not have to store it twice.
					Named(Concat(Attr("name"), Text("_"), Each("name"))),
					Carries(Concat(Text("work waiting for step "), Each("name"))),
					// EVERY KEY IS UNDER `queue/`, because `sync` is a typed kind and
					// the block that is written IS the type. Writing the consensus is
					// what makes the block exist at all — a produced queue with no
					// authored delivery fields would otherwise have no type.
					Sets(Key("queue", "consensus"), Text("raft")),
					Sets(Key("queue", "max_attempts"), Each("attempts")),
					// the schedule, onto the kind that enforces it. Unset fields drop.
					// a step that waits is a queue that holds its work back, and the
					// human string the step was written with is parsed by the queue's own
					// Duration — tcc converts it, nothing downstream has to
					Sets(Key("queue", "delay"), Each(Key("wait"))),
					Sets(Key("queue", "retry", "interval"), Each(Key("backoff", "interval"))),
					Sets(Key("queue", "retry", "factor"), Each(Key("backoff", "factor"))),
					Sets(Key("queue", "retry", "ceiling"), Each(Key("backoff", "ceiling"))),

					// EXHAUSTION IS ROUTED, NOT GRANTED, and the queue's own mechanism
					// does it: dead-lettering fires exactly when attempts run out, which
					// is the one moment no code of this step is running. A step naming
					// another step lands in that step's queue; the default lands in the
					// terminal one.
					Sets(Key("queue", "dead_letter", "name"), Concat(Attr("name"), Text("_"), Each("on_error"))),

					// CONSUMED BY THE STEP'S DRIVER, never by the author's function. The
					// function is invoked BY the driver and knows nothing about queues:
					// that is the whole of what this composition changed.
					Reaches("receive", From("function", Concat(Attr("name"), Text("_"), Each("name")))),
				),

				// ONE DRIVER PER STEP — platform code, the way an agent's loop is. Its
				// program IS the forward-progress contract: receive one item, invoke the
				// function it was granted, send what came back to the queue it was
				// granted, publish that the step finished. Fail and the queue redelivers;
				// exhaust and the queue's dead-letter moves the run.
				//
				// A FUNCTION. A function here declares no trigger — what may reach it is
				// an inbound edge — and a queue delivery is one, which is why a wasm
				// consumer needs no port and no path: it is dispatched, and the call IS
				// the delivery (deliverAttributes says so about containers by saying it
				// about their absence). `worker` is the kind whose runtime TIER is
				// authored, wasm or container, and a program the runtime itself ships
				// has no tier to choose.
				//
				// It carries the step's NAME and no suffix. The kind is part of a
				// resource's identity everywhere here — the index, the layout, a
				// collision — so `queue/checkout_reserve` and `function/checkout_reserve`
				// are the step's mailbox and the step's machine, said once each.
				Into("function",
					PerEach("steps"),
					Named(Concat(Attr("name"), Text("_"), Each("name"))),
					Carries(Concat(Text("step "), Each("name"), Text(": receive, run, hand on"))),
					Sets(Key("source", "builtin"), Text("step")),
					Sets(Key("execution", "call"), Text("step")),
					// FIXED, and not the run's timeout. This bounds one delivery — receive,
					// invoke, hand on — and what the invoke may take is the callee's own
					// execution/timeout. Copying the run's ceiling here would say a single
					// step of a 24h run may sit for 24h, which is a different claim.
					Sets(Key("execution", "timeout"), Text("1h")),
					Sets(Key("execution", "memory"), Text("64MB")),

					// THE STEP'S OWN FUNCTION, request-and-answer. `To` carries the step's
					// namespace: written, it is where the author said to look; absent, the
					// bare name resolves outward from the produced scope, which walks up to
					// the project root and finds what they wrote.
					Reaches("invoke",
						From("function", Concat(Attr("name"), Text("_"), Each("name"))),
						To("function", Each(Key("call", "function")), Each(Key("call", "namespace")))),

					// FORWARD PROGRESS: the driver's ONE send. The last step has no next,
					// so the term cannot fill and the write is dropped — which is exactly
					// where the run ends.
					Reaches("send",
						From("function", Concat(Attr("name"), Text("_"), Each("name"))),
						To("sync", Concat(Attr("name"), Text("_"), Next("name")))),
				),

				// The end of the line, with NO CONSUMER — deliberately. A dead-letter
				// queue is where a run stops so a person can look at it; something
				// draining it automatically would be the retry that already happened.
				//
				// WHERE A START LANDS is recorded here, on the shell itself. Performing
				// `start` on a workflow means sending to the start step's queue, and that
				// is a fact about this kind that no consumer may be asked to work out
				// from a naming convention. It is written as an ordinary value, so
				// decompile takes it back off with everything else a lowering added.
				Into("sync",
					Named(Concat(Attr("name"), Text("_failed"))),
					Carries(Text("runs that gave up, kept for a person to look at - nothing consumes this")),
					Sets(Key("queue", "consensus"), Text("raft")),
					Writes(Text("sync"), On("workflow", Attr("name")), At(Text("entry"), Text("start"), Text("kind"))),
					Writes(Concat(Attr("name"), Text("_"), Attr("start")), On("workflow", Attr("name")), At(Text("entry"), Text("start"), Text("name"))),
					Writes(Text("send"), On("workflow", Attr("name")), At(Text("entry"), Text("start"), Text("as"))),
				),

				// AND NO EVENTS STREAM. One was produced here — a stream per workflow,
				// every driver publishing that its step had finished — and nothing
				// authored asked for it, which is the rule this vocabulary already holds
				// everywhere else: a produced resource nobody asked for is an empty
				// promise. It has no precedent either. Every durable engine records step
				// history for its own replay and its own UI; NONE of them stands up an
				// application-facing completion bus by default. Step Functions emits
				// execution status to EventBridge at the WORKFLOW level, opt-in;
				// Cloudflare's subscriptions are instance-level; Conductor's listener is
				// opt-in; where a per-step signal exists at all it is a hook the author
				// attaches.
				//
				// AND NOTHING ELSE EITHER. There is no store for the run's state, because the
				// run's state is the MESSAGE: what the run carries travels with it from
				// one step's queue to the next, and which queue it is sitting in IS
				// which step it is at. A pipeline needs no second place to write that
				// down, and the second place is what would have to be kept in agreement
				// with the first.
				//
				// What is genuinely missing is run HISTORY — what ran, when, and what it
				// returned — which is a database and a decision, not a queue.
			),
			Resource("workflow", "Workflow", "Workflow", "workflow"),
		)),

	// NO `connector` KIND, and this is the note that stops it coming back by halves.
	//
	// It was "a source that is not already something else": a `type` naming an external
	// system, a `credentials` reference, and `polls` — hold the connection, and exactly
	// one node in the cloud runs it. It lost Kafka and NATS to `stream.engine` and
	// Postgres, MySQL and Mongo to `database.engine` on 2026-08-31, and what was left
	// was argued to still earn the kind. It does not, and every remaining case is
	// spelled by kinds this vocabulary already has:
	//
	//   - AN OBJECT STORE AS A SOURCE — s3, gcs. That is an argument for giving
	//     `storage` an engine axis, the way `database` and `stream` have one; it was
	//     never an argument for a second kind that names a storage server.
	//   - A POLLED VENDOR API. A `cron` fires a `function`, the function polls and
	//     publishes what it read to a `stream`. Three kinds, all here, and the polling
	//     logic is the author's — which it was going to be anyway.
	//   - CREDENTIALS. A workload reaches a `secret` through its access block. That is
	//     the one construct for it and a second spelling was always a copy.
	//   - "EXACTLY ONE NODE RUNS IT". That comes from the schedule: a cron fires once,
	//     wherever it fires. Nothing about it needs a kind to be true.
	//
	// WHAT IS ACTUALLY LEFT is a long-lived HELD socket to a vendor that is not a kafka
	// or a nats topic — and that is an `application`: a container that runs
	// continuously, with `egress` naming the host and a `secret` holding the credential.
	// What `connector` added on top of that was one bit, `polls`, meaning "and only one
	// copy of it runs". That is a PLACEMENT fact, and placement is already a block on
	// every kind with a footprint. A kind whose entire remaining content is one
	// placement bit is one placement bit.
	//
	// The other half of the old argument was that the PLATFORM ships the client —
	// `source: builtin`, closed to authors, so "tau's slack poller" had no spelling.
	// That only bites if tau ships connector implementations. It does not, none is
	// planned, and if the author writes the function it never arises.
	//
	// WHAT IT COSTS, said out loud: this was one of the vocabulary's two extensibility
	// seams and the only OPEN one — `connector.type` was a free string on purpose. What
	// remains open is a workload's `egress` allowlist plus a `secret`, which is the
	// same openness one construct further out: any workload may reach any host it lists,
	// and nothing has to name the vendor as a kind first. node.Register still covers
	// services.
	//
	// WHAT WOULD BRING IT BACK is not this kind: it is a singleton placement fact
	// beside `region`, or tau shipping vendor clients. Both are the owner's calls and
	// neither is a `connector`.
}

// rootAttributes is the project root. `notification.email` is deleted: it has been
// inert since it was added and nothing reads it. An inert field is a lie with a
// type signature — it comes back as a `notifications:` block with destinations when
// the notification subsystem lands.
func rootAttributes() []*Attribute {
	return []*Attribute{
		String("id", IsCID(), Required(), Generated(GenCID), EmitValidation("project_id", "project_id"), Doc("ID", "Content-addressed identifier (CID) of the project.")),
		String("name", IsVariableName(), Doc("Name", "Project name. Must be a valid variable name.")),

		// Declared so a consumer can DISCOVER it — engine.DerivedBy names the
		// derivation and its inputs — rather than each one hard-coding "hash the id
		// and base32 it". It is never authored and so never appears in the compiled
		// object; the validator is what says so out loud, because a key the schema
		// knows would otherwise be silently accepted from a document that set it, and
		// a stale copy of a derived name is exactly the drift deriving it avoids.
		String("zone_label", Derived(DeriveDNSLabel, "id"), NoAccessors(),
			Validator(func(v string) error {
				if v == "" {
					return nil
				}
				return errors.New("zone-label is derived from the project id, not authored; remove it")
			}),
			Doc("Zone Label", "The project's label in a cloud's generated zone, derived from its id. Read-only.")),
		String("description", Doc("Description", "Free-form, human-readable description of the project.")),
		StringSlice("tags", WireDrop(), Doc("Tags", "Project-level labels.")),

		// THE CEILING, AND IT IS A SCOPE'S STATEMENT RATHER THAN A RESOURCE'S. A
		// resource says where it IS placed; a scope says where anything under it may be
		// placed at all, and the difference is that this one is inherited. Absent means
		// the parent's, and absent all the way up means wherever the platform compiling
		// it places it — the audit asks its Regions (regions.go), so a project that never
		// heard of the feature writes nothing and is bounded only by what the platform
		// serves.
		//
		// NARROWING ONLY, which the audit enforces because nothing else can: the rule is
		// cross-document, so neither the schema nor load time can see both ends of it.
		// A scope that widened its parent's list would mean a child running where its
		// parent said nothing may, which is the whole reason the ceiling exists.
		// THE SAME WORD A RESOURCE USES, and deliberately: a scope's regions are
		// where everything under it may be, and a resource's are where it IS.
		// One concept narrowing down, rather than an allowance and a placement
		// that differ by a letter.
		//
		// IT IS ALSO THE DEFAULT. A resource that declares nothing is placed
		// across this, which is what makes the block optional for almost
		// everything — and PerEnv is what lets production stay in the EU while
		// preview goes somewhere cheaper, in one line at the top of the project.
		StringSlice("regions", PerEnv(),
			Doc("Regions", "The regions everything in this project may be in, and where anything that does not say otherwise is placed. Absent means wherever the platform places it. A scope below may narrow this list and may never widen it.")),
	}
}

// namespacesGroup is the SCOPE container — a directory whose instances hold a
// nested copy of every resource group, each resolving names in its own scope.
//
// v1 called this `applications`, which was v1's word for a scope and is now the
// compute kind. tcc is agnostic about the name; v2 uses `namespaces`, which is also
// what the access, quota and connection rules are written about.
// MaxNamespaceDepth is how deep namespaces may nest. It is a real cap, not a
// formality: the schema is a finite node tree, so the nesting has to be declared
// rather than expressed as recursion.
//
// A self-referential node — making the group a child of its own iterator — is the
// obvious spelling and it does not work: the node graph becomes cyclic, and the
// interpreter's schema walk has no visited set, so it recurses until the stack is
// gone (interp/shape.go's eachIterIn). Eight matches the parent-chain cap the model
// already assumes.
const MaxNamespaceDepth = 8

// namespacesGroup is the scope container — v1's `applications`, with one change:
// in v2 a namespace holds namespaces, so a reseller's tenant can hold its own
// tenants. Each level is a distinct copy of the node tree, built down to depth.
// scopeContainer is the group whose instances hold resources of their own — the
// top-level key, and the directory a scope is. It was read back off the compiled root
// by everything that needed it, on the argument that a constant would be a second
// place to rename it; naming it here and BUILDING the group from it makes this the
// only place, and stops the index footprint reading the root while the root is being
// built.
const scopeContainer = "namespace"

func namespacesGroup(depth int) *Node {
	children := append([]*Node{}, V2Resources...)
	if depth > 1 {
		children = append(children, namespacesGroup(depth-1))
	}
	return DefineGroup(scopeContainer,
		// THE ALLOWANCE, ON THE SCOPE AND NOT IN identityAttributes(). That helper is
		// shared with every resource, and a resource does not get to widen or narrow
		// what may run under it — it IS one of the things placed. So the field is
		// declared here, where the container's own attributes are, and the project root
		// declares the same one.
		DefineIterGroup(append(identityAttributes(),
			StringSlice("regions", PerEnv(), InSection("identity"),
				Doc("Regions", "The regions everything in this namespace may be in, and where anything that does not say otherwise is placed. Absent means the parent's. Narrows only; the audit reports a widening."))),
			children...).
			With(Singular("Namespace"),
				// namespace/shop/meta.yaml — the document this scope's own fields live
				// in, kept distinct from the kind-named directories beside it that hold
				// what it contains.
				//
				// NOT "namespace", which is what it was called while the kinds were
				// plural. With singular kinds the nested namespaces live in a child
				// group ALSO called `namespace`, at the same level — and a leaf whose
				// name collides with a child group is skipped as the leaf and never
				// walked as a group. A leaf must not be named after any child of its own
				// group, which is why no kind name can serve here and `meta` — not a
				// kind, and not a thing anyone deploys — can.
				Leaf("meta"),
				GroupDoc("A named scope holding resources, and namespaces, of its own. Names resolve here first, then in the parent."),
				secIdentity,
				Icon("apps"),
				// A namespace is looked up by name like everything else: `/shop/eu`
				// is written as names and the object is keyed by ids, so something
				// has to turn one into the other. Last position, after every kind.
				interp.Indexes(len(V2Resources), byName)))
}

var scopeShape = namespacesGroup(MaxNamespaceDepth)

var _ = func() any {
	attachPlacement()
	attachBuild()
	attachAccess()
	attachIndexes()
	attachLowerable()
	return nil
}()

// ENVIRONMENTS ARE ONE FILE, and they are in the project's own document rather than a
// directory of their own. They are not resources: nothing reaches an environment, no
// lowering produces one, and there is no verb to do to it. It is a small table of
// deployments — which exist, which branches feed each, where each keeps its state —
// read whole or not at all, and a table read whole belongs in one document.
//
// Declaring them in config is also what tells the compiler which environments a
// document may suffix a key for, so `timeout_prevew` is a typo with the declared names
// in the message rather than a key nobody reads.
//
//	environments:
//	    production:
//	        branches: [main, master]
//	        regions: [eu-west]
//	        shadows:
//	            ab:
//	                branches: [main]
//	    preview:
//	        branches: ["*"]
func environmentsGroup() *Node {
	return DefineGroup("environments",
		DefineIter([]*Attribute{
			environmentBranches(),

			// WHERE THIS ENVIRONMENT'S STATE LIVES. A shadow has no such field, which
			// is the whole reason shadows are written INSIDE the environment they
			// shadow rather than pointing at it: the state is not a shadow's to place,
			// and a shape that cannot say the wrong thing beats a check that catches it.
			//
			// Absent means the cloud decides, which is the common case and the one
			// §10a wants: a region an operator owns is not the project's to name, and
			// a project that hardcoded one would not survive being dropped on another
			// cloud. What is authored is a CONSTRAINT — "keep this in the EU" — not a
			// placement.
			StringSlice("regions", InSection("deployment"),
				Doc("Regions", "Where this environment keeps its state, as region names the cloud offers. Absent means the cloud places it.")),
		},
			GroupDoc("The deployments of this project: which branches feed each, and where each keeps its state."),
			Section("deployment", "Deployment", "Which branches feed an environment, and where it keeps its state."),
			Nested(shadowsGroup())),
	)
}

// A SHADOW IS A DEPLOYMENT ATTACHED TO ANOTHER'S STATE. It is its own deployment —
// its own release, its own compiled artifact, its own address, its own assets — and it
// shares everything that does not fork with the environment it is written inside:
// every stateful kind, the secrets, and the TRIGGERS. A shadow starts no scheduler and
// consumes no queue of its own, because the environment holding it is already doing
// that.
//
// Which is the point: an A/B variant and a frontend tested against production data are
// both "different code, same data".
//
// WRITTEN INSIDE, not pointing at. A `shadows: production` field would be a name to
// misspell, a reference with no namespace to resolve in, and a place to write `regions`
// that a shadow may not have. Nested, all three problems are shapes that do not exist.
//
// The word is borrowed and narrowed. Elsewhere a shadow deployment gets a COPY of
// traffic with its answers thrown away and shares nothing; here it is a real deployment
// sharing the state of the one above it. Both senses mean "attached to that one rather
// than standing alone", which is the half worth keeping.
func shadowsGroup() *Node {
	return DefineGroup("shadows",
		DefineIter([]*Attribute{
			environmentBranches(),
		},
			GroupDoc("Deployments attached to this environment: their own code and their own address, over its state.")),
	)
}

// environmentBranches is the same field on both, because "which branches feed this
// deployment" is one question whether the deployment stands alone or shadows another.
//
// A LIST OF PATTERNS. v1 refused every branch but `main`/`master` from a literal list
// read in fifty places, so a team whose default branch is `trunk` could not deploy at
// all and `feature/*` was unrepresentable. A branch is a value here, never a path
// segment — and `[main, master]` is one environment fed by either, which is what that
// literal list was trying to say.
//
// ONE BRANCH MAY FEED SEVERAL DEPLOYMENTS, and that is not a collision to resolve — it
// is what a shadow is for. `main` feeds production and every shadow of it that names
// it: one commit, several deployments, one set of state underneath.
func environmentBranches() *Attribute {
	return StringSlice("branches", Required(), InSection("deployment"),
		Doc("Branches", "Which branches feed this deployment. Globs allowed: [main, master], [\"release/*\"], or [\"*\"] for everything else."))
}

var v2Root = Root(
	rootAttributes(),
	append(append([]*Node{}, V2Resources...), scopeShape, environmentsGroup())...,
).With(Singular("Project"), Leaf("project"), Strict(),
	// A resource is stored under its `id` and called by the name of the document it was
	// written in — the interpreter's two field names, which are this vocabulary's to
	// choose and therefore this vocabulary's to state.
	Identity("id", "name"),
	// AND ITS ONE VARIABLE. Environment is the axis a key may be suffixed with here;
	// the engine has no such word, so the root is where it is said. A second axis would
	// be a second name on this line, in precedence order, and nothing below it changes.
	Varying(EnvironmentVar))

var TaubyteProject = SchemaDefinition(v2Root)

// GenerationRoot is the node list a generator walks: the resource groups plus the
// applications container, read off the real root so no curated list can drift.
func GenerationRoot() []*Node { return v2Root.Children }

// KindRoot is the shape of kinds in a compiled project. In particular workflow and agent
// remain those kinds while also carrying the same structural container role as namespace.
func KindRoot() []*Node { return kindRootNode().Children }

var (
	kindRootOnce sync.Once
	compiledRoot *Node
)

func kindRootNode() *Node {
	kindRootOnce.Do(func() { compiledRoot = interp.LoweredRoot(v2Root) })
	return compiledRoot
}

// CompileRoot is the interpreter's entrypoint into the DSL: the whole project root.
func CompileRoot() *Node { return v2Root }

// transformersGroup is the middleware chain on an ingress: the functions traffic
// passes through, in the order they run.
//
//	transformers:
//	    - function: redact
//	      fail: closed
//	    - function: /shop/tier_header
//
// A LIST, because a list is ordered by being a list. It was a map keyed by the
// function's name with an `order:` integer inside each entry — which is a list with the
// ordering written out by hand, because a yaml mapping has none. The integer existed
// only to put back what the shape had thrown away.
//
// A list of OBJECTS rather than of names, because a use of a transformer carries
// decisions that belong to the use and not to the function: the same `redact` can be
// advisory on one gateway and mandatory on another. A list of bare names has one place
// to put that — nowhere — and the workaround is a second parallel list, which is how
// config formats end up with `transformers` and `transformers_required`.
//
// A transformer is always a FUNCTION, so an element does not say which kind — it says
// where, in the two fields every other cross-namespace reference in this vocabulary
// uses. `namespace` omitted means this scope and then its parents, exactly as it does
// on a handler and on an authorizer.
//
// THERE IS NO `direction`. It said inbound / outbound / both, and it was wrong to be
// here: whether a function can touch the response is a property of the host interface
// it was built against, not of this use of it. A function that only sees the request
// cannot be made to see the response by a word in the config, and one that sees both
// does not need permission. If a response-only chain is ever wanted, it is another
// world in `implements`, not a flag here.
// stepsGroup is the workflow's pipeline: an ordered list, because the order IS the
// program. Every field here is one the ENGINE can carry — a list element does not get
// the interpreter's passes, so `retries` is an integer rather than a Duration and a
// step names its function with the namespace+name pair used everywhere else.
// terminalStep is the name a step's on_error takes when nothing is written: not a step,
// but the terminal queue `{workflow}_failed`. checkWorkflows knows it for the same reason
// the lowering does — it is the one on_error value that names no step and is still right.
const terminalStep = "failed"

// entryGroup is where an operation ON a lowered kind is actually carried out, keyed by the
// operation. `start` on a workflow is a send to the start step's queue; a consumer reads
// that here rather than knowing that a workflow becomes queues and how they are named.
//
// Nothing authors it. A lowering writes it, which means the record of that write is what
// takes it back off again — so decompile returns the document somebody wrote, with no
// second mechanism to teach.
func entryGroup() *Node {
	return DefineGroup("entry",
		DefineIter([]*Attribute{
			String("kind", InSection("entry"),
				Doc("Kind", "The kind of resource the operation lands on.")),
			String("name", InSection("entry"),
				Doc("Name", "Which resource of that kind, in the scope this became.")),
			String("as", InSection("entry"),
				Doc("As", "Which of that kind's own operations carries it out.")),
		}, GroupDoc("Where an operation on this resource is carried out, keyed by the operation.")))
}

func stepsGroup() *Node {
	return DefineList("steps", []*Attribute{
		String("name", Required(), IsVariableName(), NotNamed(terminalStep), InSection("steps"),
			Doc("Step", "What this step is called. Another step's on-error names it, the queue it becomes is named after it, and a start names one.")),

		// WHAT THE STEP RUNS, as a block rather than a bare name, because a step has
		// more to say about its call than which function: what it is handed, and where
		// the answer is kept.
		//
		// WHICH KEY IS PRESENT IS THE TYPE. There is no `type:` field beside these and no
		// Either() capturing one — a step that opens `call:` is a call and a step that
		// opens `wait:` is a wait, and materialising that a second time is the same fact
		// twice for two edits to disagree about.
		//
		// It is also what a captured branch key cannot do here. A resource's attributes
		// are written back through their declared paths, so a matcher path has no
		// authored key and decompile skips it — `storage` compiles `type: object` and
		// decompiles to `object:` with no `type` in sight. A list element is written back
		// as DATA, so a captured key would land in the yaml and the next compile would
		// refuse it as unknown. Plain keys have no such half: what compiles is what was
		// written.
		//
		// What that costs is the declaration refusing a step that opens two of them, so
		// checkWorkflows says it instead — the one place in this vocabulary where a
		// mutual exclusion is a check rather than an Either(), and the reason is written
		// here so nobody 'fixes' it into one.
		String(Key("call", "function"), Path("call", "function"), IsVariableName(), Ref("function", ByName()), InSection("steps"),
			Doc("Function", "The function this step runs.")),
		String(Key("call", "namespace"), Path("call", "namespace"), IsNamespacePath(), InSection("steps"),
			Doc("Namespace", "Path to the namespace holding the function, e.g. \"/shop/eu\". Omitted means this scope, then its parents.")),

		// WHAT IT IS HANDED AND WHERE THE ANSWER GOES. Every declarative peer has these
		// two — ASL's Arguments/Output, Google Cloud Workflows' args/result, Conductor's
		// input parameters — and without them a pipeline past the trivial case has to
		// reshape its message inside the functions, which puts wiring in code.
		//
		// `input` is an EXPRESSION, parsed where it is written. `result` is a NAME: it
		// says where in the run's state the answer is kept, and a name is not computed.
		String(Key("call", "input"), Path("call", "input"), IsExpression(), InSection("steps"),
			Doc("Input", "What the function is handed, as an expression over the run's state, e.g. \"order.lines\". Omitted hands it the whole state.")),
		String(Key("call", "result"), Path("call", "result"), IsVariableName(), InSection("steps"),
			Doc("Result", "Where the function's answer is kept in the run's state, by name. Omitted means the answer replaces the state.")),

		// A STEP THAT ONLY WAITS. Not a Duration(): scalar codecs do not run inside a
		// list element (§2a), so this is carried as the human string it was written as
		// and the driver parses it — which is where the delay is applied anyway.
		String(Key("wait"), Path("wait"), InSection("steps"),
			Doc("Wait", "Pause for this long, then carry on: a human string (e.g. \"30s\", \"1h\"). A step waits or calls, never both.")),

		// ATTEMPTS, not retries. "3 retries" is three or four runs depending on who is
		// reading, and the number lands in a queue's `max_attempts`, which counts runs —
		// so the config counts what the thing it becomes counts, and the lowering is a
		// copy rather than arithmetic. Templates must not grow a calculator.
		//
		// Not a duration either: what a step may TAKE is the function's own
		// execution/timeout, and a second ceiling here would be a number that disagrees
		// with it.
		Int("attempts", MinInt(1), Default(1), InSection("steps"),
			Doc("Attempts", "How many times this step is run before it has failed. 1 means no retry.")),

		// HOW THE ATTEMPTS ARE SPACED. Strings rather than Durations for the same reason
		// as `wait`, and the queue's own Duration fields parse them: this is copied onto
		// the kind that enforces it, which is where redelivery lives. No defaults — a
		// step that says nothing produces a queue with no retry block, which is the
		// honest reading of "no backoff".
		String(Key("backoff", "interval"), InSection("steps"),
			Doc("Backoff Interval", "Delay before a failed attempt is redelivered, e.g. \"5s\". Omitted means the queue's visibility timeout alone governs.")),
		Float(Key("backoff", "factor"), InSection("steps"),
			Doc("Backoff Factor", "Multiplier applied to the interval per further attempt. Omitted or 1 means a fixed interval.")),
		String(Key("backoff", "ceiling"), InSection("steps"),
			Doc("Backoff Ceiling", "The delay never grows past this, e.g. \"5m\". Omitted means no ceiling.")),

		// DEFAULTED, and the default is not cosmetic. This fills the produced queue's
		// dead_letter, which is what happens the moment attempts run out — the moment
		// no code of this step is running. Without a value the write is dropped, the
		// terminal queue is produced with nothing pointing at it, and where an
		// exhausted run goes becomes something the executor has to invent.
		String("on_error", IsVariableName(), Default(terminalStep), InSection("steps"),
			Doc("On Error", "Another step to run when this one has exhausted its attempts. The default sends the run to the {workflow}_failed queue, where a person picks it up.")),
	}, GroupDoc("One step: what it does, how often it is attempted and how those attempts are spaced, and where failure goes."))
}

func transformersGroup() *Node {
	return DefineList("transformers", []*Attribute{
		String("namespace", IsNamespacePath(), InSection("transformers"),
			Doc("Namespace", "Path to the namespace holding the function, e.g. \"/shop/eu\". Omitted means this scope, then its parents.")),
		String("name", Required(), IsVariableName(), Ref("function", ByName()), InSection("transformers"),
			Doc("Name", "The function's name within that namespace.")),

		// The one decision that is genuinely per-use. It is also the one with a
		// dangerous default in both directions, so it is stated rather than assumed:
		// a filter that fails open stops filtering exactly when something is wrong,
		// and an enrichment that fails closed takes the site down to add a header.
		String("fail", InSet("closed", "open"), Default("closed"), InSection("transformers"),
			Doc("On Failure", "What happens to traffic if this transformer errors or times out: \"closed\" rejects it, \"open\" passes it through untransformed.")),
	}, GroupDoc("Functions traffic passes through, in the order they run."))
}

var secTransformers = Section("transformers", "Transformers", "Functions traffic passes through, in the order they run.")

// variablesGroup is how a workload is configured: entries keyed by the NAME the
// workload reads them under.
//
//	variables:
//	    LOG_LEVEL:   { value: info }
//	    REGION:      { value: eu-west }
//	    STRIPE_KEY:  { secret: stripe }
//
// NOT named `config`, which is the word everyone reaches for first. `config` is the
// the leaf this vocabulary names: loadChildren skips a child key called
// that, because it is where a CONTAINER keeps its own fields, so a nested group by that
// name is read as nothing at all — silently, which is how this was found. `variables`
// is also what v1 called them.
//
// A container reads them as environment variables; a WASM workload reads them through
// the host config interface. Same declaration either way, because "what is this
// workload configured with" is one question and the delivery mechanism is the runtime's
// business.
//
// WHY THIS EXISTED NOWHERE. A workload could declare an image, ports, ceilings and
// which secrets it may READ, but not a log level or an upstream URL — so the only place
// to put one was inside the image, which makes one image per environment and defeats
// naming the image at all. The harmless half of configuration had no path while the
// dangerous half did, which is backwards.
//
// A VALUE OR A SECRET, not both, and the difference is real: `value` is in git and
// visible to anyone who can read the repo; `secret` names a secret and only the
// reference travels. Nothing in the DSL can say "exactly one of these" — a list of
// objects has no representation and neither does a mutual exclusion — so a publish-time
// check owes that one too, alongside the four it already owes.
//
// Referencing a secret here is a USE of it and the graph draws it. It does not replace
// the `secrets:` access block: that block is for a workload that FETCHES a secret while
// running, which is a different thing from one handed a value at start — one can be
// revoked mid-flight and the other cannot.
//
// Not covered, deliberately: configuration as a FILE. An env var has a size limit and
// no structure, so a certificate or a 40KB json document wants a mount. That is a
// second delivery mechanism rather than a second source, and it can be an `as: file`
// with a path when something needs it.
// WHEN THE VALUE IS HANDED OVER. A build argument and a runtime variable are the same
// entry with different lifetimes, and everything this vocabulary has to be able to
// read carries both — a build environment beside a run environment, build args beside
// env. Until this field there was one answer and it was `export FOO=bar` written
// inside `.taubyte/build.sh`: the value committed to the author's repository in
// plaintext, out of reach of anything that reads config.
//
// What it buys that is not just a home: a build-time SECRET. The `secret:` branch and
// `as: file` are already here, so a registry token or a private module credential can
// be handed to a build the way a running workload gets one — a reference in git and
// the value in the secret store — instead of being pasted into a script or baked into
// a layer.
//
// `both` is not a third mechanism. It is the same value handed over twice, which is
// exactly what a variable baked into a bundle at build and read again at run is.
//
// THE LEGAL SET IS THE CALLER'S, the same parameterisation contentSource and
// wasmAttributesEntrypoint already take, and the first element is the default. A kind
// with no running process — a website, a component — has only `build`, so writing
// `at: run` on one is a compile error naming the value that is legal there rather than
// a lifetime nothing honours. That is also the seam: the day a website runs on an
// engine that executes something, `run` joins its set and nothing else moves.
const (
	runLifetime   = "run"
	buildLifetime = "build"
	bothLifetimes = "both"
)

func variablesGroup(lifetimes ...string) *Node {
	return DefineGroup("variables",
		DefineIter([]*Attribute{
			String("at", InSet(lifetimes...), Default(lifetimes[0]), InSection("config"),
				Doc("Handed Over", "run: the workload reads it when it starts. build: the build reads it and the workload never sees it. both: handed over twice, which is what a value baked into a bundle and read again at run is.")),
			String("value", PerEnv(), InSection("config"),
				Doc("Value", "A literal, stored in git and readable by anyone who can read the repo. For anything that is not a secret.")),
			String(Key("secret", "namespace"), IsNamespacePath(), InSection("config"),
				Doc("Namespace", "Where that secret lives. Omitted means this resource's own scope, then its parents.")),
			String(Key("secret", "name"), IsVariableName(), Ref("secret", Inbound()), InSection("config"),
				Doc("From Secret", "Take the value from this secret, as \"secret/<name>\". Only the reference is in git.")),

			// An environment variable has a size limit and no structure, so a
			// certificate, a private key or a large json document cannot travel as
			// one. A file is a second DELIVERY MECHANISM rather than a second source:
			// the value still comes from `value` or `secret`, it just arrives
			// somewhere a program can open.
			String("as", InSet("env", "file"), Default("env"), InSection("config"),
				Doc("Delivered As", "env: an environment variable named by this entry's key. file: a file the workload can read, at `path`.")),
			String("path", ShowWhen("as", "file"), InSection("config"),
				Doc("Path", "Absolute path the file is mounted at, e.g. \"/etc/tls/cert.pem\". Required when delivered as a file.")),
		},
			GroupDoc("One configuration entry, keyed by the name the workload reads it under."),
		))
}

var secConfig = Section("config", "Configuration", "Values handed to the workload when it starts.")

// portsGroup is a workload's own listening ports, keyed by name.
//
//	ports:
//	    http:    8080
//	    metrics: 9090
//	health:
//	    port: http
//	    path: /healthz
//
// The NUMBER here is the internal one — the port the process binds inside its
// container, which its own code already knows. The external port is assigned by the
// platform and tunnelled, so nothing outside the workload ever writes a number:
// health names a port, and so does a gateway route targeting this workload.
//
// That is also why a port is a named entry rather than a bare list. A workload that
// listens on two ports has to be pointed at one, and "the first one" is not an
// answer anybody can read.
func portsGroup() *Node {
	return DefineGroup("ports",
		DefineIter([]*Attribute{
			Int("number", Required(), MinInt(1), InSection("network"),
				Doc("Number", "The port the process binds INSIDE the container. Not the one anything outside reaches it on.")),
		},
			GroupDoc("A port this workload listens on, named so everything else can refer to it without repeating the number."),
		))
}
