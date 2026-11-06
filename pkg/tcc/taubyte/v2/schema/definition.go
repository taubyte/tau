// Package schema is the tau v2 configuration vocabulary: the DSL declaration the
// generic tcc interpreter compiles.
//
// Against v1: a function declares nothing about how it is reached (reachability
// is an inbound edge on whatever reaches it); smartops is gone; `libraries` is
// `component`; database grew a `type` over kv, document, sql and vector; a
// `build:` stanza says how a resource is made, with `image:` as one source
// branch; the scope container is `namespaces`; and a resource declares what it
// reaches as a top-level block per target kind.
package schema

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/taubyte/tau/pkg/tcc/interp"
)

// commonAttributes prepends the identity block and appends the access block,
// carried by every resource: no kind may only be a target.
func commonAttributes(attrs ...*Attribute) []*Attribute {
	return append(identityAttributes(), attrs...)
}

// resource is every kind in this vocabulary: one flat file, id at the document
// root, indexed by name. Access groups are attached later by attachAccess, since
// they are generated from every kind's Ops().
func resource(attrs []*Attribute, opts ...any) *Node {
	return DefineIter(attrs, opts...)
}

// attachAccess wires access once the vocabulary exists, mutating the nodes the
// namespace levels share:
//
//	every kind          gains the access groups (what it reaches)
//	a kind with Ops()   also gains an authorizer (who decides what reaches it)
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

