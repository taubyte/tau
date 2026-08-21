// Package schema is the tau v2 configuration vocabulary: the DSL declaration the
// generic tcc interpreter compiles. It is a sibling of taubyte/v1/schema, not a
// revision of it — v1 stays frozen so its parity oracle keeps applying while v2 is
// authored (.notes/v2/edges.md §9.1).
//
// What v2 changes, and why each is a deletion rather than an extension:
//
//   - The function trigger block is GONE. A function declares nothing about how it
//     is reached; reachability is an inbound edge authored on the domain, gateway,
//     channel, p2p service or cron that reaches it. Same for a website's
//     domains/paths. ".notes/v2/resources.md" §1.3, §1.5.
//   - smartops is deleted. Policy is `authorizers` (request-scoped, per-edge);
//     limits are platform-enforced at dispatch. "resources.md" Deletions.
//   - `databases` -> `kv`, `libraries` -> `module`. `database` beside `sql` is a
//     permanent support cost.
//   - `notification.email` is deleted: inert since it was added, read nowhere.
//   - The scope container is `namespaces`, not `applications`. `applications` was
//     v1's word for a scope; in v2 it is the container compute kind, and the scope
//     is named after what the access, quota and connection rules are written about.
//   - Every resource declares what it reaches as a block per target kind, at the top
//     level of its own document — `databases:`, `storages:`, `secrets:` — not under a
//     `uses:` wrapper. Declared or it does not exist. See verbs.go.
//
// Still absent, but no longer for want of a shape — DefineList arrived with the
// transformer chain, so what is left is the modelling: the `route` embed's per-path
// rules (headers/redirects/rewrites/status), `connector.params`, `domain` DNS records,
// and per-edge modifiers (mount, timeout, retry, via). See omissions.go.
package schema