// attachLowerable gives every lowering kind its two shapes: authored, the flat
// resource; compiled, the same kind and identity as a container over the resource
// groups, with its own fields in meta.yaml.
func attachLowerable() {
	for _, g := range V2Resources {
		if len(g.Children) == 0 {
			continue
		}
		if iter := g.Children[0]; len(interp.LoweringsOf(iter)) > 0 {
			children := append([]*Node{}, iter.Children...)
			// Every kind but its own: with its own group in the after shape, an access entry
			// naming another agent (`agent: {support: {ops: [invoke]}}`) would also read as a
			// directory of contained agents.
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

// Category declares what a kind is for, so consumers grouping resources do not
// keep their own drifting tables.
//
//	internet  makes something reachable from outside the cloud
//	trigger   fires a workload, from inside
//	compute   runs code
//	data      holds state
//	code      a build artifact
func Category(name string) NodeOption { return GroupAnnotate("category", name) }

// egressAttribute is a workload's allowlist of hosts, on top of the node-wide
// deny floor (pkg/netguard) it can never widen. A host a declared reference
// already names (a model's endpoint, an external engine) is reachable without
// listing it, so a rotated endpoint lives in one place; this list is for hosts
// nothing in the config names.
func egressAttribute() *Attribute {
	return StringSlice("egress", PerEnv(), InSection("network"),
		Doc("Egress", "Hosts this workload may reach that nothing else in the project names. A host declared by something this resource references — a model's endpoint, an external database's host — is already reachable and does not belong here. Layered on the node-wide deny floor, which it can never widen; absent means the floor alone."))
}

var secNetwork = Section("network", "Network", "How this workload is reached, and what it may reach.")

// hostWorlds are the interfaces a WASM workload can be built against. A function
// named in different places is handed different worlds, so it declares one
// (default `handler`) and Audit compares it with each use. This is the config's
// word, not the artifact's; the component's WIT imports would be the better
// source.
var hostWorlds = []string{"handler", "authorizer", "http-authorizer", "dns-resolver", "instance-validator", "gateway-transformer", "proxy-transformer", "gateway-router", "proxy-router"}

// HostWorlds is every host interface a workload may be built against, exported so
// the tiers ask the compiler's question rather than keep copies.
func HostWorlds() []string { return append([]string(nil), hostWorlds...) }

// runnableAttributes is the shape of a kind that runs code. The tier is the
// package's type, so it is not written here; neither is the entrypoint's
// requirement, which only a wasm package has (checkEntrypoints).
func runnableAttributes() []*Attribute {
	base := []*Attribute{
		String("implements", InSet(hostWorlds...), Default("handler"), InSection("code"),
			Doc("Implements", "Which host interface this was built against. handler: takes a request. authorizer: takes a caller and an operation, answers with ops. gateway-transformer: takes a parsed HTTP request. proxy-transformer: takes bytes on a stream. gateway-router: takes a parsed HTTP request, decides which route or function should handle it. proxy-router: takes bytes on a stream, decides which target should receive the connection.")),
	}
	base = append(base, packageRef()...)
	base = append(base,
		String(Key("execution", "call"), InSection("code"),
			Doc("Entrypoint", "The export the host calls: a symbol of a wasm package's component, or an export of a warp package's module. Required for wasm; a warp package defaults to its default export.")),
		// A ceiling is what an environment differs in; identity never carries an
		// environment mark.
		Duration(Key("execution", "timeout"), Required(), PerEnv(), InSection("limits"), Doc("Timeout", "Maximum execution time, as a human string (e.g. \"30s\").")),
		Bytes(Key("execution", "memory"), Required(), PerEnv(), InSection("limits"), Doc("Memory", "Maximum memory this workload may use, as a human string (e.g. \"32MB\").")),
		cpuLimit(),
		egressAttribute(),
	)
	return commonAttributes(base...)
}

var secCode = Section("code", "Code", "The workload's code source and entrypoint.")
var secLimits = Section("limits", "Limits", "Runtime resource limits.")

var secSource = Section("source", "Source", "Where the code is sourced from.")

// cpuLimit is in millicores (1000 is one core), an integer so nothing rounds
// differently. Optional, unlike memory: CPU is a rate, and an absent ceiling
// competes fairly.
// applicationAttributes is the container kind's shape; readiness and liveness are
// the same five fields twice.
func applicationAttributes() []*Attribute {
	out := probeAttributes("readiness", "network", "Whether this instance should be given work.")
	out = append(out, probeAttributes("liveness", "network", "Whether this instance is still working, or should be replaced.")...)
	return append(out,
		// Consecutive liveness failures before a kill. Asymmetric cost: a liveness blip
		// destroys state and warm caches. Three: one is a network hiccup, ten is a minute
		// of traffic to a wedged instance.
		Int(Key("liveness", "failures"), MinInt(1), Default(3), InSection("network"),
			Doc("Liveness Failures", "Consecutive failed liveness checks before this instance is replaced.")),

		Bytes(Key("execution", "memory"), Required(), PerEnv(), InSection("limits"), Doc("Memory", "Maximum memory per instance.")),
		cpuLimit(),
		egressAttribute(),

		// Ceilings, not counts: how many instances run stays the platform's.
		Int(Key("limits", "max_instances"), MinInt(1), PerEnv(), InSection("limits"),
			Doc("Max Instances", "The most instances the platform may run of this workload. A ceiling it scales within, not a count.")),
		Int(Key("limits", "concurrency"), MinInt(1), PerEnv(), InSection("limits"),
			Doc("Concurrency", "The most simultaneous requests one instance may be given. Cloud Run and Knative call this concurrency; Fly calls it a hard limit. Above it the platform queues or starts another instance, within max-instances.")),
	)
}

// probeAttributes is a yes/no question the platform asks a workload:
//
//	readiness   should this instance be given work?   no -> out of rotation
//	liveness    is this instance still working?       no -> kill and replace
//
// A cache warming is not ready but must not be restarted; a deadlock is neither.
// Either probe is an http poll or a function, as an Either()+Key() switch.
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

// routerAttributes: a function that routes the request itself, in place of the
// static route block, for routing that is a program rather than a table. The kind
// is not written since only a function can be a router. Unlike a handler it does
// not end the request, hence its own world.
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

// httpAuthorizerAttributes guard traffic arriving at a name: per route or
// gateway-wide, the route's answering. Not authorizerAttributes, which is asked
// about a workload caller; this world is asked about the request and answers with
// the ops it gets.
func httpAuthorizerAttributes(at ...string) []*Attribute {
	nm := append(append([]string{}, at...), "authorizer", "name")
	ns := append(append([]string{}, at...), "authorizer", "namespace")
	where := "any route this gateway serves"
	if len(at) > 0 {
		where = "this route"
	}
	return []*Attribute{
		// No Serves(): a guard is asked about traffic, not its destination.
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

// certificateSecret is one secret holding the chain and private key, like a
// Kubernetes TLS secret: a key never sits in the repository, and the pair is
// issued, expires and rotates together.
func certificateSecret(section string) []*Attribute {
	return []*Attribute{
		String(Key("certificate", "secret", "namespace"), IsNamespacePath(), InSection(section), ShowWhen(Key("certificate", "source"), "provided"),
			Doc("Certificate Secret Namespace", "Where that secret lives. Omitted means this resource's own scope, then its parents.")),
		String(Key("certificate", "secret", "name"), IsVariableName(), Ref("secret", Inbound()),
			RequiredWhen(Key("certificate", "source"), "provided"), Field("CertSecret"), InSection(section), ShowWhen(Key("certificate", "source"), "provided"),
			Doc("Certificate Secret", "Secret holding the certificate chain and its private key. The key never goes in git.")),
	}
}

// instanceAttributes says a store holds many named instances, how many, and who
// admits each. The block's presence is the declaration; both inner fields are
// required. An instance name is a key looked up exactly, never a pattern, which
// replaces v1's match/regex. As with an authorizer the target owns the policy: the
// store names an `instance-validator` function, the caller only the instance.
func instanceAttributes() []*Attribute {
	return []*Attribute{
		// The captured switch is what makes the rest of the block mandatory: Required()
		// fires whether or not the block exists, and there is no defaulted sibling for a
		// RequiredWhen.
		String(Key("instances", "admission"), Path("instances", Either("validator")), Captures(), InSection("instances"),
			Doc("Admission", "How a new instance is let in. validator: a function decides.")),
		String(Key("instances", "validator", "namespace"), Path("instances", Either("validator"), "namespace"), IsNamespacePath(), InSection("instances"),
			Doc("Validator Namespace", "Where that function lives. Omitted means this resource's own scope, then its parents.")),
		String(Key("instances", "validator", "name"), Path("instances", Either("validator"), "name"), IsVariableName(), Ref("function", ByName()),
			RequiredWhen(Key("instances", "admission"), "validator"), InSection("instances"),
			Doc("Validator", "A function called before an instance is opened or created: it decides whether that instance may exist, and returns the parameters it gets.")),
		// No default and no absent state: `0` means no ceiling, so unbounded is written
		// down. Not `limits/max_instances`, which bounds copies of one workload; this
		// bounds distinct named instances.
		Int(Key("instances", "limit"), Path("instances", "limit"), MinInt(0),
			RequiredWhen(Key("instances", "admission"), "validator"), PerEnv(), InSection("instances"),
			Doc("Limit", "The most instances this may hold at once. 0 means no ceiling — which is a choice, not a default, so it has to be written.")),
	}
}

var secInstances = Section("instances", "Instances", "Whether this holds one instance or many, how many, and who admits a new one.")

// Footprint marks a kind whose instances run or store bytes, so one stating no
// placement still runs anywhere its allowance permits; a kind without one is held
// where what reaches it is. It only decides what an absent region block means.
// Not derived from Category: website, model, channel and secret are the
// exceptions.
func Footprint() NodeOption { return GroupAnnotate("footprint", true) }

// FootprintIter reports whether a kind's iterator declares Footprint().
func FootprintIter(iter *Node) bool { f, _ := iter.Meta["footprint"].(bool); return f }

// FootprintKinds is every kind that declares Footprint(), in declaration order. Read
// back off the schema rather than kept beside it, so there is one answer.
func FootprintKinds() []string {
	var out []string
	for _, g := range V2Resources {
		if len(g.Children) == 0 || !FootprintIter(g.Children[0]) {
			continue
		}
		if key, _ := g.Match.(string); key != "" {
			out = append(out, key)
		}
	}
	return out
}

// Completes declares a kind runs to completion: an invocation starts it, its exit
// is the answer, and nothing listens.
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

// attachPlacement hangs the Inherited() region block off every kind except a
// scope container, whose `regions` is the allowance. It runs before
// attachLowerable, which copies a kind's attributes into its lowered shape.
func attachPlacement() {
	for _, g := range V2Resources {
		if len(g.Children) == 0 || g.Children[0].Group {
			continue
		}
		iter := g.Children[0]
		iter.Attributes = append(iter.Attributes, regionAttributes()...)
		secPlacement(iter)
	}
}

// regionAttributes is where one resource may run: its placement, within the
// allowance (`regions` on the project and namespaces). A region name is an opaque
// label the implementation defines, so the schema never checks one: checkRegions
// does against a declared allowance, and the audit's Regions against what the
// platform serves. The policy is a switch; failover names main and standby rather
// than relying on list position.
//
//	regions:                regions:                regions:
//	    pinned: [eu, us]        cdn: [eu]               failover:
//	                                                        main: eu
//	                                                        standby: us
//
// Regions vary per environment on the chosen branch (`pinned_preview: [us]`);
// the policy itself cannot, since it is the branch key. An absent block means
// anywhere the allowance permits, so the switch is not Required().
func regionAttributes() []*Attribute {
	return []*Attribute{
		String(Key("regions", "policy"), Path("regions", Either("pinned", "cdn", "failover")), Captures(), Inherited(), InSection("placement"),
			Doc("Placement", "pinned: these regions only — the data-residency line. cdn: seeded here, then follows demand within the allowance. failover: exactly two, the first one main. Omit the block for anywhere the allowance permits.")),

		// A list: a resource pinned to eu and us has copies in both.
		StringSlice(Key("regions", "in"), Path("regions", Either("pinned", "cdn")),
			RequiredWhen(Key("regions", "policy"), "pinned"), PerEnv(), Inherited(), EmitValidation("region", "region"), InSection("placement"),
			Doc("Regions", "pinned: every region this is kept in. cdn: the regions it starts in before demand moves it — omit them and the platform seeds the choice.")),

		// Failover keeps named fields: which region is main is the whole statement.
		String(Key("regions", "main"), Path("regions", "failover", "main"), RequiredWhen(Key("regions", "policy"), "failover"), ShowWhen(Key("regions", "policy"), "failover"), Inherited(), EmitValidation("region", "region"), InSection("placement"),
			Doc("Main", "Where this runs while it can.")),
		String(Key("regions", "standby"), Path("regions", "failover", "standby"), RequiredWhen(Key("regions", "policy"), "failover"), ShowWhen(Key("regions", "policy"), "failover"), Inherited(), EmitValidation("region", "region"), InSection("placement"),
			Doc("Standby", "Where it runs when the main is gone.")),
	}
}

var secPlacement = Section("placement", "Placement", "Where this may run, within what its scopes allow.")

// A tier is named by the package type it runs, so the artifact and what runs it are
// one word (package.go):
//
//	wasm    a component-model component; WIT is the author contract
//	warp    a JS bundle, run by the JS engine embedded in the worker
//	oci     an OCI image, so a workload built elsewhere arrives unchanged
//	assets  a directory of files, served rather than executed
//
// Another tier is a package type and the word in each kind's Engines(); consumers
// ask EnginesOf.

// The kinds that run on more than one tier. Which one is the package's type.
var (
	functionEngines = []string{wasmPackage, warpPackage}
	workerEngines   = []string{wasmPackage, warpPackage, ociPackage}
)

// Engines declares which tiers may run a kind, so no consumer keeps its own
// table. Never an attribute: the tier is the type of the package the workload
// names, and checkPackages refuses one the kind cannot run.
func Engines(names ...string) NodeOption { return GroupAnnotate("engines", names) }

// EnginesOfIter reads a kind's declared tiers off its iterator.
func EnginesOfIter(iter *Node) []string {
	names, _ := iter.Meta["engines"].([]string)
	return names
}

// EnginesOf is which tiers may run a kind; empty for a kind that runs nothing.
func EnginesOf(kind string) []string {
	for _, g := range V2Resources {
		if key, _ := g.Match.(string); key != kind || len(g.Children) == 0 {
			continue
		}
		return EnginesOfIter(g.Children[0])
	}
	return nil
}

// handlerKinds are what may serve a route, closed so an unknown key is a compile
// error. An agent answers a request like a function. A bus is reached through a
// route like everything else, so it gets the whole pipeline (path, CORS, rate
// limit, transformers, authorizer) rather than owning a name of its own.
var handlerKinds = []string{"function", "application", "package", "gateway", "mcp", "agent", "bus"}

// externalStreamEngines are pubsub clusters reached over the network, and
// streamEngines adds tau's own. Closed: each is a wire protocol and a client, a
// release rather than a config value.
var externalStreamEngines = []string{"kafka", "nats"}

// storageTypes is what a storage may be. It was written inline in the two
// places that needed it, which is how a closed set becomes two closed sets.
var storageTypes = []string{"object", "streaming"}

// StorageTypes is every data model a storage may be.
func StorageTypes() []string { return append([]string(nil), storageTypes...) }

var streamEngines = append([]string{tauEngine}, externalStreamEngines...)

// TauEngine is what this cloud's own engine is called, wherever a consumer has
// to tell it apart from a server somebody else runs.
func TauEngine() string { return tauEngine }

// Connected reports whether a kind is reached over a socket rather than
// dispatched as a call: every tier it may run on is the container tier.
func Connected(kind string) bool {
	engines := EnginesOf(kind)
	if len(engines) == 0 {
		return false
	}
	for _, e := range engines {
		if e != ociPackage {
			return false
		}
	}
	return true
}

// channelServerKinds are what may answer a channel. A container gets the bytes on one of
// its own ports; a function is dispatched with the connection in hand. Both are real, and
// nothing else is: a website is HTTP by definition and a gateway is an ingress.
var channelServerKinds = []string{"application", "function"}

// proxyHandlerKinds are what may sit behind an L4 proxy: only a container can
// hold a raw connection open. An Either() with one branch so it reads like a
// route's handler.
var proxyHandlerKinds = []string{"application"}

// hostAttributes is how anything internet-reachable gets its name. The generated
// part stays out of the config: a project's label is derived from its id
// (ProjectLabel) and only the cloud's fqdn is unknown until deploy, so one repo
// deploys to any cloud.
//
//	host:
//	    generated:
//	        name: shop        # shop.<project>.<cloud>
//
//	host:
//	    zone:
//	        in:
//	            name: example_com
//	        name: shop        # shop.example.com
//
// A zone, not a domain, so the cloud publishes what the resource needs (A/AAAA,
// SRV for an L4 proxy) and its wildcard covers generated names. Omitting `name`
// means the apex.
func hostAttributes(section string) []*Attribute {
	return []*Attribute{
		String(Key("host", "kind"), Path("host", Either("generated", "zone")), Captures(), Field("HostKind"), InSection(section),
			Doc("Host", "generated: a name under the cloud's own zone, resolved when it is deployed. zone: a name in a zone you own and delegated here.")),
		String(Key("host", "name"), Path("host", Either("generated", "zone"), "name"), IsVariableName(), InSection(section),
			Doc("Name", "The label within that zone, e.g. \"shop\". Omit it for the zone apex.")),
		// Inbound: the zone answers for this name, so no arrow from the gateway to it.
		String(Key("host", "zone", "in", "namespace"), IsNamespacePath(), InSection(section),
			Doc("Namespace", "Where that zone lives. Omitted means this resource's own scope, then its parents.")),
		String(Key("host", "zone", "in", "name"), Path("host", Either("generated", "zone"), "in", "name"), IsVariableName(), Ref("zone", Inbound()), RequiredWhen(Key("host", "kind"), "zone"), InSection(section), ShowWhen(Key("host", "kind"), "zone"),
			Doc("Zone", "The zone this name lives in, as \"zone/<name>\". Only for a zone you own; a generated name has no zone to choose.")),
	}
}

// A database names its data model and, inside it, the engine:
//
//	sql:                                kv:
//	    postgres:                           distributed:
//	        host: db.example.com                size: 32MB
//	        credentials: { name: pg }
//
// Engine keys are declared per type, so a wrong pair is unrepresentable:
//
//	unknown key 'postgres'; valid keys here: distributed, mongo

// tauEngine is tau's own. On a database it is pkg/kvdb, a Merkle-CRDT replicated by the
// storage service; on a stream it is the swarm's own gossipsub. It is the one engine
// every kind with an engine axis has, and the only one that needs no external system.
const tauEngine = "distributed"

// externalEngineAttributes is the connection to an engine reached over the
// network: where, which secret, and wire protection. `at` is the authored path
// of the engine block, `named` the compiled-key prefix, `switchKey` the captured
// branch the requirement hangs on. A field only one caller needs is that
// caller's to append.
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

		// Absent means verify-full: weakening has to be written.
		String(key("tls"), path("tls"), InSet("verify-full", "require", "disable"), PerEnv(), InSection(section), shown,
			Doc("TLS", "How the connection is protected. Omitted means verify-full. verify-full: encrypted, with the server's certificate and hostname checked. require: encrypted, certificate unverified - a network attacker can still be the server. disable: plaintext, credentials included.")),
	}
}

// syncType is one coordination primitive. One kind, many types, as with
// database: the line is arbitration. A queue decides which consumer gets an
// item and a log the order of concurrent appends; stream and bus decide nothing
// and stay their own kinds.
type syncType struct {
	name string
	// ops is what may be done to this type. Ops() carries the union because an access
	// block loads before its target resolves; checkSyncOps holds each entry to the
	// type it reaches.
	ops Verbs
	// extra is what only this primitive has to say — a queue's delivery budget, a log's
	// retention. A field every type would carry belongs in syncType.attributes.
	extra func(syncType) []*Attribute
	doc   string
}

var syncTypes = []syncType{
	// Claim, visibility timeout, ack and dead letter, not pop-and-delete (pkg/queue).
	{
		name:  "queue",
		ops:   Verbs{DeliveredOp("receive"), WriteOp("send")},
		extra: queueAttributes,
		doc:   "Work handed from one workload to another. A claim is exclusive, an unacknowledged item comes back, and one that gives up lands in a dead letter.",
	},

	// `append` alone: an overwritable record is not an append-only log. `read` is a
	// pull from a cursor; a follow would be a delivered op like a queue's `receive`.
	{
		name:  "log",
		ops:   Verbs{WriteOp("append"), ReadOp("read")},
		extra: logAttributes,
		doc:   "Records appended to the end and read forward. Nothing rewrites what is already there.",
	},
}

// SyncTypes is every coordination primitive `sync` may be, in declaration order.
func SyncTypes() []string { return syncTypeNames() }

func syncTypeNames() []string {
	out := make([]string, 0, len(syncTypes))
	for _, t := range syncTypes {
		out = append(out, t.name)
	}
	return out
}

// SyncTypeOf is the type row one name selects, exported so the composer asks the
// compiler's question.
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
		// The type is the block that is written, so one cannot be of two types or none.
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
		// Concurrent writes, as on `database.consensus`: `raft` orders them through one
		// holder, `crdt` takes each on arrival and merges. A raft log has one total
		// order; a crdt log converges, so the ABI carries an opaque cursor, not an
		// offset. Required for the written type only. No `gossip`: a gossiped write can be
		// dropped.
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

		// How long an item waits before anyone may have it; also what a workflow's
		// `wait` step becomes.
		Duration(k("delay"), InSection("delivery"), shown,
			Doc("Delay", "How long an item waits before it becomes deliverable at all, as a human string (e.g. \"30s\"). Absent means immediately.")),
		Int(k("max_attempts"), Default(5), DefaultWhen("type", t.name), InSection("delivery"), shown,
			Doc("Max Attempts", "Deliveries before the item moves to the dead-letter queue.")),

		// How redelivery is spaced; a workflow step's `backoff` is copied here. Absent
		// means the visibility timeout alone governs.
		Duration(k("retry", "interval"), InSection("delivery"), shown,
			Doc("Retry Interval", "Delay before a failed delivery becomes deliverable again. Absent means the visibility timeout alone governs.")),
		Float(k("retry", "factor"), InSection("delivery"), shown,
			Doc("Retry Factor", "Multiplier applied to the interval per further attempt: the delay for attempt n is interval times factor^(n-1), capped by the ceiling. Absent or 1 means a fixed interval.")),
		Duration(k("retry", "ceiling"), InSection("delivery"), shown,
			Doc("Retry Ceiling", "The delay never grows past this. Absent means it is not capped.")),

		// A reference is a block. The Ref names the kind, not the type, so
		// checkDeadLetterIsAQueue holds the target to type queue.
		String(k("dead_letter", "namespace"), IsNamespacePath(), InSection("delivery"), shown,
			Doc("Dead Letter Namespace", "Where that queue lives. Omitted means this queue's own scope, then its parents.")),
		String(k("dead_letter", "name"), IsVariableName(), Ref("sync"), InSection("delivery"), shown,
			Doc("Dead Letter Queue", "Queue that receives items which exhausted max-attempts.")),

		// A queue never grows without bound. Unclaimed work is not forgotten, as a
		// log's oldest records are: past its age it moves to the dead letter, and a
		// full queue refuses a send, as a full bucket does. Defaulted, so a queue
		// nobody bounded is bounded anyway.
		Duration(k("retention", "age"), Accessor(title(t.name)+"RetentionAge"), Default("168h"), DefaultWhen("type", t.name), PerEnv(), InSection("retention"), shown,
			Doc("Retention Age", "How long an item may wait unclaimed before it moves to the dead-letter queue, as a human string (e.g. \"168h\").")),
		Bytes(k("retention", "size"), Accessor(title(t.name)+"RetentionSize"), Default("64MB"), DefaultWhen("type", t.name), PerEnv(), InSection("retention"), shown,
			Doc("Retention Size", "How much the queue's waiting and claimed items may hold, as a human string (e.g. \"64MB\"). A send past it is refused.")),
	}
}

// logAttributes is what is kept, both drop-oldest: a full log forgets the front
// where a full bucket refuses. Absent means forever.
func logAttributes(t syncType) []*Attribute {
	shown := ShowWhen("type", t.name)
	k := func(rest ...string) AKey { return Key(append([]string{t.name}, rest...)...) }

	return []*Attribute{
		Duration(k("retention", "age"), Accessor(title(t.name)+"RetentionAge"), PerEnv(), InSection("retention"), shown,
			Doc("Retention Age", "How long a record is kept, as a human string (e.g. \"168h\"). Absent means forever.")),
		Bytes(k("retention", "size"), Accessor(title(t.name)+"RetentionSize"), PerEnv(), InSection("retention"), shown,
			Doc("Retention Size", "How much the log keeps before the oldest records are dropped, as a human string (e.g. \"1GB\"). Absent means it is not capped.")),
	}
}

type databaseType struct {
	name string
	// external is the servers speaking this model over the network, closed since
	// each is a wire protocol and a driver; empty means only tau's engine.
	external []string
	// ops is what may be done to this type; Ops() carries the union and
	// checkDatabaseOps holds each entry to the type it reaches.
	ops Verbs
	// extra is what only this model has to say about itself — a vector's width and
	// metric. A field every type would carry belongs in databaseType.attributes.
	extra func(databaseType) []*Attribute
	doc   string
}

var databaseTypes = []databaseType{
	// A system of record, which `cache` is not: pkg/kvdb on disk, replicated before
	// acknowledgement, nothing expires, no `ttl`. Under `consensus: crdt` concurrent
	// writes to one key are last-writer-wins with no error, and no kind offers
	// compare-and-swap; design keys per AGENTS.md (one writer per key).
	{
		name: "kv",
		ops:  Verbs{ReadOp("get"), ReadOp("list"), ReadOp("watch"), WriteOp("put"), WriteOp("delete")},
		doc:  "Keys and values, kept until something deletes them.",
	},

	// Documents fetched by id and queried by field. tau's own engine is not shipped
	// yet; the type is declared since what an author writes will not change.
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

	// Verbs differ per type; checkDatabaseOps holds an entry to its target's type.
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

// DatabaseEngines is every engine each data model may name, read off the table so
// whatever registers adapters asks the compiler's question. tau's own engine is
// in every list and is the node's to register.
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

// databaseOps is every verb any type declares, deduplicated in table order. It
// panics when two types give one verb different effect classes, since the effect
// is what cross-cutting queries group by.
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
		// The type is the block that is written, so a database cannot be of two types or
		// none.
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

		// The size is tau's own engine's, under a literal `distributed` path:
		// `postgres: { size: 1GB }` is an unknown key, and a lowering needs fixed
		// segments to set it. Accessor because the four per-type paths share a leaf
		// (TestNoTwoAttributesCollapseToOneAccessor).
		Bytes(Key(t.name, "size"), Path(t.name, tauEngine, "size"), Accessor(title(t.name)+"Size"),
			RequiredWhen(Key(t.name, "engine"), tauEngine), PerEnv(), InSection("engine"), shown,
			Doc("Size", "Maximum on-disk size, as a human string (e.g. \"1GB\").")),

		// Concurrent writes to one key: `crdt` is pkg/kvdb, merging last-writer-wins;
		// `raft` is pkg/raft, one leader and one total order. Required with no default,
		// since either default is wrong for someone. Only under `distributed`: an
		// external server has its own consistency, so RequiredWhen on the engine branch
		// rather than Required(). No `gossip`: a dropped write in a system of record is a
		// lost fact.
		String(Key(t.name, "consensus"), Path(t.name, tauEngine, "consensus"), Accessor(title(t.name)+"Consensus"),
			InSet("crdt", "raft"), RequiredWhen(Key(t.name, "engine"), tauEngine),
			InSection("engine"), shown,
			Doc("Consensus", "What happens to concurrent writes to one key. crdt: every node takes a write the moment it arrives and the writes merge last-writer-wins, with nothing refused and nothing waited for. raft: one holder at a time, so a write costs a round trip and nothing is silently discarded.")),
	}

	if len(t.external) > 0 {
		// An external engine carries its own connection: host, credential, tls; the
		// database name is a database's alone.
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
		// Fixed at creation and required: changing the width is a new index and a
		// re-embed.
		Int(Key(t.name, "dimensions"), MinInt(1), RequiredWhen(Key(t.name, "engine"), t.engines()...),
			InSection("engine"), shown,
			Doc("Dimensions", "The width of every vector stored here, which the embedding model decides — 1536 for OpenAI's text-embedding-3-small, 1024 for Cohere v3. Fixed: changing it is a new index and a re-embed.")),

		// No Default(): a default fires whenever the value is absent, writing `metric`
		// onto every non-vector database. The metric must match how the model was
		// trained; a wrong one does not error, it ranks badly.
		String(Key(t.name, "metric"), InSet("cosine", "dot", "euclidean"), InSection("engine"), shown,
			Doc("Metric", "How closeness is measured. Omitted means cosine. Match the embedding model: cosine for normalized embeddings, dot where magnitude carries meaning, euclidean for raw distance.")),
	}
}