import (
	"errors"
	"strings"

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

// sourceShape: a code source is "." (inline), "module/<name>", or "tau/<name>" — the
// runtime's OWN code.
//
// The third form arrived with the first lowering that produces a workload. An agent
// becomes a function whose loop — call the model, hand it the tools it may reach, run
// one, decide again, stop at the ceiling — is the same program for every agent, and
// belongs to the platform rather than to the author's repository. It has to be
// EXPRESSIBLE, because a lowering that set a source no document could legally contain
// would be generating config that does not compile.
//
// It is not an extension point: `tau/` names something the runtime ships, so the set is
// closed by what exists rather than by what an author can write. An agent whose loop
// were the author's code would be a `function`.
//
// PLURAL, like every other kind. It was `module` alone, which meant one directory in
// a project read `module/` while its nineteen siblings read `functions/`, `caches/`,
// `queues/` — a rule with one exception is a rule everybody has to remember.
var sourceShape = StringShape([]string{"."}, []string{"module/", "tau/"})

// egressAttribute is the per-workload ALLOWLIST of hosts a workload may reach,
// layered on top of the node-wide deny floor (pkg/netguard) which it can never widen.
//
// On EVERY workload kind, not just the container one. It was on `applications` alone,
// which said that a container reaching the internet is a decision and a function
// doing the same is not — and the guest HTTP client is exactly as real as the
// container's socket. A sandbox is the sharpest case: it exists to run code somebody
// else wrote, and it was the one kind with no way to say where that code may call.
func egressAttribute() *Attribute {
	return StringSlice("egress", InSection("network"),
		Doc("Egress", "Hosts this workload may reach. Layered on top of the node-wide deny floor, which it can never widen. Absent means the floor alone."))
}

var secNetwork = Section("network", "Network", "How this workload is reached, and what it may reach.")

// wasmAttributes is the shared shape of everything that runs a WASM entrypoint —
// function, worker's wasm half, entity and authorizer. Declared once because the
// four differ in what points AT them, never in how they run.
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
var hostWorlds = []string{"handler", "authorizer", "gateway-transformer", "proxy-transformer"}

func wasmAttributes(attrs ...*Attribute) []*Attribute {
	return commonAttributes(append([]*Attribute{
		String("implements", InSet(hostWorlds...), Default("handler"), InSection("code"),
			Doc("Implements", "Which host interface this was built against. handler: takes a request. authorizer: takes a caller and an operation, answers with ops. gateway-transformer: takes a parsed HTTP request. proxy-transformer: takes bytes on a stream.")),
		// WHERE THE CODE COMES FROM, as a block. Absent means this resource's own
		// repository — the common case writes nothing at all, which is what "." used to
		// say with a character. `module` names one of the project's modules, with the
		// namespace it lives in beside it. `tau` names a program the RUNTIME ships, and
		// is not an extension point: the set is closed by what exists.
		//
		// It was one string — "." or "module/<name>" or "tau/<name>" — which is a kind
		// packed into a value with no room to say which namespace, and three shapes a
		// reader has to parse.
		String(Key("source", "namespace"), IsNamespacePath(), InSection("code"),
			Doc("Module Namespace", "Where that module lives. Omitted means this resource's own scope, then its parents.")),
		String(Key("source", "module"), IsVariableName(), Ref("module"), InSection("code"),
			Doc("Module", "A module of this project to build from. Omitted, with no tau either, means this resource's own code.")),
		String(Key("source", "tau"), IsVariableName(), InSection("code"),
			Doc("Runtime Program", "A program the runtime itself ships. Not for authors: a lowering sets it.")),
		String(Key("execution", "call"), Required(), InSection("code"), Doc("Entrypoint", "Exported entrypoint symbol invoked in the WASM component.")),
		Duration(Key("execution", "timeout"), Required(), InSection("limits"), Doc("Timeout", "Maximum execution time, as a human string (e.g. \"30s\").")),
		Bytes(Key("execution", "memory"), Required(), InSection("limits"), Doc("Memory", "Maximum memory this workload may use, as a human string (e.g. \"32MB\").")),
		cpuLimit(),
		egressAttribute(),
	}, attrs...)...)
}

var secCode = Section("code", "Code", "The workload's code source and entrypoint.")
var secLimits = Section("limits", "Limits", "Runtime resource limits.")

// gitSource is the repository block shared by `module` and `website`. git-provider
// stays Either("github") — GitLab lands as a second facade, not a provider
// abstraction; generalize on the third (resources.md, Extensibility).
func gitSource() []*Attribute {
	return []*Attribute{
		String("source-branch", Path("source", "branch"), RepoBranch(), InSection("source"), Doc("Branch", "Git branch to build from.")),
		String("provider", Path("source", Either("github")), Captures(), Field("Provider"), InSection("source"), Doc("Provider", "Source-control provider hosting the repository (the key selects the provider block).")),
		String("repository-id", Path("source", "github", "id"), Required(), Field("RepoID"), NoAccessors(), InSection("source"), Doc("Repository ID", "GitHub repository numeric id.")),
		String("repository-name", Path("source", "github", "fullname"), Required(), RepoName(), Field("RepoName"), NoAccessors(), InSection("source"), Doc("Repository", "GitHub repository full name (owner/repo).")),
	}
}

var secSource = Section("source", "Source", "Where the code is sourced from.")

// pullSecret sits beside every `image`. A private registry needs a credential to pull
// from, and without this the container kinds are public-registry-only — which is not a
// decision anyone took, just a field nobody added.
//
// It names a SECRET rather than holding a token, for the same reason a database's
// credentials do: the value belongs in the secret store and the config holds the
// reference. And it is on the workload rather than on a registry resource of its own,
// because a registry with nothing in it but a credential is a secret with extra steps
// — if a second field ever needs to live beside it, that is when the kind earns its
// existence.
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
	out := []*Attribute{
		String("image", Required(), InSection("code"), Doc("Image", "OCI image reference this workload runs.")),
		pullSecret(),
	}
	out = append(out, probeAttributes("readiness", "network", "Whether this instance should be given work.")...)
	out = append(out, probeAttributes("liveness", "network", "Whether this instance is still working, or should be replaced.")...)
	return append(out,
		// How many consecutive liveness failures before the platform kills the
		// instance. The cost of being wrong is asymmetric: a readiness blip takes an
		// instance out of rotation for a second, a liveness blip destroys its state and
		// its warm caches. Three, because one is a network hiccup and ten is a workload
		// wedged for a minute while traffic was still being sent to it.
		Int("liveness-failures", Path("liveness", "failures"), MinInt(1), Default(3), InSection("network"),
			Doc("Liveness Failures", "Consecutive failed liveness checks before this instance is replaced.")),

		Bytes(Key("execution", "memory"), Required(), InSection("limits"), Doc("Memory", "Maximum memory per instance.")),
		cpuLimit(),
		egressAttribute(),

		// Ceilings, not counts. How many instances run is still the platform's — these
		// bound what it may do, which is a different statement from telling it what to
		// do: one is a limit the operator owns (cost, blast radius, what the workload
		// can survive), the other is a number the system would have to fight.
		Int("limits-max-instances", Path("limits", "max-instances"), MinInt(1), InSection("limits"),
			Doc("Max Instances", "The most instances the platform may run of this workload. A ceiling it scales within, not a count.")),
		Int("limits-concurrency", Path("limits", "concurrency"), MinInt(1), InSection("limits"),
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
		String(name+"-kind", Path(name, Either("http", "function")), Captures(), InSection(section),
			Doc(title(name), doc+" An http poll, or a function that answers.")),
		String(name+"-port", Path(name, "http", "port"), IsVariableName(), InSection(section), ShowWhen(name+"-kind", "http"),
			Doc(title(name)+" Port", "Which of this workload's ports the poll is made on, by name.")),
		String(name+"-path", Path(name, "http", "path"), InSection(section), ShowWhen(name+"-kind", "http"),
			Doc(title(name)+" Path", "HTTP path polled.")),
		String(name+"-function-name", Path(name, "function", "name"), IsVariableName(), Ref("function", ByName()), InSection(section), ShowWhen(name+"-kind", "function"),
			Doc(title(name)+" Function", "A function that answers for this workload, when \"working\" is not a status code — a queue drained, a migration finished, a lease still held.")),
		String(name+"-function-namespace", Path(name, "function", "namespace"), IsNamespacePath(), InSection(section), ShowWhen(name+"-kind", "function"),
			Doc(title(name)+" Function Namespace", "Where that function lives. Omitted means this resource's own scope, then its parents.")),
	}
}

func title(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

func cpuLimit() *Attribute {
	return Int("execution-cpu", Path("execution", "cpu"), MinInt(1), InSection("limits"),
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
func certificateSecret(section string) *Attribute {
	return String(Key("certificate", "secret", "name"), IsVariableName(), Ref("secret"),
		RequiredWhen("certificate-source", "provided"), Field("CertSecret"), InSection(section), ShowWhen("certificate-source", "provided"),
		Doc("Certificate Secret", "Secret holding the certificate chain and its private key, as \"secret/<name>\". The key never goes in git."))
}

func pullSecret() *Attribute {
	return String(Key("image-pull-secret", "name"), IsVariableName(), Ref("secret"), InSection("code"),
		Doc("Image Pull Secret", "Secret holding the registry credential this image is pulled with, as \"secret/<name>\". Omit for a public registry."))
}

// handlerKinds are what may serve a route. Closed, because it is the Either() a
// handler's kind switches on: a key outside this set is a compile error rather than
// a route that silently never fires.
// `agent` is in the list because an agent is asked a question and answers it, which is
// a request and a response — the same shape a function has. Leaving it out meant an
// agent could be declared and never reached: nothing could route to one, and the audit
// could not complain either, because a kind no route can name is not one it checks.
var handlerKinds = []string{"function", "application", "website", "gateway", "mcp", "agent"}

// streamServerKinds are what may answer a stream. A container gets the bytes on one of
// its own ports; a function is dispatched with the stream in hand. Both are real, and
// nothing else is: a website is HTTP by definition and a gateway is an ingress.
var streamServerKinds = []string{"application", "function"}

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
		String("host-kind", Path("host", Either("generated", "zone")), Captures(), Field("HostKind"), InSection(section),
			Doc("Host", "generated: a name under the cloud's own zone, resolved when it is deployed. zone: a name in a zone you own and delegated here.")),
		String("host-name", Path("host", Either("generated", "zone"), "name"), IsVariableName(), InSection(section),
			Doc("Name", "The label within that zone, e.g. \"shop\". Omit it for the zone apex.")),
		// INBOUND: the zone is what ANSWERS for this name. The reference is declared
		// here because this is where the name is chosen, but the relationship runs the
		// other way, and an arrow from here would say the gateway reaches the zone.
		String(Key("host", "zone", "in", "name"), IsVariableName(), Ref("zone", Inbound()), RequiredWhen("host-kind", "zone"), InSection(section), ShowWhen("host-kind", "zone"),
			Doc("Zone", "The zone this name lives in, as \"zone/<name>\". Only for a zone you own; a generated name has no zone to choose.")),
	}
}

// externalDatabaseEngines are the database servers reachable over the network, and
// databaseEngines is those plus tau's own. Closed, because each one is a wire protocol
// and a driver — adding one is a release, not a config value — and because a closed set
// is what makes naming a non-database here impossible rather than merely wrong.
var externalDatabaseEngines = []string{"postgres", "mysql"}

var databaseEngines = append([]string{"distributed"}, externalDatabaseEngines...)

// A kind declares Ops() when something can reach it. A kind without them is not a
// target: nothing "uses" a cron, a zone or a module — a cron fires, a zone answers
// DNS, a module is built into a workload and named by its source. Their absence is
// the statement, not an oversight.
//
// V2Resources is the v2 resource vocabulary. The enum is closed: tcc's validation,
// completion, JSON-Schema emission and decompilation all need every type's shape at
// compile time. Openness lives at two seams instead — `connector` for external
// systems, node.Register for services (resources.md, Extensibility decision).
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
			commonAttributes(applicationAttributes()...),
			Nested(portsGroup(), variablesGroup()),
			GroupDoc("A long-running container that listens on HTTP. Mount a volume for anything it must keep."), Icon("box"), Category("compute"),
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
			Nested(variablesGroup()),
			GroupDoc("A serverless function. It declares no trigger: what may reach it is an inbound edge."), Icon("bolt"), Category("compute"),
			secIdentity, secAccess, secConfig, secNetwork, secCode, secLimits,
			Ops(WriteOp("invoke")), // invoke is write: a call may do anything the callee can
			Resource("function", "Function", "Function", "function"),
		)),

	// On-demand, long-running once triggered, not HTTP-listening. The only type
	// where the runtime tier is an authored field (resources.md §1.2).
	DefineGroup("worker",
		resource(
			wasmAttributes(
				String("runtime", InSet("wasm", "container"), Default("wasm"), InSection("code"), Doc("Runtime", "Which tier runs this worker: wasm or container.")),
				String("image", RequiredWhen("runtime", "container"), InSection("code"), Doc("Image", "OCI image reference (container runtime only).")),
				pullSecret(),
			),
			Nested(variablesGroup()),
			GroupDoc("An on-demand workload that runs to completion. Not HTTP-listening."), Icon("gear"), Category("compute"),
			secIdentity, secAccess, secConfig, secNetwork, secCode, secLimits,
			Ops(WriteOp("invoke")),
			Resource("worker", "Worker", "Worker", "worker"),
		)),

	// Interactive container: exec, filesystem, a generated FQDN. idle_timeout is
	// separate from max_lifetime on purpose — an idle agent session and a runaway
	// one are different failures (resources.md §1.7).
	DefineGroup("sandbox",
		resource(
			commonAttributes(
				String("image", Required(), InSection("code"), Doc("Image", "OCI image the sandbox boots from.")),
				pullSecret(),
				Duration("lifetime-idle", Path("lifetime", "idle"), InSection("limits"), Doc("Idle Timeout", "Stop the sandbox after this long with no activity.")),
				Duration("lifetime-max", Path("lifetime", "max"), InSection("limits"), Doc("Max Lifetime", "Hard ceiling on how long a sandbox may live.")),
				egressAttribute(),
				Bytes(Key("execution", "memory"), Required(), InSection("limits"), Doc("Memory", "Maximum memory the sandbox may use.")),
				cpuLimit(),
			),
			Nested(variablesGroup()),
			GroupDoc("An interactive container with exec and filesystem access, for agent and build sessions."), Icon("terminal"), Category("compute"),
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
				String("on-overlap", Path("on-overlap"), InSet("skip", "queue", "allow"), Default("skip"), InSection("firing"),
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

	DefineGroup("module",
		resource(
			commonAttributes(append(
				[]*Attribute{String("source-path", Path("source", "path"), InSection("source"), Doc("Path", "Subpath within the repository that holds the code."))},
				gitSource()...)...),
			GroupDoc("A WASM / Component Model artifact built from a git repository, referenced as a workload source."), Icon("book"), Category("code"),
			secIdentity, secAccess, secSource,
			Resource("module", "Module", "Module", "module"),
		)),

	// Build -> zip -> hoarder -> serve, unchanged. What left: domains, paths and
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
			commonAttributes(append(gitSource(),
				String("serve-index", Path("serve", "index"), Default("index.html"), InSection("serving"),
					Doc("Index Document", "What a request for a directory returns, e.g. \"/docs/\" -> /docs/index.html.")),
				String("serve-mode", Path("serve", "mode"), InSet("static", "spa"), Default("static"), InSection("serving"),
					Doc("Mode", "static: an unmatched path returns the not-found document with 404. spa: it returns the index document with 200, because the page's own router renders the route.")),
				String("serve-not-found", Path("serve", "not-found"), InSection("serving"), ShowWhen("serve-mode", "static"),
					Doc("Not Found Document", "Returned with 404 for a path the site does not have. Omitted means a bare 404.")),
			)...),
			GroupDoc("A static website built from a git repository. What serves it is an inbound domain edge."), Icon("globe"), Category("compute"),
			secIdentity, secAccess, secSource,
			Section("serving", "Serving", "What the site returns for a path it does not have."),
			Resource("website", "Website", "Website", "website"),
		)),

	// ---- data ----

	// A cache. Eventually consistent, AP, and it never gets CAS — an entity is what
	// serves a linearizable counter. It is NOT a database: entries have a lifetime,
	// losing one costs a recomputation, and nothing here is a system of record.
	DefineGroup("cache",
		resource(
			commonAttributes(
				String("match", RequiredUnless("useRegex"), InSection("keys"), Doc("Match", "Key or key-pattern this cache serves.")),
				Bool("useRegex", Path("regex"), InSection("keys"), Doc("Use Regex", "Treat match as a regular expression instead of a literal prefix.")),
				String("reach-network", Path("reach", "network"), InSet("all", "subnet", "host"), Default("all"), EnumBool("Local", []string{"host"}, []string{"all", "host"}, [2]string{"host", "all"}), NoAccessors(), InSection("keys"), Doc("Network Access", "Which peers may reach this cache: all, subnet, or host.")),
				Bytes("size", Path("size"), Required(), InSection("keys"), Doc("Size", "Maximum size, as a human string (e.g. \"32MB\").")),
				Duration("ttl", Path("ttl"), InSection("keys"), Doc("Time-To-Live", "Entry lifetime. A floor, not a guarantee: peers may disagree about existence for up to the clock skew.")),
				// A REFERENCE, never the material. "key material or reference" let the
				// same field be either, and the safe reading was the one nothing enforced.
				String(Key("encryption", "key", "name"), IsVariableName(), Ref("secret"), NoAccessors(), InSection("encryption"),
					Doc("Encryption Key", "Secret holding the key entries are encrypted with, as \"secret/<name>\".")),
			),
			GroupDoc("An eventually-consistent key-value cache served to the project's peers. Not a system of record."), Icon("bolt"), Category("data"),
			secIdentity, secAccess,
			Section("keys", "Keys", "Key matching, capacity, and reachability."),
			Section("encryption", "Encryption", "Encryption at rest."),
			Ops(ReadOp("get"), ReadOp("list"), ReadOp("watch"), WriteOp("put"), WriteOp("delete")),
			Resource("cache", "Cache", "Cache", "cache"),
		)),

	// A database — a system of record, and the only kind here that is one.
	//
	// `engine` is the switch, and the two answers are genuinely different products:
	// `distributed` is tau's own, mushql over CRDT replication, and it is the one that
	// needs no external system; `connector` is somebody else's Postgres or MySQL,
	// reached through the connector that already holds its credentials, which is where
	// every managed database belongs rather than as a second set of fields here.
	//
	//	engine:
	//	    distributed: { size: 1GB }
	//	  # engine:
	//	  #   connector: connectors/pg-main
	// The external engines are a CLOSED set, and this database names one directly
	// rather than pointing at a `connector`.
	//
	// It used to point at one, and `databases/orders` naming `connectors/kafka-orders`
	// compiled clean — Ref() resolves a name to an id and stops there, so nothing
	// noticed that Kafka answers no query. The first fix was a `provides` role list on
	// the connector, which was wrong twice over: it asked the author to restate a fact
	// about software they did not write (whoever implements the postgres connector
	// knows it backs a database; the person copying that into yaml can copy it wrong),
	// and it declared capabilities nothing consumed, which is the inert-field defect
	// this vocabulary deleted `notification.email` for.
	//
	// So the mismatch is made UNREPRESENTABLE rather than detectable. `kafka` is not a
	// key that can be written here, the same way a non-HTTP kind cannot be a gateway
	// handler. A database engine is not an extensibility surface — a new one is a new
	// wire protocol and a new driver, which is a release, not a config value.
	//
	// `connectors` keeps the open seam and goes back to meaning one thing: an external
	// system you RECEIVE EVENTS from.
	//
	// The cost, accepted: a Postgres used both as a database and as an event source is
	// configured twice. They are different credentials and different access anyway, and
	// one config object meaning two things is what caused this.
	DefineGroup("database",
		resource(
			commonAttributes(
				String("engine-kind", Path("engine", Either(databaseEngines...)), Captures(), Field("Engine"), InSection("engine"),
					Doc("Engine", "Which engine backs this database. distributed is tau's own, replicated across the swarm; the rest are external systems reached over the network.")),
				Bytes("engine-distributed-size", Path("engine", "distributed", "size"), RequiredWhen("engine-kind", "distributed"), InSection("engine"), ShowWhen("engine-kind", "distributed"),
					Doc("Size", "Maximum on-disk size, as a human string (e.g. \"1GB\").")),

				// An external engine carries its OWN connection. There is no reference
				// to a `connector` here, and that absence is the point — see the note
				// above this kind.
				String("engine-host", Path("engine", Either(externalDatabaseEngines...), "host"), RequiredWhen("engine-kind", externalDatabaseEngines...), InSection("engine"),
					Doc("Host", "Where the external database is reached, as host or host:port.")),
				String("engine-database", Path("engine", Either(externalDatabaseEngines...), "database"), InSection("engine"),
					Doc("Database", "The database name within that server. Defaults to this resource's name.")),
				String(Key("engine", "credentials", "name"), IsVariableName(), Path("engine", Either(externalDatabaseEngines...), "credentials", "name"), RequiredWhen("engine-kind", externalDatabaseEngines...), Ref("secret"), InSection("engine"),
					Doc("Credentials", "Secret holding the credentials to connect with, as \"secret/<name>\".")),

				// SAFE BY DEFAULT, EXPLICIT TO WEAKEN: absent means verify-full, and an
				// author who wants credentials on the wire in the clear has to write
				// `disable` and see the word.
				//
				// It is not Required(), and not RequiredWhen either, because NEITHER
				// expresses "required only on this Either branch" — both were tried:
				//
				//   Required()                        fires on `distributed` too, which
				//                                     opens no socket
				//   RequiredWhen("engine-kind", …)    never fires at all, because
				//                                     the engine kind is a Captures() attribute
				//                                     and RequiredWhen reads a sibling's
				//                                     DOCUMENT value, which for a Key is
				//                                     the branch's contents rather than
				//                                     its name
				//
				// The second is a live defect, not a quirk of this field:
				// Path("engine","distributed","size") makes the same claim and is equally
				// unenforced. Recorded in omissions.go.
				String("engine-tls", Path("engine", Either(externalDatabaseEngines...), "tls"), InSet("verify-full", "require", "disable"), InSection("engine"), ShowWhen("engine-kind", externalDatabaseEngines...),
					Doc("TLS", "How the connection is protected. Omitted means verify-full. verify-full: encrypted, with the server's certificate and hostname checked. require: encrypted, certificate unverified - a network attacker can still be the server. disable: plaintext, credentials included."))),
			GroupDoc("A database: tau's own distributed engine, or an external server reached over the network."), Icon("database"), Category("data"),
			secIdentity, secAccess,
			Section("engine", "Engine", "Which engine backs it, and its settings."),
			Ops(ReadOp("query"), WriteOp("exec")), // exec is write: the host cannot classify arbitrary SQL
			Resource("database", "Database", "Database", "database"),
		)),

	// NO `sql` KIND, deliberately. It was declared here with exactly one authored
	// field — a capacity number — because everything else about it (one writer,
	// SQLite file format, WAL-mode refusal, savepoints) is runtime behaviour, not
	// configuration. The thing a user would actually author is the schema and its
	// migrations, and that is undesigned and blocked on mushql's differential
	// harness against real C SQLite. A kind with nothing to author is the same
	// defect as `notification.email`: a lie with a type signature. It comes back
	// when there is something to write in it.

	DefineGroup("storage",
		resource(
			commonAttributes(
				String("type", Path(Either("object", "streaming")), Captures(), InSection("storage"), Doc("Type", "Storage kind: object or streaming (the key selects the type block).")),
				String("match", RequiredUnless("useRegex"), InSection("storage"), Doc("Match", "Key or key-pattern this storage serves.")),
				Bool("useRegex", Path("regex"), InSection("storage"), Doc("Use Regex", "Treat match as a regular expression instead of a literal prefix.")),
				String("reach-network", Path("reach", "network"), InSet("all", "subnet", "host"), Default("all"), EnumBool("Public", []string{"all"}, []string{"all", "subnet", "host"}, [2]string{"all", "subnet"}), NoAccessors(), InSection("reach"), Doc("Network Access", "Which peers may reach this storage: all, subnet, or host.")),
				Bool("object-versioning", Path("object", "versioning"), NoSetter(), InSection("storage"), Doc("Versioning", "Keep historical versions of objects (object storage).")),
				Duration("streaming-ttl", Path("streaming", "ttl"), Field("Ttl"), Accessor("TTL"), NoSetter(), InSection("storage"), Doc("Time-To-Live", "Lifetime for streamed entries. A floor, not a guarantee.")),
				Bytes("size", Path(Either("object", "streaming"), "size"), Required(), InSection("storage"), Doc("Size", "Maximum storage size, as a human string (e.g. \"1GB\").")),
			),
			GroupDoc("Object or streaming storage, content-addressed, served to the project's peers."), Icon("bucket"), Category("data"),
			secIdentity, secAccess,
			Section("storage", "Storage", "Storage kind, key matching, and capacity."),
			Section("reach", "Reachability", "Which peers may reach this."),
			Ops(ReadOp("get"), ReadOp("list"), ReadOp("head"), WriteOp("put"), WriteOp("delete")),
			Resource("storage", "Storage", "Storage", "storage"),
		)),

	// A leased, quota'd local directory whose contents are continuously checkpointed
	// into the CAS as a snapshot chain. The chain IS the backup product.
	//
	// rpo-budget and on-budget-exceeded exist because RPO is NOT the checkpoint
	// interval — it is the age of the last fully uploaded, pointer-committed
	// snapshot, and it is unbounded while checkpoints cannot keep up with churn.
	// An unbounded RPO with no ceiling is silent data loss with a dashboard
	// (resources.md §2.4).
	DefineGroup("volume",
		resource(
			commonAttributes(
				Bytes("size", Path("quota"), Required(), InSection("storage"), Doc("Size", "Hard quota for the directory, as a human string (e.g. \"10GB\").")),
				Duration("checkpoint-interval", Path("checkpoint", "interval"), Default("60s"), InSection("checkpoint"), Doc("Checkpoint Interval", "How often the volume is walked, chunked and uploaded.")),
				Duration("checkpoint-rpo-budget", Path("checkpoint", "rpo-budget"), InSection("checkpoint"), Doc("RPO Budget", "Ceiling on the age of the last committed snapshot before on-budget-exceeded fires.")),
				String("checkpoint-on-budget-exceeded", Path("checkpoint", "on-budget-exceeded"), InSet("alert", "freeze-writes"), Default("alert"), InSection("checkpoint"), Doc("On Budget Exceeded", "What to do when the committed-snapshot age passes rpo-budget. Nothing happens without a budget to pass.")),
			),
			GroupDoc("A single-writer leased directory with full POSIX semantics, checkpointed into the CAS. No RWX; bind mounts are refused."), Icon("drive"), Category("data"),
			secIdentity, secAccess,
			Section("storage", "Storage", "Capacity."),
			Section("checkpoint", "Checkpoint", "Durability out of band from the write path."),
			Ops(Mounts(ReadOp("mount-ro")), Mounts(WriteOp("mount-rw"))),
			Resource("volume", "Volume", "Volume", "volume"),
		)),

	// Nearest-neighbour search over embeddings: what an agent remembers, what a
	// retrieval step searches, what a recommender ranks with.
	//
	// ITS OWN KIND rather than an engine on `database`, and the vocabulary's own rule
	// decides it: verbs are per kind, and these are not a database's. A database is
	// asked a QUERY and answers rows; this is asked for the nearest N to a vector and
	// answers neighbours with distances. `query`/`exec` cannot describe `search`/
	// `upsert` — and an engine key that changed which verbs a kind has would break the
	// one thing Ops() exists to guarantee, that a kind's verb set is a fact about the
	// kind.
	//
	// DIMENSIONS ARE FIXED AT CREATION and required, because they are not a setting: an
	// index is built for a width, every vector in it has that width, and changing it is
	// a new index and a re-embed of everything. A default would be a guess at which
	// embedding model someone is using.
	DefineGroup("vector",
		resource(
			commonAttributes(
				Int("dimensions", MinInt(1), Required(), InSection("index"),
					Doc("Dimensions", "The width of every vector stored here, which the embedding model decides — 1536 for OpenAI's text-embedding-3-small, 1024 for Cohere v3. Fixed: changing it is a new index and a re-embed.")),

				// The metric has to match how the model was TRAINED, which is why it is
				// named rather than tuned: cosine for a model trained on normalized
				// embeddings, dot for one that is not, euclidean where magnitude means
				// something. Getting it wrong does not error — it just ranks badly.
				String("metric", InSet("cosine", "dot", "euclidean"), Default("cosine"), InSection("index"),
					Doc("Metric", "How closeness is measured. Match the embedding model: cosine for normalized embeddings, dot where magnitude carries meaning, euclidean for raw distance.")),

				Bytes("size", Path("size"), Required(), InSection("index"),
					Doc("Size", "Maximum on-disk size, as a human string (e.g. \"1GB\"). An index is the vectors plus their neighbour graph, so it is larger than the vectors alone.")),
			),
			GroupDoc("Nearest-neighbour search over embeddings. What an agent remembers, and what a retrieval step searches."), Icon("vector"), Category("data"),
			secIdentity, secAccess,
			Section("index", "Index", "The width of a vector, how closeness is measured, and how much of it there may be."),
			Ops(ReadOp("search"), WriteOp("upsert"), WriteOp("delete")),
			Resource("vector", "Vector", "Vector", "vector"),
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
			Ops(ReadOp("get")), // there is no write verb from a workload, ever
			Resource("secret", "Secret", "Secret", "secret"),
		)),

	// ---- coordination ----

	// A named, durably-stored, single-writer compute instance: the fence is a quorum
	// lease and its epoch. CP, and the docs say so in the first paragraph — during a
	// partition the side that cannot reach K replicas cannot complete a write.
	// `shards` is fixed at creation because it is the lease and replica-set
	// granularity (resources.md §3.1).
	DefineGroup("entity",
		resource(
			wasmAttributes(
				Bool("durable", InSection("coordination"), Doc("Durable", "Journal every host call so an interrupted turn replays deterministically.")),
				Bytes("size", Path("size"), InSection("limits"), Doc("Storage", "Per-instance SQL storage ceiling.")),
			),
			Nested(variablesGroup()),
			GroupDoc("A single-writer, durably-stored compute instance addressed by name. CP: a partition that cannot reach K replicas cannot write."), Icon("cube"), Category("compute"),
			secIdentity, secAccess, secConfig, secNetwork, secCode, secLimits,
			Section("coordination", "Coordination", "Sharding and durability."),
			Ops(ReadOp("get"), WriteOp("call")),
			Resource("entity", "Entity", "Entity", "entity"),
		)),

	// Claim / visibility-timeout / ack / DLQ — not pop-and-delete. Today's
	// pkg/raft Queue.Pop deletes before returning, so a consumer that crashes
	// loses the job with no trace (resources.md §3.2).
	DefineGroup("queue",
		resource(
			commonAttributes(
				Duration("visibility-timeout", Default("30s"), InSection("delivery"), Doc("Visibility Timeout", "How long a claimed item stays invisible before it becomes deliverable again.")),
				Int("max-attempts", Default(5), InSection("delivery"), Doc("Max Attempts", "Deliveries before the item moves to the dead-letter queue.")),
				// A REFERENCE IS A BLOCK. The kind is on the Ref, so writing it into the
				// value is the same fact twice; and `<kind>/<name>` has nowhere to say
				// WHICH namespace, so it silently means "somewhere up from here". Same
				// shape every other cross-resource pointer here already uses.
				String(Key("dead-letter", "namespace"), IsNamespacePath(), InSection("delivery"),
					Doc("Dead Letter Namespace", "Where that queue lives. Omitted means this queue's own scope, then its parents.")),
				String(Key("dead-letter", "name"), IsVariableName(), Ref("queue"), InSection("delivery"),
					Doc("Dead Letter Queue", "Queue that receives items which exhausted max-attempts.")),
			),
			GroupDoc("A global FIFO with claim, visibility timeout, retries and a dead-letter queue. Per-key ordering is an entity, not this."), Icon("list"), Category("data"),
			secIdentity, secAccess,
			Section("delivery", "Delivery", "Claim, retry and dead-letter behaviour."),
			Ops(DeliveredOp("receive"), WriteOp("send")),
			Resource("queue", "Queue", "Queue", "queue"),
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
	// provider when seer is already an authoritative server.
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
				StringSlice("records", InSection("records"), Doc("Records", "Zone-file lines, e.g. \"www 300 IN A 1.2.3.4\", \"@ 3600 IN MX 10 mail.example.com.\".")),
			),
			GroupDoc("A DNS zone served authoritatively by this cloud, and the records you own in it."), Category("internet"), Icon("dns"),
			secIdentity, secAccess,
			Section("records", "Records", "Records this zone answers, in zone-file notation."),
			Resource("zone", "Zone", "Zone", "zone"),
		)),

	// A gateway is a NAME and the policy at that name. It does NOT list its routes:
	// routes name the gateway they attach to, and the gateway says whose routes it
	// accepts.
	//
	// That direction is the Kubernetes Gateway API's, deliberately. An HTTPRoute names
	// its parent through `parentRefs`, and the Gateway's listener carries
	// `allowedRoutes` to permit the attachment, so an infrastructure team owns the
	// gateway and application teams add routes without editing a file they do not own
	// — and many routes merge onto one gateway. The alternative, a gateway listing its
	// routes, makes every new path a change to someone else's config.
	// https://gateway-api.sigs.k8s.io/reference/api-types/httproute/
	//
	// It also matches this DSL's own cross-scope shape: a reference from the source
	// plus a ceiling declared by the target is exactly connection-plus-grant.
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
			append(append(identityAttributes(), hostAttributes("naming")...),
				String("certificate-source", Path("certificate", "source"), InSet("generated", "provided"), Default("generated"), Field("CertSource"), InSection("naming"), ShowWhen("host-kind", "zone"), Doc("Certificate", "generated: issued on demand for this name. provided: from the secret below. A host under the cloud's own zone needs neither — that zone has a wildcard.")),
				certificateSecret("naming"),

				// THE NESTING IS DECLARED BY Path, never spelled into the key. TNS is
				// a CRDT and the compiled object is a tree, so route -> handler ->
				// namespace has to be three mergeable levels; a key that spelled the
				// path itself would be one opaque leaf that can only be replaced whole,
				// which is why "/" in a key is a build failure (engine/keys.go).
				String("route-path", Path("route", "path"), Required(), InSection("route"), Doc("Path", "The path served, e.g. \"/checkout\" or \"/\".")),
				String("route-match", Path("route", "match"), InSet("prefix", "exact", "regex"), Default("prefix"), InSection("route"), Doc("Match", "How the path is compared: prefix, exact, or regex.")),
				// A handler mounted at /docs receives /docs/x unless this is set. For an
				// APPLICATION that is usually right — it routes on the full path and may
				// need the prefix to build links. For a WEBSITE it is usually wrong: the
				// site was built for /, so /docs/x is a file it does not have, and every
				// page 404s while the config looks correct.
				//
				// False by default because passing the request through unchanged is the
				// answer that surprises nobody; the audit reports a website mounted at a
				// prefix without it.
				Bool("route-strip-prefix", Path("route", "strip-prefix"), InSection("route"),
					Doc("Strip Prefix", "Remove the matched path prefix before handing the request on. A website mounted anywhere but / usually wants this.")),
				StringSlice("route-methods", Path("route", "methods"), InSection("route"), Doc("Methods", "HTTP methods answered. Omit for any. GET/HEAD/OPTIONS read, the rest write.")),

				// the switch: which kind handles it, and where that resource lives
				String("route-handler-kind", Path("route", "handler", Either(handlerKinds...)), Captures(), Field("HandlerKind"), InSection("handler"), Doc("Handler", "Which kind serves this path (the key selects the handler block).")),
				String("route-handler-namespace", Path("route", "handler", Either(handlerKinds...), "namespace"), InSection("handler"), Doc("Namespace", "Path to the namespace holding the handler, e.g. \"/shop/eu\". Omitted means the project root.")),
				String("route-handler-name", Path("route", "handler", Either(handlerKinds...), "name"), Required(), IsVariableName(), Ref("", KindFrom("route-handler-kind"), ByName()), InSection("handler"), Doc("Name", "The handler resource's name within that namespace.")),
				String("route-handler-port", Path("route", "handler", Either(handlerKinds...), "port"), IsVariableName(), InSection("handler"), Doc("Port", "Which of the handler's ports to reach, by name. A workload listening on more than one has to be told which, and the number is never written here — the platform assigns the external one, and there may be several instances of the workload on one host.")),

				// The name this gateway answers on, whatever it turns out to be. A
				// generated host is `<label>.<project>.<cloud fqdn>` and the cloud's
				// half is not knowable when the config is written, so an author cannot
				// put their own origin in the list below — which made cors usable with
				// a zone you own and not with the default path.
				Bool("cors-allow-self", Path("cors", "allow-self"), InSection("policy"),
					Doc("Allow Own Origin", "Allow the origin this gateway itself answers on. The only way to say it when the name is generated.")),
				StringSlice("cors-origins", Path("cors", "origins"), InSection("policy"), Doc("CORS Origins", "Origins allowed by CORS.")),
				StringSlice("cors-methods", Path("cors", "methods"), InSection("policy"), Doc("CORS Methods", "Methods allowed by CORS.")),
				Int("rate-limit-rpm", Path("rate-limit", "rpm"), InSection("policy"), Doc("Rate Limit", "Requests per minute.")),
				Int("rate-limit-burst", Path("rate-limit", "burst"), InSection("policy"), Doc("Burst", "Requests allowed above the sustained rate.")),
				String("rate-limit-key", Path("rate-limit", "key"), InSection("policy"), Doc("Rate Limit Key", "What the limit is counted per: \"ip\" or \"header:<name>\".")),
			),
			Nested(transformersGroup()),
			GroupDoc("A name, the policy at it, and what it serves."), Category("internet"), Icon("route"),
			AnswersAt("host"),
			secIdentity, secTransformers,
			Section("naming", "Naming", "The name served, and where its certificate comes from."),
			Section("route", "Route", "The path served and how it is matched."),
			Section("handler", "Handler", "What serves it."),
			Section("policy", "Policy", "CORS and rate limiting, at the name."),
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
			append(append(identityAttributes(), hostAttributes("listener")...),
				String("protocol", InSet("tcp", "udp", "sctp"), Default("tcp"), InSection("listener"),
					Doc("Protocol", "The transport carried. tcp is the transparent one; see this kind's docs for what a tunnel does to udp and sctp.")),

				// The SRV label, and with it the port a client that cannot do SRV should
				// dial. `postgres` publishes `_postgres._tcp.<name>` and tells anything
				// that only knows conventions to use 5432. Defaults to this resource's
				// name, so it is only written when the resource is called something the
				// protocol is not.
				String("service", IsVariableName(), InSection("listener"),
					Doc("Service", "The IANA service this speaks, e.g. \"postgres\". Publishes _<service>._<protocol> beside this name in DNS, and is how a client that cannot look up SRV knows which conventional port to dial. Defaults to the resource's name.")),

				// WHAT A CLIENT LOOKS UP, stated rather than left for a reader to
				// assemble out of `service` and `protocol`. It is the same reason
				// `zone-label` is derived on the project: a name that several consumers
				// need must have one definition, and a renderer composing its own is a
				// second one that agrees only by coincidence.
				String("srv", Derived(DeriveSRVName, "service", "protocol"), NoAccessors(), InSection("listener"),
					Doc("SRV Name", "The DNS name a client looks up to find the port, e.g. \"_postgres._tcp\". Derived from the service and the protocol; never authored.")),

				// Only `terminate` earns its keep: the proxy holds the certificate so a
				// plaintext workload can answer on a public name, reusing exactly the
				// gateway's cert machinery. There is no `passthrough` mode because there
				// is nothing to pass through to a decision — one proxy owns its address,
				// so nothing is multiplexed and no SNI is read.
				String("tls-mode", Path("tls", "mode"), InSet("none", "terminate"), Default("none"), Field("TLSMode"), InSection("listener"),
					Doc("TLS", "none: an opaque pipe, whatever the protocol does about TLS is its own business. terminate: the proxy answers TLS and hands plaintext to the workload.")),
				// F14: DEFAULTED, not RequiredWhen. `tls: terminate` on a GENERATED host
				// needs no certificate — the cloud holds a wildcard for its own zone,
				// which is the gateway's rule and there is no reason for a proxy to have
				// a different one. RequiredWhen("tls-mode","terminate") demanded the
				// field for exactly that case, while ShowWhen hid it from the editor: a
				// compile blocked on a field the author could not see.
				String("certificate-source", Path("certificate", "source"), InSet("generated", "provided"), Default("generated"), Field("CertSource"), InSection("listener"), ShowWhen("host-kind", "zone"),
					Doc("Certificate", "generated: issued on demand for this name. provided: from the secret below. A host under the cloud's own zone needs neither — that zone has a wildcard.")),
				certificateSecret("listener"),

				// Same shape as a gateway route's handler, one kind wide.
				// FORWARD, not "handler". A gateway HANDLES a request — it parses one,
				// matches a path, and something answers it. A proxy handles nothing: it
				// accepts a connection and forwards the bytes. Calling both "handler"
				// borrowed a word from the layer above and made an L4 listener read as
				// if it understood what was going through it.
				String("forward-kind", Path("forward", Either(proxyHandlerKinds...)), Captures(), Field("HandlerKind"), InSection("forward"),
					Doc("Forward To", "Which kind the connection is forwarded to (the key selects the block).")),
				String("forward-namespace", Path("forward", Either(proxyHandlerKinds...), "namespace"), IsNamespacePath(), InSection("forward"),
					Doc("Namespace", "Path to the namespace holding the workload, e.g. \"/shop/eu\". Omitted means the project root.")),
				String("forward-name", Path("forward", Either(proxyHandlerKinds...), "name"), Required(), IsVariableName(), Ref("", KindFrom("forward-kind"), ByName()), InSection("forward"),
					Doc("Name", "The workload's name within that namespace.")),

				// THE WORKLOAD'S OWN PORT, by name — the one it binds inside its
				// container. It is the only port in this file, and deliberately: the
				// port CLIENTS dial is allocated by the platform and published in DNS,
				// because at L4 the port is the address and an authored one would
				// collide across projects. See the note on this kind.
				String("forward-port", Path("forward", Either(proxyHandlerKinds...), "port"), IsVariableName(), InSection("forward"),
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
				Int("rate-limit-connections-per-minute", Path("rate-limit", "connections-per-minute"), MinInt(1), InSection("limits"),
					Doc("Connections / minute", "How fast new connections may be accepted from one source. Absent means unlimited.")),
				Int("rate-limit-burst", Path("rate-limit", "burst"), MinInt(1), InSection("limits"),
					Doc("Burst", "Connections allowed above the sustained rate.")),
				Int("rate-limit-concurrent", Path("rate-limit", "concurrent"), MinInt(1), InSection("limits"),
					Doc("Concurrent", "How many connections may be open at once from one source. The limit that matters when a protocol holds its socket open.")),
				String("rate-limit-key", Path("rate-limit", "key"), InSet("ip", "subnet"), Default("ip"), InSection("limits"),
					Doc("Counted Per", "ip: each address separately. subnet: per /24 and /64, which is what stops one host with an address range from being many clients.")),
			),
			Nested(transformersGroup()),
			GroupDoc("An allocated address that accepts a raw connection and streams it to a workload over p2p. For everything a gateway cannot parse."), Category("internet"), Icon("plug"),
			AnswersAt("host"),
			secIdentity, secTransformers,
			Section("listener", "Listener", "The protocol carried, the name it is reached by, and whether TLS stops here."),
			Section("forward", "Forward", "The workload on the other end."),
			Section("limits", "Limits", "What one source may open, and how fast."),
			Resource("proxy", "Proxy", "Proxy", "proxy"),
		)),

	// Both halves are declared now: the channel edges to its consumer with
	// `subscribe`, the producer edges back with `publish`. Two statements because
	// they are two capabilities — collapsing them into `write` erases a real
	// boundary, and today the publish half is not checked at all (resources.md §5.3).
	// NOT `messaging`, which was v1's word and named a DOMAIN rather than a thing. Every
	// other kind is a noun for the thing itself — a database, a queue, a cache — and
	// nobody says "a messaging". The kind's own field prefix and its documentation
	// already said `channel`; the name now says it too.
	//
	// With the kind called `channel`, `channel: match:` inside `channel/orders.yaml` was
	// the same word twice, so the two matching fields sit at the document root — which
	// is where `cache` and `storage` already put the identical pair.
	DefineGroup("channel",
		resource(
			commonAttributes(
				Bool("local", InSection("matching"), Doc("Local", "Restrict the channel to the local node / project scope.")),
				String("match", Required(), Field("Match"), Accessor("ChannelMatch"), NoSetter(), InSection("matching"), Doc("Match", "Channel name or pattern this resource matches.")),
				Bool("useRegex", Path("regex"), NoSetter(), InSection("matching"), Doc("Use Regex", "Treat match as a regular expression instead of a literal name.")),
				Bool("bridges-mqtt-enable", Path("bridges", "mqtt", "enable"), Accessor("MQTT"), NoSetter(), InSection("bridges"), Doc("MQTT", "Expose this channel over the MQTT bridge.")),
				Bool("bridges-websocket-enable", Path("bridges", "websocket", "enable"), Accessor("WebSocket"), NoSetter(), InSection("bridges"), Doc("WebSocket", "Expose this channel over the WebSocket bridge.")),
			),
			GroupDoc("A PubSub channel, optionally bridged to MQTT/WebSocket. It edges to its subscribers; publishers edge to it."), Icon("message"), Category("trigger"),
			secIdentity, secAccess,
			Section("matching", "Matching", "The name or pattern this channel answers to."),
			Section("bridges", "Bridges", "External protocol bridges."),
			Ops(DeliveredOp("subscribe"), WriteOp("publish")), // never collapse these into "write"
			Resource("channel", "Channel", "Channel", "channel"),
		)),

	// NO `auth` FIELD. It was `none | oauth`, and `oauth` promised to "validate tokens
	// minted by a NAMED authorization server" with nowhere to name one — no issuer, no
	// audience, no keys. A security-shaped claim nothing could honour, which is the
	// inert-field defect this vocabulary deleted `notification.email` for, and worse:
	// an author who read it would believe the server was protected.
	//
	// Deleted rather than completed, because the prerequisite is itself blocked — an
	// authenticated MCP has to be routed on the `Mcp-Method` / `Mcp-Name` headers and
	// this vocabulary cannot match on a header yet (omissions.go). The shape it comes
	// back as is recorded there, so the return is a list entry rather than a redesign.
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
				String("tools-cache-scope", Path("tools", "cache-scope"), InSet("none", "public", "principal"), Default("none"), InSection("tools"), Doc("Cache Scope", "How widely tools/list may be cached. public is cacheable across the whole swarm.")),
				Duration("tools-cache-ttl", Path("tools", "cache-ttl"), InSection("tools"), Doc("Cache TTL", "How long a cached tools/list stays valid.")),
			),
			GroupDoc("An MCP server grouping functions as tools. Stateless Streamable HTTP; subscriptions are declined in v1."), Icon("plug"), Category("internet"),
			secIdentity, secAccess,
			Section("tools", "Tools", "tools/list caching."),
			Resource("mcp", "MCP", "MCP", "mcp"),
		)),

	// ---- in-project addressing ----

	// The kind v1 called `services`, back with the name it should have had. A STREAM:
	// a named, ordered, bidirectional byte channel between two workloads inside the
	// project, carried by libp2p, with no edge in the path.
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
	// identity prepended, so two projects that both call a stream `sync` do not meet.
	// An author who could write the full wire protocol could collide with another
	// tenant on purpose, which is the same reason a proxy authors no port.
	//
	// Deliberately thin, and stated as such: v1's whole declaration was one field. What
	// serves the stream is named here, the way a proxy names what it forwards to,
	// because a stream nothing answers is a name that dials into a void.
	DefineGroup("stream",
		resource(
			commonAttributes(
				String("protocol", Required(), IsVariableName(), InSection("stream"),
					Doc("Protocol", "The name this stream is addressed by within the project, e.g. \"sync\". What goes on the wire has the project's identity prepended, so two projects using the same name never meet.")),

				// Same shape as a proxy's `forward` and a gateway route's handler: the
				// resource that answers, by kind, namespace and name.
				String("serve-kind", Path("serve", Either(streamServerKinds...)), Captures(), InSection("stream"),
					Doc("Served By", "Which kind answers this stream (the key selects the block).")),
				String("serve-namespace", Path("serve", Either(streamServerKinds...), "namespace"), IsNamespacePath(), InSection("stream"),
					Doc("Namespace", "Path to the namespace holding it, e.g. \"/shop/eu\". Omitted means this scope, then its parents.")),
				String("serve-name", Path("serve", Either(streamServerKinds...), "name"), Required(), IsVariableName(), Ref("", KindFrom("serve-kind"), ByName()), InSection("stream"),
					Doc("Name", "Its name within that namespace.")),
				String("serve-port", Path("serve", Either(streamServerKinds...), "port"), IsVariableName(), InSection("stream"),
					Doc("Port", "Which of the workload's own named ports the stream is delivered to. Containers only; a function is dispatched, not connected.")),
			),
			GroupDoc("A named byte stream between two workloads inside the project, over libp2p. The way in that is not an ingress."), Icon("stream"), Category("compute"),
			secIdentity, secAccess,
			Section("stream", "Stream", "The name it answers to, and what answers it."),
			Ops(WriteOp("dial")), // dial is write: what travels is opaque
			Resource("stream", "Stream", "Stream", "stream"),
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
				String("identifier", Required(), InSection("model"),
					Doc("Identifier", "What this model is called where it is served, e.g. \"claude-opus-5\" or \"llama-3.1-70b\". The resource's own name is yours; this one is theirs.")),
				String("format", InSet("openai", "anthropic", "ollama"), Required(), InSection("model"),
					Doc("API Format", "The wire format the endpoint speaks. Not the provider: plenty of things that are not OpenAI serve the OpenAI format, and the caller needs the shape of the request.")),

				// `inference`, not `serve`. A stream already says `serve:` and means
				// something else by it — WHICH KIND answers — and two keys with one
				// spelling is how a check written for one starts firing on the other.
				// It did: the port check read this switch's `local` as a resource kind.
				String("inference-kind", Path("inference", Either("remote", "local")), Captures(), InSection("model"),
					Doc("Inference", "remote: a provider reached over the network. local: a workload in this project serving it (the key selects the block).")),
				String("inference-endpoint", Path("inference", "remote", "endpoint"), IsURL(), RequiredWhen("inference-kind", "remote"), InSection("model"),
					Doc("Endpoint", "Base URL of the provider, e.g. \"https://api.anthropic.com\".")),
				String(Key("inference", "remote", "credentials", "name"), IsVariableName(), Ref("secret"), RequiredWhen("inference-kind", "remote"), InSection("model"),
					Doc("Credentials", "Secret holding the provider credential, as \"secret/<name>\". It lives here rather than on each caller, so rotating it is one edit.")),
				String("inference-workload", Path("inference", "local", "name"), IsVariableName(), RequiredWhen("inference-kind", "local"), InSection("model"),
					Doc("Workload", "The application in this project that serves this model.")),
				String("inference-port", Path("inference", "local", "port"), IsVariableName(), InSection("model"),
					Doc("Port", "Which of that workload's named ports inference is reached on.")),

				// TYPED, not a map. The DSL has no arbitrary-keyed values, and that is
				// the right constraint here rather than a limit worked around: these are
				// the four every format has, they have ranges worth checking, and a
				// provider-specific fifth belongs to whoever added the provider.
				// Recorded in omissions.go.
				Float("parameters-temperature", Path("parameters", "temperature"), InSection("parameters"),
					Doc("Temperature", "How much the model is allowed to vary its answer. 0 is as close to deterministic as a model gets.")),
				Float("parameters-top-p", Path("parameters", "top-p"), InSection("parameters"),
					Doc("Top P", "Nucleus sampling: consider only the most likely tokens summing to this probability.")),
				Int("parameters-max-tokens", Path("parameters", "max-tokens"), MinInt(1), InSection("parameters"),
					Doc("Max Tokens", "Ceiling on one answer. A cost ceiling as much as a length one.")),
				StringSlice("parameters-stop", Path("parameters", "stop"), InSection("parameters"),
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
			commonAttributes(
				// ONE model, named as a resource. The endpoint, the credential, the wire
				// format and the parameters are its business, not this agent's — which
				// is what stopped every agent carrying a copy of the same four facts and
				// gave the credential somewhere to live that is not the caller.
				// `model` is a kind AND this attribute's name, and both live under the
				// same key: this block, and the access group generated for the kind.
				// They coexist because a key an attribute declares is not an entry of
				// the group sharing its name (engine/node.go, claimedUnder).
				String(Key("model", "namespace"), IsNamespacePath(), InSection("model"),
					Doc("Model Namespace", "Where that model lives. Omitted means this agent's own scope, then its parents.")),
				String(Key("model", "name"), IsVariableName(), Ref("model"), Required(), InSection("model"),
					Doc("Model", "The model this agent answers with.")),

				// The prompt is a string here and a file nowhere, which is the wrong
				// way round for anything long. Recorded in omissions.go rather than
				// solved by letting a 40KB system prompt into a yaml document.
				String("instructions", InSection("model"),
					Doc("Instructions", "The system prompt. For anything longer than a paragraph, keep it in the workload's own source and leave this empty.")),

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
				String(Key("memory", "embedder", "name"), IsVariableName(), Ref("model"), InSection("memory"),
					Doc("Embedding Model", "The model that turns what this agent sees into vectors, as \"model/<name>\". Naming it is what turns memory on.")),
				Int("memory-dimensions", Path("memory", "dimensions"), MinInt(1), RequiredWhen(Key("memory", "embedder", "name")), InSection("memory"),
					Doc("Dimensions", "The width the embedding model produces — 1536 for text-embedding-3-small. Fixed once there is anything in memory.")),
				Bytes("memory-size", Path("memory", "size"), Default("1GB"), InSection("memory"),
					Doc("Memory Size", "How much the agent may remember, as a human string.")),

				Int("limits-steps", Path("limits", "steps"), MinInt(1), Required(), InSection("limits"),
					Doc("Max Steps", "How many times this agent may call a tool and decide again in one invocation. Required: an agent with no ceiling is a loop that spends money until someone notices.")),
				Int("limits-concurrent", Path("limits", "concurrent"), MinInt(1), InSection("limits"),
					Doc("Concurrent", "How many invocations may run at once.")),
				egressAttribute(),
			),
			Nested(variablesGroup()),
			GroupDoc("An LLM loop with tools. What it may call is what it declares access to — there is no second list."), Icon("agent"), Category("compute"),
			secIdentity, secAccess, secConfig, secNetwork, secLimits,
			Section("model", "Model", "Which model answers, and what it is told."),
			Section("memory", "Memory", "What it remembers, and how wide the vectors are."),
			Ops(WriteOp("invoke")),

			// AN AGENT LOWERS, like a workflow: nothing in the substrate runs "agents".
			// The loop is a function, and what it remembers is a vector index — both
			// resources the platform already runs, so an agent adds a vocabulary and no
			// component.
			//
			// The function's SOURCE is the runtime's own (`tau/agent`), which is the one
			// thing here an author does not supply: the loop — call the model, hand it
			// the tools it may reach, run one, decide again, stop at the ceiling — is the
			// same program for every agent, and the parts that differ are the fields
			// above. An agent whose loop was the author's code would be a `function`.
			Lowers(
				Into("function",
					// The loop is what CALLS the tools this agent declares, so opened,
					// the box reads model-and-memory-and-tools around it.
					Acts(),
					Named("{name}_infer"),
					Carries("the loop: call the model, run a tool, decide again"),
					Sets(Key("source", "tau"), "agent"),
					Sets(Key("execution", "call"), "infer"),
					Sets(Key("execution", "timeout"), "5m"),
					Sets(Key("execution", "memory"), "128MB"),
					Sets("implements", "handler"),
				),

				// Only when there is an embedder to fill it: an agent that remembers
				// nothing gets no index rather than an empty one.
				Into("vector",
					When(Key("memory", "embedder", "name")),
					Named("{name}_memory"),
					Carries("what {name} remembers"),
					Sets("dimensions", "{memory-dimensions}"),
					Sets("size", "{memory-size}"),
					// The loop is what searches it, and that is an entry on the loop:
					// `vector: { <name>_memory: { ops: [search] } }`, exactly what an
					// author would have written.
					Reaches("search", From("function", "{name}_infer")),
				),
			),
			Resource("agent", "Agent", "Agent", "agent"),
		)),

	// A state machine over things that already exist. Steps run in the order written;
	// each names a function, may be retried, and may hand off to another step when it
	// finally fails.
	//
	// IT LOWERS. A workflow is not a component — nothing in the substrate runs
	// "workflows". Each step becomes a queue with the step's retry budget, failures
	// land in a dead-letter queue, and the run's own state lives in an entity, which is
	// the only kind here that gives a single writer per key. Every one of those is a
	// primitive this vocabulary already has and the platform already runs, which is
	// what makes a workflow a compile-time construct rather than a new subsystem to
	// operate. See Lowers() below, and interp/lowering.go for the expansion.
	//
	// LINEAR, for now, and that is a real limit rather than a simplification: there is
	// no Choice, no Parallel, no Map. Those are the shapes that make Step Functions
	// what it is, and each needs something the list element cannot hold yet.
	// omissions.go records which.
	DefineGroup("workflow",
		resource(
			commonAttributes(
				Duration("timeout", InSection("run"),
					Doc("Timeout", "Ceiling on one whole run, as a human string (e.g. \"1h\"). Absent means a run may take as long as its steps do.")),
				Int("concurrency", MinInt(1), InSection("run"),
					Doc("Concurrency", "How many runs may be in flight at once. Absent means unlimited.")),
			),
			Nested(stepsGroup()),
			GroupDoc("An ordered pipeline of functions with retries. It runs on queues and an entity — nothing new is deployed to serve it."), Icon("workflow"), Category("trigger"),
			secIdentity, secAccess,
			Section("run", "Run", "Ceilings on one run and on all of them."),
			Section("steps", "Steps", "What runs, in order."),
			Ops(WriteOp("start")),

			// THE LOWERING, declared rather than computed. tcc expands it (interp/
			// lowering.go); this says what a workflow becomes and nothing about how.
			//
			// One queue PER STEP rather than one for the workflow, because a queue is
			// where a retry budget lives and the budget is the step's: `reserve` may be
			// worth three attempts and `charge` exactly one. Sharing a queue would make
			// the whole pipeline share whichever number was written last.
			//
			// An ENTITY for the run rather than a row in a database, because a run is
			// the definition of a single-writer key — two nodes advancing the same run
			// past the same step is the one thing this must not do, and an entity is the
			// only kind here that fences it.
			Lowers(
				// One queue PER STEP: a queue is where a retry budget lives, and the
				// budget is the step's — `reserve` may be worth four attempts and
				// `charge` exactly one. Sharing a queue would make the whole pipeline
				// share whichever number was written last.
				Into("queue",
					PerEach("steps"),
					Named("{name}_{each.name}"),
					Carries("work waiting for step {each.name}"),
					Sets("max-attempts", "{each.attempts}"),
					Sets(Key("dead-letter", "name"), "{name}_failed"),

					// WHO CONSUMES IT. A queue nothing receives from is a queue nothing
					// happens in, so the step's function is named as its consumer — the
					// `receive` half of the queue's own verbs, in the direction work
					// actually travels.
					Reaches("receive", From("function", "{each.run}", "{each.namespace}")),

					// WHAT MAKES IT A PIPELINE — and it starts at the FUNCTION, not at
					// the queue. A queue does not hand work to another queue; the
					// function that consumed this one sends to the next step's queue
					// when it is done, and an edge drawn queue-to-queue describes
					// something that cannot happen.
					//
					// The last step has no next, the template cannot be filled, and the
					// link is dropped — which is exactly right, because that is where the
					// run ends.
					Reaches("send",
						From("function", "{each.run}", "{each.namespace}"),
						To("queue", "{name}_{each.next.name}")),

					// Same origin for a step that names somewhere other than the failure
					// queue to go when it has used up its attempts.
					Reaches("send",
						From("function", "{each.run}", "{each.namespace}"),
						To("queue", "{name}_{each.on-error}")),
				),

				// The end of the line, with NO CONSUMER — deliberately. A dead-letter
				// queue is where a run stops so a person can look at it; something
				// draining it automatically would be the retry that already happened.
				Into("queue",
					Named("{name}_failed"),
					Carries("runs that gave up, kept for a person to look at - nothing consumes this"),
				),

				// AND NOTHING ELSE. There is no store for the run's state, because the
				// run's state is the MESSAGE: what the run carries travels with it from
				// one step's queue to the next, and which queue it is sitting in IS
				// which step it is at. A pipeline needs no second place to write that
				// down, and the second place is what would have to be kept in agreement
				// with the first.
				//
				// It briefly lowered into an `entity` for exactly that job, which was
				// wrong twice: an entity is a code-bearing compute instance — source,
				// entrypoint, timeout and memory, none of which a workflow has to give
				// it — so it was a resource nobody could instantiate, and it was
				// answering a question the queue had already answered. CheckLowerings
				// now refuses a lowering that leaves a required field unset, so that
				// particular mistake is a build failure rather than a plan that reads
				// fine.
				//
				// What is genuinely missing is run HISTORY — what ran, when, and what it
				// returned — which is a database and a decision, not a queue. Recorded
				// in omissions.go.
			),
			Resource("workflow", "Workflow", "Workflow", "workflow"),
		)),

	// One type for every external system: it holds the credentials, and for
	// source-like externals it holds the connection too — Lambda's event-source
	// mapping, where a separate poller joins the source and the invoked compute
	// never touches it. A connector that holds a live connection runs exactly once
	// per cloud: a quorum lease with an epoch on every event it emits, so a stale
	// poller's events are rejected rather than duplicated (resources.md §6).
	DefineGroup("connector",
		resource(
			commonAttributes(
				String("type", Required(), InSection("identity"), Doc("Type", "Which external system events are received from, e.g. \"kafka\", \"s3\", \"slack\".")),
				String(Key("credentials", "namespace"), IsNamespacePath(), InSection("identity"), Doc("Credentials Namespace", "Where that secret lives. Omitted means this connector's own scope, then its parents.")),
				String(Key("credentials", "name"), IsVariableName(), Ref("secret"), InSection("identity"), Doc("Credentials", "Secret holding this connector's credentials.")),
				Bool("polls", InSection("identity"), Doc("Polls", "This connector holds a live connection to the external system and emits events from it. Exactly one node in the cloud runs it.")),
			),
			GroupDoc("An external system events are received from: credentials, and the held connection. Events are delivered as CloudEvents over tau pubsub."), Icon("cable"), Category("trigger"),
			secIdentity, secAccess,
			Resource("connector", "Connector", "Connector", "connector"),
		)),
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
		String("zone-label", Derived(DeriveDNSLabel, "id"), NoAccessors(),
			Validator(func(v string) error {
				if v == "" {
					return nil
				}
				return errors.New("zone-label is derived from the project id, not authored; remove it")
			}),
			Doc("Zone Label", "The project's label in a cloud's generated zone, derived from its id. Read-only.")),
		String("description", Doc("Description", "Free-form, human-readable description of the project.")),
		StringSlice("tags", WireDrop(), Doc("Tags", "Project-level labels.")),
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
		DefineIterGroup(identityAttributes(), children...).
			With(Singular("Namespace"),
				// namespace/shop/scope.yaml.
				//
				// NOT "namespace", which is what it was called while the kinds were
				// plural. With singular kinds the nested namespaces live in a child
				// group ALSO called `namespace`, at the same level — and a leaf whose
				// name collides with a child group is skipped as the leaf and never
				// walked as a group, which is the same silent drop that made `config`
				// unusable. A leaf must not be named after any child of its own group.
				Leaf("scope"),
				GroupDoc("A named scope holding resources, and namespaces, of its own. Names resolve here first, then in the parent."),
				secIdentity,
				Icon("apps"),
				// A namespace is looked up by name like everything else: `/shop/eu`
				// is written as names and the object is keyed by ids, so something
				// has to turn one into the other. Last position, after every kind.
				interp.Indexes(len(V2Resources), byName)))
}