// A kind declares Ops() when something can reach it; a cron, a zone or a
// component is not a target.
//
// V2Resources is the closed v2 resource vocabulary: validation, completion,
// JSON Schema and decompilation need every shape at compile time. What stays open
// is a workload's `egress` plus a `secret`.
var V2Resources = []*Node{

	// A container workload listening on HTTP. No port: the platform assigns one,
	// passes it in the environment and tunnels it over p2p. No replica count: the
	// platform places. No ops: HTTP arrives only through a gateway route.
	DefineGroup("application",
		resource(
			commonAttributes(append(packageRef(), applicationAttributes()...)...),
			Nested(portsGroup(), variablesGroup()),
			GroupDoc("A long-running container that listens on HTTP. Mount a volume for anything it must keep."), Icon("box"), Category("compute"), Footprint(), Engines(ociPackage),
			secIdentity, secAccess, secConfig, secNetwork, secCode, secLimits,
			Resource("application", "Application", "Application", "application"),
		)),

	// Code plus limits. An authorizer is a function built against the authorizer
	// world, not a kind.
	DefineGroup("function",
		resource(
			runnableAttributes(),
			Nested(variablesGroup()),
			GroupDoc("A serverless function. It declares no trigger: what may reach it is an inbound edge."), Icon("bolt"), Category("compute"), Footprint(), Engines(functionEngines...),
			secIdentity, secAccess, secConfig, secNetwork, secCode, secLimits,
			Ops(WriteOp("invoke")), // invoke is write: a call may do anything the callee can
			Resource("function", "Function", "Function", "function"),
		)),

	// On demand, long-running once triggered, not listening on HTTP.
	DefineGroup("worker",
		resource(
			runnableAttributes(),
			Nested(variablesGroup()),
			GroupDoc("An on-demand workload that runs to completion. Not HTTP-listening."), Icon("gear"), Category("compute"), Footprint(), Completes(), Engines(workerEngines...),
			secIdentity, secAccess, secConfig, secNetwork, secCode, secLimits,
			Ops(WriteOp("invoke")),
			Resource("worker", "Worker", "Worker", "worker"),
		)),

	// Interactive container: exec, filesystem, a generated FQDN. idle_timeout and
	// max_lifetime differ: an idle session and a runaway one are different failures.
	DefineGroup("sandbox",
		resource(
			commonAttributes(append(packageRef(),
				Duration(Key("lifetime", "idle"), PerEnv(), InSection("limits"), Doc("Idle Timeout", "Stop the sandbox after this long with no activity.")),
				Duration(Key("lifetime", "max"), PerEnv(), InSection("limits"), Doc("Max Lifetime", "Hard ceiling on how long a sandbox may live.")),
				egressAttribute(),
				Bytes(Key("execution", "memory"), Required(), PerEnv(), InSection("limits"), Doc("Memory", "Maximum memory the sandbox may use.")),
				cpuLimit(),
			)...),
			Nested(variablesGroup()),
			GroupDoc("An interactive container with exec and filesystem access, for agent and build sessions."), Icon("terminal"), Category("compute"), Footprint(), Engines(ociPackage),
			secIdentity, secAccess, secConfig, secNetwork, secCode, secLimits,
			Ops(WriteOp("exec"), ReadOp("read"), WriteOp("write")),
			Resource("sandbox", "Sandbox", "Sandbox", "sandbox"),
		)),

	// One field and one outbound edge; nameable so an edge has two ends.
	DefineGroup("cron",
		resource(
			commonAttributes(
				String("schedule", Required(), InSection("identity"), Doc("Schedule", "Cron expression, e.g. \"0 3 * * *\".")),

				// A cron expression names a moment only with a zone. UTC by default, having no
				// daylight saving to run a job twice or never.
				String("timezone", Default("UTC"), InSection("identity"),
					Doc("Timezone", "IANA zone the schedule is written in, e.g. \"Europe/Paris\". UTC by default — the only zone where every day has 24 hours.")),

				// When the last run is still going: skip, queue or allow. `skip` by default as
				// the only one that cannot make things worse.
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

	DefineGroup("package",
		resource(
			commonAttributes(packageAttributes()...),
			Nested(variablesGroup(), buildBlock()),
			GroupDoc("What a workload runs: one artifact, where its code comes from and how it is built. Nothing else builds."), Icon("book"), Category("code"),
			secIdentity, secAccess, secConfig, secSource, secBuild,
			Resource("package", "Package", "Package", "package"),
		)),

	// A built static site at a name: written as one thing, compiled into the gateway that
	// serves its package's files. Nothing at run time is a website. `mode` exists because
	// SPA and 404 serve a fallback with opposite statuses: index.html with 200 for a
	// client-side router, the 404 page with 404 otherwise.
	DefineGroup("website",
		resource(
			commonAttributes(append(append(packageRef(), hostAttributes("naming")...),
				String(Key("certificate", "source"), InSet("generated", "provided"), Default("generated"), Field("CertSource"), InSection("naming"), ShowWhen(Key("host", "kind"), "zone"), Doc("Certificate", "generated: issued on demand for this name. provided: from the secret below. A host under the cloud's own zone needs neither — that zone has a wildcard.")),
				certificateSecret("naming")[0], certificateSecret("naming")[1],
				String(Key("path"), Default("/"), InSection("serving"),
					Doc("Path", "Where on that name the site is mounted. Anything but / is stripped before a file is looked up, so the site is built for / either way.")),
				String(Key("serve", "index"), Default("index.html"), InSection("serving"),
					Doc("Index Document", "What a request for a directory returns, e.g. \"/docs/\" -> /docs/index.html.")),
				String(Key("serve", "mode"), InSet(staticServing, spaServing), Default(staticServing), InSection("serving"),
					Doc("Mode", "static: an unmatched path returns the not-found document with 404. spa: it returns the index document with 200, because the page's own router renders the route.")),
				String(Key("serve", "not_found"), InSection("serving"), ShowWhen(Key("serve", "mode"), staticServing),
					Doc("Not Found Document", "Returned with 404 for a path the site does not have. Omitted means a bare 404.")),
			)...),
			GroupDoc("A static website: a package of type assets, served at a name. It compiles into the gateway that serves it."), Icon("globe"), Category("internet"),
			secIdentity, secAccess, secCode,
			Section("naming", "Naming", "The name the site answers on, and where its certificate comes from."),
			Section("serving", "Serving", "Where the site is mounted, and what it returns for a path it does not have."),
			Resource("website", "Website", "Website", "website"),

			// The gateway is the whole of it: the package's files, served at the name.
			Lowers(
				Into("gateway",
					Named(Attr("name")),
					Carries(Concat(Text("serves website "), Attr("name"))),
					Sets(Key("host", "kind"), Attr(Key("host", "kind"))),
					Sets(Key("host", "name"), Attr(Key("host", "name"))),
					Sets(Key("host", "zone", "in", "namespace"), Attr(Key("host", "zone", "in", "namespace"))),
					Sets(Key("host", "zone", "in", "name"), Attr(Key("host", "zone", "in", "name"))),
					Sets(Key("certificate", "source"), Attr(Key("certificate", "source"))),
					Sets(Key("certificate", "secret", "namespace"), Attr(Key("certificate", "secret", "namespace"))),
					Sets(Key("certificate", "secret", "name"), Attr(Key("certificate", "secret", "name"))),
					Sets(Key("routing"), Text("route")),
					Sets(Key("route", "path"), Attr("path")),
					Sets(Key("route", "match"), Text("prefix")),
					// A no-op at /, and what a site built for / needs anywhere else.
					Sets(Key("route", "strip_prefix"), Text("true")),
					Sets(Key("route", "target"), Text("handler")),
					Sets(Key("route", "handler", "kind"), Text("package")),
					Sets(Key("route", "handler", "namespace"), Attr(Key("package", "namespace"))),
					Sets(Key("route", "handler", "name"), Attr(Key("package", "name"))),
					Sets(Key("route", "handler", "index"), Attr(Key("serve", "index"))),
					Sets(Key("route", "handler", "mode"), Attr(Key("serve", "mode"))),
					Sets(Key("route", "handler", "not_found"), Attr(Key("serve", "not_found"))),
				),
			),
		)),

	// A cache: entries have a lifetime and losing one costs a recomputation; it is
	// not a system of record and has no conditional write. Its consistency is
	// `consensus`'s answer, not the kind's.
	DefineGroup("cache",
		resource(
			commonAttributes(append(instanceAttributes(),
				Bytes(Key("size"), Required(), PerEnv(), InSection("keys"), Doc("Size", "Maximum size, as a human string (e.g. \"32MB\").")),
				Duration(Key("ttl"), PerEnv(), InSection("keys"), Doc("Time-To-Live", "Entry lifetime. A floor, not a guarantee: peers may disagree about existence for up to the clock skew.")),

				// Where the data lives: a copy per peer, or one node others fetch from. A bool
				// because absent (node-local) is the cheap, obvious answer. Not access, and no
				// mechanism or replica count. Under `crdt` and `gossip` it is implied, so it only
				// carries information under `raft`; whether to keep it is an open call.
				Bool(Key("replicated"), InSection("keys"),
					Doc("Replicated", "Whether every peer keeps its own copy of this cache. Omitted means the data lives on one node and other peers reach it over the network. Only a question under raft consensus: crdt and gossip both put a copy on every peer.")),

				// Concurrent writes to one key:
				//
				//	raft    one holder at a time; a write costs a round trip, nothing is discarded
				//	crdt    taken on arrival, merged last-writer-wins; the loser is gone silently
				//	gossip  taken locally, spread best-effort; an entry can be dropped
				//
				// `gossip` is lossy, acceptable only where a miss is cheap, so database lacks it.
				// Required: two writes arrive whether or not anybody chose. At the root since a
				// cache has no external engine branch.
				String(Key("consensus"), InSet("raft", "crdt", "gossip"), Required(), InSection("keys"),
					Doc("Consensus", "What happens to concurrent writes to one key. raft: one holder at a time, so a write costs a round trip and nothing is silently discarded. crdt: every node takes the write the moment it arrives and the writes merge last-writer-wins, with nothing refused and nothing waited for. gossip: the write is taken locally and spread best-effort, so an entry may fail to arrive or be dropped — cheapest, and fine where losing one costs a recomputation.")),

				// A REFERENCE, never the material. "key material or reference" let the
				// same field be either, and the safe reading was the one nothing enforced.
				String(Key("encryption", "key", "namespace"), IsNamespacePath(), InSection("encryption"),
					Doc("Namespace", "Where that secret lives. Omitted means this resource's own scope, then its parents.")),
				String(Key("encryption", "key", "name"), IsVariableName(), Ref("secret", Inbound()), InSection("encryption"),
					Doc("Encryption Key", "Secret holding the key entries are encrypted with, as \"secret/<name>\".")),
			)...),
			GroupDoc("A key-value cache served to the project's peers, with a lifetime on every entry. Not a system of record: losing an entry costs a recomputation, never a fact."), Icon("bolt"), Category("data"), Footprint(),
			secIdentity, secAccess,
			Section("keys", "Keys", "Capacity, lifetime, where the data lives, and what happens to two writes at one key."),
			secInstances,
			Section("encryption", "Encryption", "Encryption at rest."),
			Ops(ReadOp("get"), ReadOp("list"), ReadOp("watch"), WriteOp("put"), WriteOp("delete")),
			Resource("cache", "Cache", "Cache", "cache"),
		)),

	// The only system of record here, with four data models (see databaseTypes).
	// `cache` stays apart on durability, not shape.
	DefineGroup("database",
		resource(
			commonAttributes(append(databaseAttributes(), instanceAttributes()...)...),
			GroupDoc("A database: keys and values, documents, tables or vectors — on tau's own replicated engine, or on an external server reached over the network."), Icon("database"), Category("data"), Footprint(),
			secIdentity, secAccess,
			Section("engine", "Engine", "Which data model it is, which engine backs it, and its settings."),
			secInstances,
			// The union: the loader cannot yet know the target; checkDatabaseOps holds each
			// entry to its type.
			Ops(databaseOps()...),
			Resource("database", "Database", "Database", "database"),
		)),

	DefineGroup("storage",
		resource(
			commonAttributes(append(append([]*Attribute{
				String("type", Path(Either(storageTypes...)), Captures(), InSection("storage"), Doc("Type", "Storage kind: object or streaming (the key selects the type block).")),
			}, instanceAttributes()...),
				Bool(Key("object", "versioning"), InSection("storage"), Doc("Versioning", "Keep historical versions of objects (object storage).")),
				Duration(Key("streaming", "ttl"), Field("Ttl"), Accessor("TTL"), PerEnv(), InSection("storage"), Doc("Time-To-Live", "Lifetime for streamed entries. A floor, not a guarantee.")),
				Bytes(Key("size"), Path(Either(storageTypes...), "size"), Required(), PerEnv(), InSection("storage"), Doc("Size", "Maximum storage size, as a human string (e.g. \"1GB\").")),
			)...),
			GroupDoc("Object or streaming storage, content-addressed, served to the project's peers."), Icon("bucket"), Category("data"), Footprint(),
			secIdentity, secAccess,
			Section("storage", "Storage", "Storage kind and capacity."),
			secInstances,
			Ops(ReadOp("get"), ReadOp("list"), ReadOp("head"), WriteOp("put"), WriteOp("delete")),
			Resource("storage", "Storage", "Storage", "storage"),
		)),

	// A quota'd local directory continuously checkpointed into the CAS as a snapshot
	// chain, which is the backup. It may be shared: POSIX locks arbitrate, so there
	// is no lease. mount-ro and mount-rw stay two verbs. RPO is the age of the last
	// committed snapshot, unbounded when checkpoints lag churn, hence the budget.
	DefineGroup("volume",
		resource(
			commonAttributes(
				Bytes(Key("quota"), Required(), PerEnv(), InSection("storage"), Doc("Size", "Hard quota for the directory, as a human string (e.g. \"10GB\").")),
				Duration(Key("checkpoint", "interval"), Default("60s"), PerEnv(), InSection("checkpoint"), Doc("Checkpoint Interval", "How often the volume is walked, chunked and uploaded.")),
				// A restore is crash-consistent, not quiesced: a checkpoint walks the directory
				// while writers write, so it is not a consistent point across several writers.
				Duration(Key("checkpoint", "rpo_budget"), PerEnv(), InSection("checkpoint"), Doc("RPO Budget", "Ceiling on the age of the last committed snapshot before on-budget-exceeded fires. What a snapshot holds is a crash-consistent moment — writers are not stopped for the walk — so a restore gives back what a power cut would.")),
				String(Key("checkpoint", "on_budget_exceeded"), InSet("alert", "freeze-writes"), Default("alert"), InSection("checkpoint"), Doc("On Budget Exceeded", "What to do when the committed-snapshot age passes rpo-budget. Nothing happens without a budget to pass.")),
			),
			GroupDoc("A quota'd directory with full POSIX semantics, checkpointed into the CAS. More than one workload may mount it read-write; the filesystem arbitrates. Bind mounts are refused."), Icon("drive"), Category("data"), Footprint(),
			secIdentity, secAccess,
			Section("storage", "Storage", "Capacity."),
			Section("checkpoint", "Checkpoint", "Durability out of band from the write path. A snapshot is crash-consistent, not quiesced."),
			Ops(Mounts(ReadOp("mount-ro")), Mounts(WriteOp("mount-rw"))),
			Resource("volume", "Volume", "Volume", "volume"),
		)),

	// A declaration and a reference, never a value: the value lives in the secrets
	// service, keyed <project>/<environment>/<key>, so rotation needs no push.
	DefineGroup("secret",
		resource(
			commonAttributes(
				String("source", InSet("value", "oidc"), Default("value"), InSection("identity"), Doc("Source", "value: stored in the secrets service. oidc: minted per request as a short-lived token, nothing to rotate or leak.")),
				Duration("rotation", InSection("identity"), Doc("Rotation", "How often the value is expected to rotate. Advisory; rotation itself never requires a new release.")),
			),
			GroupDoc("A named reference to a secret value held outside git. The config never carries the value."), Icon("key"), Category("data"),
			secIdentity, secAccess,
			// No write verb from a workload, ever; the value flows out to whoever named it.
			Ops(Arrives(ReadOp("get"))),
			Resource("secret", "Secret", "Secret", "secret"),
		)),

	// Coordination; the type says which shape: a queue hands work over exactly once,
	// a log fixes order. A lock, counter or barrier would be another row in syncTypes.
	DefineGroup("sync",
		resource(
			commonAttributes(syncAttributes()...),
			GroupDoc("Coordination between workloads: a queue that hands work over exactly once, or an append-only log that fixes what order things happened in. What these have in common is that the resource decides who wins — which is what a stream and a bus deliberately do not."), Icon("list"), Category("data"), Footprint(),
			secIdentity, secAccess,
			Section("sync", "Sync", "Which primitive it is, and what happens to concurrent writes."),
			Section("delivery", "Delivery", "Claim, retry and dead-letter behaviour."),
			Section("retention", "Retention", "What is kept, and for how long."),
			// THE UNION, and it is not a hole in the per-kind verb rule — see syncOps.
			Ops(syncOps()...),
			Resource("sync", "Sync", "Sync", "sync"),
		)),

	// A DNS zone this cloud is authoritative for, owned and delegated by the project
	// (the cloud's own zone is never declared; see hostAttributes). Apart from
	// gateways: it answers what a name resolves to at all. `records` are zone-file
	// presentation lines, the format providers import and export, at the cost of
	// per-field validation. A gateway's fqdn needs no record here.
	DefineGroup("zone",
		resource(
			commonAttributes(
				String("fqdn", IsFqdn(), Required(), Field("Fqdn"), Accessor("FQDN"), EmitValidation("zone", "dns"), InSection("identity"), Doc("Zone", "The zone apex this cloud answers for, e.g. \"example.com\". Delegate it with NS records at your registrar. The cloud's OWN zone is not declared here — it is generated, and a resource reaches it with host.generated.")),
				// A file, a resolver, or both, not a switch: the file is what is known at deploy
				// (including NS delegations), the resolver answers the rest. Neither is required.
				// The lines here are what the node serves until a file can reach the compiled
				// object.
				StringSlice("records", InSection("records"),
					Doc("Records", "Zone-file lines, e.g. \"www 300 IN A 1.2.3.4\", \"@ 3600 IN MX 10 mail.example.com.\".")),

				String("file", IsRepoPath(), Inlined("records"), InSection("records"),
					Doc("Zone File", "Path to a zone file in this repository, e.g. \"dns/example.com.zone\". Served with this zone's fqdn as $ORIGIN. Use NS records in it to delegate a subtree elsewhere.")),

				// Asked only for what the file does not answer: the more specific statement wins.
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

	// A gateway is a name, the policy at that name, and one route; many routes on one
	// name is a gap, and mounting gateways beneath one another does not dedupe the
	// policy. The handler switches on the target kind:
	//
	//	handler:
	//	    function:                 # or application / package / gateway / mcp
	//	        namespace: /shop/eu   # omitted means the project root
	//	        name: checkout
	DefineGroup("gateway",
		resource(
			// A gateway with no host block is reachable only mounted beneath another.
			append(append(append(append(append(append(identityAttributes(), hostAttributes("naming")...),
				routerAttributes("route", "gateway-router", "router")...),
				routerAttributes("handler", "gateway-router", "route", "router")...),
				// Either a router or routes, and inside a route a handler or its own router;
				// the captured switch enforces exactly one.
				String(Key("routing"), Path(Either("route", "router")), Captures(), Required(), InSection("route"),
					Doc("Routing", "route: this gateway serves one path, declared below. router: a function decides where everything goes, and there are no static routes.")),
				// Not Required: a router may route everything.
				String(Key("route", "target"), Path("route", Either("handler", "router")), Captures(), InSection("handler"),
					Doc("Target", "handler: where the request ends. router: a function that says where it should go.")),
				String(Key("certificate", "source"), InSet("generated", "provided"), Default("generated"), Field("CertSource"), InSection("naming"), ShowWhen(Key("host", "kind"), "zone"), Doc("Certificate", "generated: issued on demand for this name. provided: from the secret below. A host under the cloud's own zone needs neither — that zone has a wildcard.")),
				certificateSecret("naming")[0], certificateSecret("naming")[1],

				// Nesting is declared by Path, never spelled into the key, so each level merges
				// (engine/keys.go).
				String(Key("route", "path"), RequiredWhen(Key("routing"), "route"), InSection("route"), Doc("Path", "The path served, e.g. \"/checkout\" or \"/\". Required when this gateway declares a route; a gateway with a router has no paths of its own.")),
				String(Key("route", "match"), InSet("prefix", "exact", "regex"), Default("prefix"), InSection("route"), Doc("Match", "How the path is compared: prefix, exact, or regex.")),
				// A site built for / mounted at /docs 404s on every page unless the prefix is
				// stripped; an application usually wants it kept. False by default; the audit
				// reports a package mounted at a prefix without it.
				Bool(Key("route", "strip_prefix"), InSection("route"),
					Doc("Strip Prefix", "Remove the matched path prefix before handing the request on. A package's files mounted anywhere but / usually want this.")),
				StringSlice(Key("route", "methods"), EachInSet(HttpMethods...), InSection("route"), Doc("Methods", "HTTP methods answered. Omit for any. GET/HEAD/OPTIONS read, the rest write.")),

				// One handler and no weight: traffic splitting is not expressible here.
				String(Key("route", "handler", "kind"), Path("route", "handler", Either(handlerKinds...)), Captures(), Field("HandlerKind"), InSection("handler"), Doc("Handler", "Which kind serves this path (the key selects the handler block).")),
				String(Key("route", "handler", "namespace"), Path("route", "handler", Either(handlerKinds...), "namespace"), InSection("handler"), Doc("Namespace", "Path to the namespace holding the handler, e.g. \"/shop/eu\". Omitted means the project root.")),
				// Required once there is a handler, so a route cannot name a kind and no
				// resource; not outright, since a router has no handler block.
				String(Key("route", "handler", "name"), Path("route", "handler", Either(handlerKinds...), "name"), RequiredWhen(Key("route", "target"), "handler"), IsVariableName(), Ref("", KindFrom(Key("route", "handler", "kind")), ByName(), Serves()), InSection("handler"), Doc("Name", "The handler resource's name within that namespace.")),
				String(Key("route", "handler", "port"), Path("route", "handler", Either(handlerKinds...), "port"), IsVariableName(), InSection("handler"), Doc("Port", "Which of the handler's ports to reach, by name. A workload listening on more than one has to be told which, and the number is never written here — the platform assigns the external one, and there may be several instances of the workload on one host.")),

				// A package handler is its built files, served by the gateway itself: only an
				// assets package has files to serve (checkServedPackages).
				String(Key("route", "handler", "index"), Path("route", "handler", "package", "index"), InSection("handler"), ShowWhen(Key("route", "handler", "kind"), "package"),
					Doc("Index Document", "What a request for a directory returns. Omitted means index.html.")),
				String(Key("route", "handler", "mode"), Path("route", "handler", "package", "mode"), InSet(staticServing, spaServing), InSection("handler"), ShowWhen(Key("route", "handler", "kind"), "package"),
					Doc("Mode", "static: an unmatched path returns the not-found document with 404. spa: it returns the index document with 200. Omitted means static.")),
				String(Key("route", "handler", "not_found"), Path("route", "handler", "package", "not_found"), InSection("handler"), ShowWhen(Key("route", "handler", "kind"), "package"),
					Doc("Not Found Document", "Returned with 404 for a path the package does not have, in static mode. Omitted means a bare 404.")),

				// The gateway's own name, unknowable when a generated host is written, so cors
				// can allow it.
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

	// The front door for what is not HTTP: a raw TCP connection accepted at the edge
	// and streamed over p2p to a workload. No port and no address are authored: at
	// L4 the port is the address, and an authored one would collide across projects.
	// The proxy gets its own address, and the platform publishes both records:
	//
	//	db.shop.example.com.              30 IN AAAA 2001:db8::7
	//	_postgres._tcp.db.shop.example.com. 30 IN SRV 0 0 5432 db.shop.example.com.
	//
	// Few clients do SRV lookups, so a dedicated address frees the protocol's
	// conventional port (`service`) and `psql -h db.shop.example.com` just works.
	DefineGroup("proxy",
		resource(
			append(append(append(identityAttributes(), hostAttributes("listener")...),
				routerAttributes("forward", "proxy-router", "router")...),
				String(Key("routing"), Path(Either("forward", "router")), Captures(), Required(), InSection("forward"),
					Doc("Routing", "forward: every connection goes to the workload named below. router: a function picks one per connection.")),
				// No `protocol` field: a tunnel is a reliable ordered stream, so only TCP is
				// servable, and the transport is the kind's.
				String("service", IsVariableName(), InSection("listener"),
					Doc("Service", "The IANA service this speaks, e.g. \"postgres\". Publishes _<service>._tcp beside this name in DNS, and is how a client that cannot look up SRV knows which conventional port to dial. Defaults to the resource's name.")),

				// Only `terminate`: one proxy owns its address, so nothing is multiplexed and
				// there is nothing to pass through to.
				String(Key("tls", "mode"), InSet("none", "terminate"), Default("none"), Field("TLSMode"), InSection("listener"),
					Doc("TLS", "none: an opaque pipe, whatever the protocol does about TLS is its own business. terminate: the proxy answers TLS and hands plaintext to the workload.")),
				// Defaulted, not RequiredWhen: a generated host needs no certificate, the cloud's
				// wildcard covers it, as on a gateway.
				String(Key("certificate", "source"), InSet("generated", "provided"), Default("generated"), Field("CertSource"), InSection("listener"), ShowWhen(Key("host", "kind"), "zone"),
					Doc("Certificate", "generated: issued on demand for this name. provided: from the secret below. A host under the cloud's own zone needs neither — that zone has a wildcard.")),
				certificateSecret("listener")[0], certificateSecret("listener")[1],

				// `forward`, not `handler`: a proxy handles nothing, it forwards bytes.
				String(Key("forward", "kind"), Path("forward", Either(proxyHandlerKinds...)), Captures(), Field("HandlerKind"), InSection("forward"),
					Doc("Forward To", "Which kind the connection is forwarded to (the key selects the block).")),
				String(Key("forward", "namespace"), Path("forward", Either(proxyHandlerKinds...), "namespace"), IsNamespacePath(), InSection("forward"),
					Doc("Namespace", "Path to the namespace holding the workload, e.g. \"/shop/eu\". Omitted means the project root.")),
				// Required once the proxy forwards; a router-only proxy has no forward block.
				String(Key("forward", "name"), Path("forward", Either(proxyHandlerKinds...), "name"), RequiredWhen(Key("routing"), "forward"), IsVariableName(), Ref("", KindFrom(Key("forward", "kind")), ByName(), Serves()), InSection("forward"),
					Doc("Name", "The workload's name within that namespace.")),

				// The workload's own port, by name, the one it binds; the port clients dial is
				// allocated and published in DNS.
				String(Key("forward", "port"), Path("forward", Either(proxyHandlerKinds...), "port"), IsVariableName(), InSection("forward"),
					Doc("Port", "Which of the workload's own named ports to stream to. Not the port clients dial — that one is assigned and published in DNS.")),

				// A raw stream has no requests, so the limits are a connection's:
				//
				//	rate         how fast new connections may arrive
				//	concurrent   how many may be open at once
				//
				// Neither defaults: a ceiling nobody chose cannot be defended.
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

	// Fan-out to every subscriber; subscribe and publish are two capabilities, never
	// collapsed into `write`. Alongside: `queue` fans in to one claimant, `bus` fans
	// out and forgets, `channel` is two workloads and a socket.
	//
	// The engine is a key from a closed set, and the topic lives where it says:
	//
	//	distributed: {}                  kafka:
	//	                                     host: broker:9092
	//	                                     credentials: { name: k }
	//
	// `kafka` and `nats` (JetStream) are logs with ordering, consumer groups and
	// replay, and require host and credential. `distributed` is the swarm's gossipsub
	// with none of that; the key makes moving a topic onto a log an engine block, not
	// a rewrite. A stream is its name: one resource, one topic, no pattern.
	DefineGroup("stream",
		resource(
			commonAttributes(append([]*Attribute{
				String("engine", Path(Either(streamEngines...)), Captures(), InSection("engine"),
					Doc("Engine", "Which engine holds the topic: distributed is the swarm's own gossipsub; kafka and nats are external clusters reached over the network.")),
			}, externalEngineAttributes(nil, nil, externalStreamEngines, Key("engine"), "engine", ShowWhen("engine", externalStreamEngines...))...)...),
			GroupDoc("A pubsub stream: what is published to it reaches every subscriber. On the swarm's own gossipsub, or on an external Kafka or NATS cluster."), Icon("message"), Category("trigger"),
			secIdentity, secAccess,
			Section("engine", "Engine", "Which engine holds the topic, and its settings."),
			Ops(DeliveredOp("subscribe"), WriteOp("publish")), // never collapse these into "write"
			Resource("stream", "Stream", "Stream", "stream"),
		)),

	// An n:n bus that can never rewind. On tau's engine it delivers like a
	// `distributed` stream; the difference is that a stream names an engine and can
	// move onto a log, a bus cannot. No engine axis, since a bus has nothing to
	// choose. Writing `host:` gives it a websocket at that name; without one it is
	// reachable only inside the project. A browser names the instance, so
	// `instances:` is where the project can say no. Its verbs are `stream`'s.
	DefineGroup("bus",
		resource(
			commonAttributes(instanceAttributes()...),
			GroupDoc("An ephemeral n:n message bus: everything attached gets what is sent, and nothing is kept. Point a gateway route at it and a browser can attach over websocket."), Icon("cable"), Category("trigger"),
			secIdentity, secAccess,
			secInstances,
			Ops(DeliveredOp("subscribe"), WriteOp("publish")), // never collapse these into "write"
			Resource("bus", "Bus", "Bus", "bus"),
		)),

	// No `auth` field until an issuer can be named and MCP can route on the
	// `Mcp-Method`/`Mcp-Name` headers. Its shape then: an `auth` block whose oauth
	// branch requires `issuer`, `scopes` optional; the audience is derived from the
	// serving route and jwks discovered from the issuer.
	//
	// Stateless Streamable HTTP, POST only, no sessions; the tools are functions.
	// Routing predicates on the Mcp-Method/Mcp-Name headers, never the JSON-RPC body,
	// and the protocol-version guard must come before the router or header routing
	// trusts attacker-controlled headers.
	DefineGroup("mcp",
		resource(
			commonAttributes(
				String(Key("tools", "cache_scope"), InSet("none", "public", "principal"), Default("none"), InSection("tools"), Doc("Cache Scope", "How widely tools/list may be cached. public is cacheable across the whole swarm.")),
				Duration(Key("tools", "cache_ttl"), PerEnv(), InSection("tools"), Doc("Cache TTL", "How long a cached tools/list stays valid.")),
			),
			GroupDoc("An MCP server grouping functions as tools. Stateless Streamable HTTP; subscriptions are declined in v1."), Icon("plug"), Category("internet"),
			secIdentity, secAccess,
			Section("tools", "Tools", "tools/list caching."),

			// The protocol's own methods: `tools/list` reads the catalogue, `tools/call` may
			// do anything the tool can. No resources/read or prompts/get: nothing here
			// publishes either.
			Ops(ReadOp("list"), WriteOp("call")),
			Resource("mcp", "MCP", "MCP", "mcp"),
		)),

	// A named, ordered, bidirectional byte conduit between exactly two workloads in
	// the project, over libp2p with no edge in the path: the one inbound path that is
	// not an ingress. The platform prefixes the wire protocol with the project's
	// identity, so two projects' `sync` channels never meet and no author can collide
	// on purpose. What serves it is named, so it never dials into a void.
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

	// Where inference happens and on whose account: endpoint, credential and
	// parameters belong to the model, not each caller. `remote` is a provider's,
	// `local` a workload in this project serving one. Format is the wire shape, not
	// the provider.
	DefineGroup("model",
		resource(
			commonAttributes(
				String("identifier", Required(), PerEnv(), InSection("model"),
					Doc("Identifier", "What this model is called where it is served, e.g. \"claude-opus-5\" or \"llama-3.1-70b\". The resource's own name is yours; this one is theirs.")),
				String("format", InSet("openai", "anthropic", "ollama"), Required(), InSection("model"),
					Doc("API Format", "The wire format the endpoint speaks. Not the provider: plenty of things that are not OpenAI serve the OpenAI format, and the caller needs the shape of the request.")),

				// `inference`, not `serve`, which a stream uses for which kind answers; the port
				// check once read this `local` as a kind.
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

				// Typed, not a map: the four every format has, with ranges worth checking.
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

	// An LLM loop with tools, declared rather than written. Its tools are its access
	// block, so the target's authorizer sees every grant and blast radius is a graph
	// query. `steps` is required: a tool loop has no natural end.
	DefineGroup("agent",
		resource(
			commonAttributes(append([]*Attribute{
				// One model, by reference. `model` is both a kind and this attribute's name;
				// they coexist because a key an attribute declares is not an entry of the
				// same-named group (engine/node.go, claimedUnder).
				String(Key("model", "namespace"), IsNamespacePath(), PerEnv(), InSection("model"),
					Doc("Model Namespace", "Where that model lives. Omitted means this agent's own scope, then its parents.")),
				// The one PerEnv reference: a cheaper model in preview. Every other reference is
				// environment-invariant: what a resource is set to may vary, what it reaches may
				// not.
				String(Key("model", "name"), IsVariableName(), Ref("model"), Required(), PerEnv(), InSection("model"),
					Doc("Model", "The model this agent answers with.")),

				// A filename, not the prose, relative to the agent's `source`; with no source the
				// base is `LayoutOf().Content`, `agent/<name>/`.
				String("instructions", IsRepoPath(), InSection("model"),
					Doc("Instructions", "The file holding the system prompt, e.g. \"instructions.md\". Relative to this agent's source, which is this repository at the agent's own location unless it says otherwise.")),

				// Memory is a lowered vector index sized by the embedding model, which is a
				// different model from the one that answers, hence its own reference.
				String(Key("memory", "embedder", "namespace"), IsNamespacePath(), InSection("memory"),
					Doc("Namespace", "Where that model lives. Omitted means this resource's own scope, then its parents.")),
				String(Key("memory", "embedder", "name"), IsVariableName(), Ref("model"), InSection("memory"),
					Doc("Embedding Model", "The model that turns what this agent sees into vectors, as \"model/<name>\". Naming it is what turns memory on.")),
				Int(Key("memory", "dimensions"), MinInt(1), RequiredWhen(Key("memory", "embedder", "name")), InSection("memory"),
					Doc("Dimensions", "The width the embedding model produces — 1536 for text-embedding-3-small. Fixed once there is anything in memory.")),
				// No default, required only with an embedder: a default would claim memory an
				// agent without one does not have.
				Bytes(Key("memory", "size"), RequiredWhen(Key("memory", "embedder", "name")), PerEnv(), InSection("memory"),
					Doc("Memory Size", "How much the agent may remember, as a human string.")),

				// Bounds decisions, not spend: every turn re-sends the conversation, so cost is
				// superlinear in steps. A spend bound would be `limits.tokens`.
				Int(Key("limits", "steps"), MinInt(1), Required(), PerEnv(), InSection("limits"),
					Doc("Max Steps", "How many times this agent may call a tool and decide again in one invocation. Required: an agent with no ceiling is a loop that runs until something else stops it.")),
				Int(Key("limits", "concurrent"), MinInt(1), PerEnv(), InSection("limits"),
					Doc("Concurrent", "How many invocations may run at once.")),

				// Wall clock for one invocation; defaulted because the produced loop must have
				// one.
				Duration(Key("limits", "timeout"), Default("5m"), PerEnv(), InSection("limits"),
					Doc("Timeout", "Ceiling on one whole invocation, as a human string (e.g. \"5m\"). Counts the model calls, the tools and the waiting between them.")),
				Bytes(Key("limits", "memory"), Default("128MB"), PerEnv(), InSection("limits"),
					Doc("Memory", "How much memory the loop may use, as a human string (e.g. \"128MB\"). An agent handing large tool results between the model and its tools needs more than one juggling short answers.")),
				egressAttribute(),
				// Prose lives where the resource does; an agent is not built, so no
				// `component` or `builtin`.
			}, contentSource("source", "git")...)...),
			// Nothing builds an agent, so its variables have only the run lifetime.
			Nested(variablesGroup()),
			GroupDoc("An LLM loop with tools. What it may call is what it declares access to — there is no second list."), Icon("agent"), Category("compute"), Footprint(),
			secIdentity, secAccess, secConfig, secNetwork, secLimits, secSource,
			Section("model", "Model", "Which model answers, and what it is told."),
			Section("memory", "Memory", "What it remembers, and how wide the vectors are."),
			Ops(WriteOp("invoke")),

			// An agent lowers: the loop is a function running the platform's `agent`
			// package, and memory a vector index. An agent whose loop is the author's code is
			// a `function`.
			Lowers(
				Into("package",
					Named(Concat(Attr("name"), Text("_loop"))),
					Carries(Text("the platform's agent loop")),
					Sets("type", Text(wasmPackage)),
					Sets(Key("source", "builtin"), Text("agent")),
				),
				Into("function",
					// The loop is what CALLS the tools this agent declares, so opened,
					// the box reads model-and-memory-and-tools around it.
					Acts(),
					Named(Concat(Attr("name"), Text("_infer"))),
					Carries(Text("the loop: call the model, run a tool, decide again")),
					Sets(Key("package", "name"), Concat(Attr("name"), Text("_loop"))),
					Sets(Key("execution", "call"), Text("infer")),
					Sets(Key("execution", "timeout"), Attr(Key("limits", "timeout"))),
					Sets(Key("execution", "memory"), Attr(Key("limits", "memory"))),
					Sets("implements", Text("handler")),
				),

				// Only with an embedder. Writing under `vector/` selects the type, and both set
				// fields have literal paths because authoredPath cannot locate a path with a
				// matcher.
				Into("database",
					When(Key("memory", "embedder", "name")),
					Named(Concat(Attr("name"), Text("_memory"))),
					Carries(Concat(Text("what "), Attr("name"), Text(" remembers"))),
					// One term, one field: the value is CARRIED, so the index's size is
					// the agent's own byte count and comes back out again as `2GB`.
					Sets(Key("vector", "dimensions"), Attr(Key("memory", "dimensions"))),
					Sets(Key("vector", "size"), Attr(Key("memory", "size"))),
					// The produced kind requires consensus. `crdt`: a memory index is re-embeddable,
					// so a discarded write costs a re-embed, and a per-turn writer should not pay a
					// round trip.
					Sets(Key("vector", "consensus"), Text("crdt")),
					// The loop is what searches it, and that is an entry on the loop:
					// `database: { <name>_memory: { ops: [search] } }`, exactly what an
					// author would have written.
					Reaches("search", From("function", Concat(Attr("name"), Text("_infer")))),
				),
			),
			Resource("agent", "Agent", "Agent", "agent"),
		)),

	// A linear state machine over existing things. It lowers: each step becomes a
	// queue and a driver consuming it, failures land in a dead-letter queue, and the
	// run's state is the message between them (interp/lowering.go). No Choice,
	// Parallel or Map yet: each needs an expression or a list inside a list element.
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
			GroupDoc("An ordered pipeline of functions with retries. Each step runs as its own queue; nothing new has to be deployed to serve it. There is no run state to inspect yet."), Icon("workflow"), Category("trigger"), Footprint(),
			secIdentity, secAccess,
			Section("run", "Run", "Ceilings on one run and on all of them."),
			Section("steps", "Steps", "What runs, in order."),
			Ops(WriteOp("start")),

			// What a workflow becomes; tcc expands it (interp/lowering.go).
			Lowers(
				// ONE QUEUE PER STEP: a queue is where a retry budget lives, and the
				// budget is the step's — `reserve` may be worth four attempts and
				// `charge` exactly one. Sharing a queue would make the whole pipeline
				// share whichever number was written last.
				Into("sync",
					PerEach("steps"),
					// The name states its parts, so the step's name can be read back.
					Named(Concat(Attr("name"), Text("_"), Each("name"))),
					Carries(Concat(Text("work waiting for step "), Each("name"))),
					// Every key under `queue/`: writing the consensus is what selects the type.
					Sets(Key("queue", "consensus"), Text("raft")),
					Sets(Key("queue", "max_attempts"), Each("attempts")),
					// A waiting step is a queue holding its work back; tcc parses the duration.
					Sets(Key("queue", "delay"), Each(Key("wait"))),
					Sets(Key("queue", "retry", "interval"), Each(Key("backoff", "interval"))),
					Sets(Key("queue", "retry", "factor"), Each(Key("backoff", "factor"))),
					Sets(Key("queue", "retry", "ceiling"), Each(Key("backoff", "ceiling"))),

					// Exhaustion is routed by the queue's dead-lettering, the one moment no step code
					// runs: to the named step's queue, else the terminal one.
					Sets(Key("queue", "dead_letter", "name"), Concat(Attr("name"), Text("_"), Each("on_error"))),

					// CONSUMED BY THE STEP'S DRIVER, never by the author's function. The
					// function is invoked BY the driver and knows nothing about queues:
					// that is the whole of what this composition changed.
					Reaches("receive", From("function", Concat(Attr("name"), Text("_"), Each("name")))),
				),

				// The drivers' code is the platform's, one package for every step.
				Into("package",
					Named(Concat(Attr("name"), Text("_driver"))),
					Carries(Text("the platform's step driver")),
					Sets("type", Text(wasmPackage)),
					Sets(Key("source", "builtin"), Text("step")),
				),

				// One driver per step, platform code: receive one item, invoke the step's
				// function, send the result on; a failure redelivers and exhaustion dead-letters.
				// A function, since a queue delivery is an inbound edge, and it shares the step's
				// name since the kind is part of identity.
				Into("function",
					PerEach("steps"),
					Named(Concat(Attr("name"), Text("_"), Each("name"))),
					Carries(Concat(Text("step "), Each("name"), Text(": receive, run, hand on"))),
					Sets(Key("package", "name"), Concat(Attr("name"), Text("_driver"))),
					Sets(Key("execution", "call"), Text("step")),
					// Bounds one delivery, not the run; the invoke's own limit is the callee's.
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

				// The terminal queue has no consumer: a run stops here for a person to look at.
				// The start step's queue is recorded on the shell so no consumer derives it from
				// a naming convention.
				Into("sync",
					Named(Concat(Attr("name"), Text("_failed"))),
					Carries(Text("runs that gave up, kept for a person to look at - nothing consumes this")),
					Sets(Key("queue", "consensus"), Text("raft")),
					Writes(Text("sync"), On("workflow", Attr("name")), At(Text("entry"), Text("start"), Text("kind"))),
					Writes(Concat(Attr("name"), Text("_"), Attr("start")), On("workflow", Attr("name")), At(Text("entry"), Text("start"), Text("name"))),
					Writes(Text("send"), On("workflow", Attr("name")), At(Text("entry"), Text("start"), Text("as"))),
				),

				// No events stream and no state store: the run's state is the message, and which
				// queue holds it is which step it is at.
			),
			Resource("workflow", "Workflow", "Workflow", "workflow"),
		)),
}

// rootAttributes is the project root.
func rootAttributes() []*Attribute {
	return []*Attribute{
		String("id", IsCID(), Required(), Generated(GenCID), EmitValidation("project_id", "project_id"), Doc("ID", "Content-addressed identifier (CID) of the project.")),
		String("name", IsVariableName(), Doc("Name", "Project name. Must be a valid variable name.")),

		// Declared so a consumer discovers the derivation rather than hard-coding it.
		// Never authored and never in the compiled object; the validator refuses a
		// document that sets it, since a stale copy is the drift deriving avoids.
		String("zone_label", Derived(DeriveDNSLabel, "id"),
			Validator(func(v string) error {
				if v == "" {
					return nil
				}
				return errors.New("zone-label is derived from the project id, not authored; remove it")
			}),
			Doc("Zone Label", "The project's label in a cloud's generated zone, derived from its id. Read-only.")),
		String("description", Doc("Description", "Free-form, human-readable description of the project.")),
		StringSlice("tags", WireDrop(), Doc("Tags", "Project-level labels.")),

		// The allowance, a scope's statement: where anything under it may be placed,
		// inherited. Absent all the way up means wherever the platform's Regions places
		// it. Narrowing only, enforced by the audit since the rule spans documents. It is
		// also the default placement for a resource that declares none; PerEnv lets
		// preview go somewhere cheaper.
		StringSlice("regions", PerEnv(),
			Doc("Regions", "The regions everything in this project may be in, and where anything that does not say otherwise is placed. Absent means wherever the platform places it. A scope below may narrow this list and may never widen it.")),
	}
}

// MaxNamespaceDepth is how deep namespaces nest. A self-referential node would
// make the schema graph cyclic, and the interpreter's walk (interp/shape.go
// eachIterIn) has no visited set, so each level is a copy built down to this
// depth.
const MaxNamespaceDepth = 8

// scopeContainer is the group whose instances hold resources of their own: the
// top-level key and the directory a scope is. namespacesGroup is built from it.
const scopeContainer = "namespace"

func namespacesGroup(depth int) *Node {
	children := append([]*Node{}, V2Resources...)
	if depth > 1 {
		children = append(children, namespacesGroup(depth-1))
	}
	return DefineGroup(scopeContainer,
		// The allowance is on the scope, not in identityAttributes(), which every
		// resource shares: a resource is placed, it does not bound placement.
		DefineIterGroup(append(identityAttributes(),
			StringSlice("regions", PerEnv(), InSection("identity"),
				Doc("Regions", "The regions everything in this namespace may be in, and where anything that does not say otherwise is placed. Absent means the parent's. Narrows only; the audit reports a widening."))),
			children...).
			With(Singular("Namespace"),
				// namespace/shop/meta.yaml holds the scope's own fields. Not `namespace`: nested
				// namespaces live in a child group of that name, and a leaf named after a child
				// group is never walked as the group.
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
	attachAccess()
	attachIndexes()
	attachLowerable()
	return nil
}()

// Environments are a table in the project's own document, not resources: nothing
// reaches one and it is read whole. Declaring them is also what tells the
// compiler which suffixes a key may take, so `timeout_prevew` is a typo.
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

			// Where this environment's state lives, as a constraint, not a placement; absent
			// means the cloud decides. A shadow has no such field, which is why it is written
			// inside the environment it shadows.
			StringSlice("regions", InSection("deployment"),
				Doc("Regions", "Where this environment keeps its state, as region names the cloud offers. Absent means the cloud places it.")),
		},
			GroupDoc("The deployments of this project: which branches feed each, and where each keeps its state."),
			Section("deployment", "Deployment", "Which branches feed an environment, and where it keeps its state."),
			Nested(shadowsGroup())),
	).With(RequiredGroup())
}

// A shadow is its own deployment (release, artifact, address, assets) attached to
// the state of the environment it is written in: every stateful kind, the
// secrets and the triggers, so it runs no scheduler or consumer of its own. For an
// A/B variant or a frontend tested on production data. Nested rather than
// referenced, so there is no name to misspell and no `regions` to write.
func shadowsGroup() *Node {
	return DefineGroup("shadows",
		DefineIter([]*Attribute{
			environmentBranches(),
		},
			GroupDoc("Deployments attached to this environment: their own code and their own address, over its state.")),
	)
}

// environmentBranches is a list of branch patterns, shared by environments and
// shadows. One branch may feed several deployments: that is what a shadow is.
func environmentBranches() *Attribute {
	return StringSlice("branches", Required(), InSection("deployment"),
		Doc("Branches", "Which branches feed this deployment. Globs allowed: [main, master], [\"release/*\"], or [\"*\"] for everything else."))
}

var v2Root = Root(
	rootAttributes(),
	append(append([]*Node{}, V2Resources...), scopeShape, environmentsGroup())...,
).With(Singular("Project"), Leaf("project"),
	// A resource is stored under `id` and called by its document's name.
	Identity("id", "name"),
	// Environment is the one axis a key may be suffixed with; a second would be
	// another name here, in precedence order.
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

// terminalStep is a step's on_error when none is written: not a step but the
// terminal queue `{workflow}_failed`.
const terminalStep = "failed"

// entryGroup is where an operation on a lowered kind is carried out, keyed by the
// operation: `start` on a workflow is a send to the start step's queue. Only a
// lowering writes it, so decompile takes it back off with the lowering's record.
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

		// Which key is present is the step's type: no `type:` and no captured Either(),
		// because a list element is written back as data and a captured key would land in
		// the yaml and be refused on the next compile. checkWorkflows refuses a step
		// opening two, the one mutual exclusion here that is a check, not an Either().
		String(Key("call", "function"), Path("call", "function"), IsVariableName(), Ref("function", ByName()), InSection("steps"),
			Doc("Function", "The function this step runs.")),
		String(Key("call", "namespace"), Path("call", "namespace"), IsNamespacePath(), InSection("steps"),
			Doc("Namespace", "Path to the namespace holding the function, e.g. \"/shop/eu\". Omitted means this scope, then its parents.")),

		// What the step is handed and where its answer goes, so wiring stays out of the
		// functions. `input` is an expression; `result` is a name in the run's state.
		String(Key("call", "input"), Path("call", "input"), IsExpression(), InSection("steps"),
			Doc("Input", "What the function is handed, as an expression over the run's state, e.g. \"order.lines\". Omitted hands it the whole state.")),
		String(Key("call", "result"), Path("call", "result"), IsVariableName(), InSection("steps"),
			Doc("Result", "Where the function's answer is kept in the run's state, by name. Omitted means the answer replaces the state.")),

		// Not a Duration(): scalar codecs do not run inside a list element, so the driver
		// parses the string.
		String(Key("wait"), Path("wait"), InSection("steps"),
			Doc("Wait", "Pause for this long, then carry on: a human string (e.g. \"30s\", \"1h\"). A step waits or calls, never both.")),

		// Attempts, not retries: it lands in the queue's `max_attempts`, which counts
		// runs, so the lowering copies rather than computes.
		Int("attempts", MinInt(1), Default(1), InSection("steps"),
			Doc("Attempts", "How many times this step is run before it has failed. 1 means no retry.")),

		// Strings for the same reason as `wait`, parsed by the queue's own Durations. No
		// defaults: no backoff produces no retry block.
		String(Key("backoff", "interval"), InSection("steps"),
			Doc("Backoff Interval", "Delay before a failed attempt is redelivered, e.g. \"5s\". Omitted means the queue's visibility timeout alone governs.")),
		Float(Key("backoff", "factor"), InSection("steps"),
			Doc("Backoff Factor", "Multiplier applied to the interval per further attempt. Omitted or 1 means a fixed interval.")),
		String(Key("backoff", "ceiling"), InSection("steps"),
			Doc("Backoff Ceiling", "The delay never grows past this, e.g. \"5m\". Omitted means no ceiling.")),

		// Defaulted: it fills the produced queue's dead_letter, and without it an
		// exhausted run would have nowhere to go.
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

// When a variable is handed over: at run, at build, or both. A build-time secret
// reaches a build as a reference instead of being pasted into a script. The legal
// set is the caller's, first element the default, so `at: run` on a website is a
// compile error.
// variablesGroup is a resource's literals: a workload's, read when it starts, and a
// package's, read by its build. A secret is read through the vault, never handed
// over.
func variablesGroup() *Node {
	return DefineGroup("variables",
		DefineIter([]*Attribute{
			String("value", Required(), PerEnv(), InSection("config"),
				Doc("Value", "A literal, stored in git and readable by anyone who can read the repo. For anything that is not a secret.")),

			// A file is a second delivery mechanism for values an environment variable cannot
			// carry (certificates, keys, large json).
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
// The number is the one the process binds; the external port is assigned and
// tunnelled, so everything else names a port.
func portsGroup() *Node {
	return DefineGroup("ports",
		DefineIter([]*Attribute{
			Int("number", Required(), MinInt(1), InSection("network"),
				Doc("Number", "The port the process binds INSIDE the container. Not the one anything outside reaches it on.")),
		},
			GroupDoc("A port this workload listens on, named so everything else can refer to it without repeating the number."),
		))
}