var _ = func() any { attachAccess(); attachIndexes(); return nil }()

var v2Root = Root(
	rootAttributes(),
	append(append([]*Node{}, V2Resources...), namespacesGroup(MaxNamespaceDepth))...,
).With(Singular("Project"), Leaf("project"), Strict(),
	// A resource is stored under its `id` and called by the name of the document it was
	// written in — the interpreter's two field names, which are this vocabulary's to
	// choose and therefore this vocabulary's to state.
	Identity("id", "name"))

var TaubyteProject = SchemaDefinition(v2Root)

// GenerationRoot is the node list a generator walks: the resource groups plus the
// applications container, read off the real root so no curated list can drift.
func GenerationRoot() []*Node { return v2Root.Children }

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
func stepsGroup() *Node {
	return DefineList("steps", []*Attribute{
		String("name", Required(), IsVariableName(), InSection("steps"),
			Doc("Step", "What this step is called. Another step's on-error names it, and the run's state is keyed by it.")),
		String("namespace", IsNamespacePath(), InSection("steps"),
			Doc("Namespace", "Path to the namespace holding the function, e.g. \"/shop/eu\". Omitted means this scope, then its parents.")),
		String("run", Required(), IsVariableName(), Ref("function", ByName()), InSection("steps"),
			Doc("Function", "The function this step invokes.")),

		// ATTEMPTS, not retries. "3 retries" is three or four runs depending on who is
		// reading, and the number lands in a queue's `max-attempts`, which counts runs
		// — so the config counts what the thing it becomes counts, and the lowering is
		// a copy rather than arithmetic. Templates must not grow a calculator.
		//
		// Not a duration either: what a step may TAKE is the function's own
		// execution/timeout, and a second ceiling here would be a number that disagrees
		// with it.
		Int("attempts", MinInt(1), Default(1), InSection("steps"),
			Doc("Attempts", "How many times this step is run before it has failed. 1 means no retry. The delay between attempts is the queue's, not the step's.")),
		String("on-error", IsVariableName(), InSection("steps"),
			Doc("On Error", "Another step to run when this one has exhausted its retries. Omitted means the run fails there.")),
	}, GroupDoc("One step: a function, how often it is retried, and where failure goes."))
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
func variablesGroup() *Node {
	return DefineGroup("variables",
		DefineIter([]*Attribute{
			String("value", InSection("config"),
				Doc("Value", "A literal, stored in git and readable by anyone who can read the repo. For anything that is not a secret.")),
			String(Key("secret", "name"), IsVariableName(), Ref("secret"), InSection("config"),
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
