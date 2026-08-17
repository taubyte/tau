// Package schema is the tau v2 configuration vocabulary: the DSL declaration the
// generic tcc interpreter compiles. It is a sibling of taubyte/v1/schema, not a
// revision of it — v1 stays frozen so its parity oracle keeps applying while v2 is
// authored (.notes/v2/edges.md §9.1).
//
// What v2 changes, and why each is a deletion rather than an extension:
//
//   - The function trigger block is GONE. A function declares nothing about how it
//     is reached; reachability is an inbound edge authored on the domain, gateway,
//     messaging channel, p2p service or cron that reaches it. Same for a website's
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
// Still absent, because a list of objects has no representation in the DSL and the
// authored form for these is not settled: the `route` embed's per-path rules
// (headers/redirects/rewrites/status), `connector.params`, `domain` DNS records, and
// per-edge modifiers (mount, timeout, retry, via).
package schema

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

		if _, reachable := iter.Meta["ops"].([]Verb); reachable {
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

// sourceShape: a code source is "." (inline) or "module/<name>". The v1 prefix was
// "libraries/"; the rename carries into the reference form.
var sourceShape = StringShape([]string{"."}, []string{"module/"})

// wasmAttributes is the shared shape of everything that runs a WASM entrypoint —
// function, worker's wasm half, entity and authorizer. Declared once because the
// four differ in what points AT them, never in how they run.
func wasmAttributes(attrs ...*Attribute) []*Attribute {
	return commonAttributes(append([]*Attribute{
		String("source", Required(), Ref("module", Prefix("module/")), sourceShape, InSection("code"), Doc("Source", "Code source: \".\" for inline code, or \"module/<name>\" to build from a defined module.")),
		String("execution/call", Path("execution", "call"), Required(), InSection("code"), Doc("Entrypoint", "Exported entrypoint symbol invoked in the WASM component.")),
		Duration("execution/timeout", Path("execution", "timeout"), Required(), InSection("limits"), Doc("Timeout", "Maximum execution time, as a human string (e.g. \"30s\").")),
		Bytes("execution/memory", Path("execution", "memory"), Required(), InSection("limits"), Doc("Memory", "Maximum memory this workload may use, as a human string (e.g. \"32MB\").")),
	}, attrs...)...)
}

var secCode = Section("code", "Code", "The workload's code source and entrypoint.")
var secLimits = Section("limits", "Limits", "Runtime resource limits.")

// gitSource is the repository block shared by `module` and `website`. git-provider
// stays Either("github") — GitLab lands as a second facade, not a provider
// abstraction; generalize on the third (resources.md, Extensibility).
func gitSource() []*Attribute {
	return []*Attribute{
		String("source/branch", Path("source", "branch"), RepoBranch(), InSection("source"), Doc("Branch", "Git branch to build from.")),
		String("provider", Path("source", Either("github")), Key(), Field("Provider"), InSection("source"), Doc("Provider", "Source-control provider hosting the repository (the key selects the provider block).")),
		String("repository-id", Path("source", "github", "id"), Required(), Field("RepoID"), NoAccessors(), InSection("source"), Doc("Repository ID", "GitHub repository numeric id.")),
		String("repository-name", Path("source", "github", "fullname"), Required(), RepoName(), Field("RepoName"), NoAccessors(), InSection("source"), Doc("Repository", "GitHub repository full name (owner/repo).")),
	}
}

var secSource = Section("source", "Source", "Where the code is sourced from.")

// handlerKinds are what may serve a route. Closed, because it is the Either() a
// handler's kind switches on: a key outside this set is a compile error rather than
// a route that silently never fires.
var handlerKinds = []string{"function", "application", "website", "gateway", "mcp"}

// proxyHandlerKinds are what may sit behind an L4 proxy. Only a container: a raw
// connection is held open and streamed, and a function is request-and-answer with no
// connection to hold. A website or an mcp is HTTP by definition, so a route serves
// them and this never does.
//
// It is still an Either() with one branch rather than a flat block, so a proxy's
// handler reads exactly like a gateway route's, and a second kind is a list entry
// rather than a reshape.
var proxyHandlerKinds = []string{"application"}

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
	DefineGroup("applications",
		resource(
			commonAttributes(
				String("image", Required(), InSection("code"), Doc("Image", "OCI image reference this workload runs.")),
				// How the platform decides this workload is up. Two answers, and they
				// are different in kind, so the switch names which: an HTTP probe the
				// platform makes itself, or a FUNCTION that decides — given the
				// workload, it answers healthy or not, and can look at whatever
				// "healthy" actually means here (a queue drained, a cache warm, a
				// migration finished) rather than at a status code.
				String("health/kind", Path("health", Either("http", "function")), Key(), Field("HealthKind"), InSection("network"),
					Doc("Health", "How readiness is decided: an http probe, or a function that answers.")),
				String("health/port", Path("health", "http", "port"), IsVariableName(), InSection("network"), ShowWhen("health/kind", "http"),
					Doc("Health Port", "Which of this workload's ports the probe is made on, by name.")),
				String("health/path", Path("health", "http", "path"), InSection("network"), ShowWhen("health/kind", "http"),
					Doc("Health Path", "HTTP path polled for readiness.")),
				String("health/function/name", Path("health", "function", "name"), IsVariableName(), InSection("network"), ShowWhen("health/kind", "function"),
					Doc("Health Function", "A function that decides whether this workload is ready.")),
				String("health/function/namespace", Path("health", "function", "namespace"), IsNamespacePath(), InSection("network"), ShowWhen("health/kind", "function"),
					Doc("Health Function Namespace", "Where that function lives. Omitted means this resource's own scope, then its parents.")),
				Bytes("execution/memory", Path("execution", "memory"), Required(), InSection("limits"), Doc("Memory", "Maximum memory per instance.")),
				StringSlice("egress", InSection("network"), Doc("Egress", "Hosts this workload may reach. Layered on top of the node-wide deny floor, which it can never widen.")),

				// Ceilings, not counts. How many instances run is still the
				// platform's — these bound what it may do, which is a different
				// statement from telling it what to do: one is a limit the operator
				// owns (cost, blast radius, what the workload can survive), the other
				// is a number the system would have to fight.
				Int("limits/max-instances", Path("limits", "max-instances"), MinInt(1), InSection("limits"),
					Doc("Max Instances", "The most instances the platform may run of this workload. A ceiling it scales within, not a count.")),
				Int("limits/concurrency", Path("limits", "concurrency"), MinInt(1), InSection("limits"),
					Doc("Concurrency", "The most simultaneous requests one instance may be given. Cloud Run and Knative call this concurrency; Fly calls it a hard limit. Above it the platform queues or starts another instance, within max-instances.")),
			),
			Nested(portsGroup()),
			GroupDoc("A long-running container that listens on HTTP. Ships with volumes or it ships broken."), Icon("box"), Category("compute"),
			secIdentity, secAccess, secCode, secLimits,
			Section("network", "Network", "Listening port, health, and declared egress."),
			Resource("applications", "Application", "Application", "application"),
		)),

	// A function is now only code plus limits. Nine attributes, one section, three
	// conditional sections and the domains foreign key left with the trigger block.
	//
	// An AUTHORIZER is one of these, not a kind of its own: it was declared with
	// exactly a function's fields and nothing else, and what distinguishes it is the
	// host interface it is built against — it is handed the operation and answers
	// yes or no — which is a property of the artifact, not of the config.
	DefineGroup("functions",
		resource(
			wasmAttributes(),
			GroupDoc("A serverless function. It declares no trigger: what may reach it is an inbound edge."), Icon("bolt"), Category("compute"),
			secIdentity, secAccess, secCode, secLimits,
			Ops(WriteOp("invoke")), // invoke is write: a call may do anything the callee can
			Resource("functions", "Function", "Function", "function"),
		)),

	// On-demand, long-running once triggered, not HTTP-listening. The only type
	// where the runtime tier is an authored field (resources.md §1.2).
	DefineGroup("workers",
		resource(
			wasmAttributes(
				String("runtime", InSet("wasm", "container"), Default("wasm"), InSection("code"), Doc("Runtime", "Which tier runs this worker: wasm or container.")),
				String("image", RequiredWhen("runtime", "container"), InSection("code"), Doc("Image", "OCI image reference (container runtime only).")),
			),
			GroupDoc("An on-demand workload that runs to completion. Not HTTP-listening."), Icon("gear"), Category("compute"),
			secIdentity, secAccess, secCode, secLimits,
			Ops(WriteOp("invoke")),
			Resource("workers", "Worker", "Worker", "worker"),
		)),

	// Interactive container: exec, filesystem, a generated FQDN. idle_timeout is
	// separate from max_lifetime on purpose — an idle agent session and a runaway
	// one are different failures (resources.md §1.7).
	DefineGroup("sandboxes",
		resource(
			commonAttributes(
				String("image", Required(), InSection("code"), Doc("Image", "OCI image the sandbox boots from.")),
				Duration("lifetime/idle", Path("lifetime", "idle"), InSection("limits"), Doc("Idle Timeout", "Stop the sandbox after this long with no activity.")),
				Duration("lifetime/max", Path("lifetime", "max"), InSection("limits"), Doc("Max Lifetime", "Hard ceiling on how long a sandbox may live.")),
				Bytes("execution/memory", Path("execution", "memory"), Required(), InSection("limits"), Doc("Memory", "Maximum memory the sandbox may use.")),
			),
			GroupDoc("An interactive container with exec and filesystem access, for agent and build sessions."), Icon("terminal"), Category("compute"),
			secIdentity, secAccess, secCode, secLimits,
			Ops(WriteOp("exec"), ReadOp("read"), WriteOp("write")),
			Resource("sandboxes", "Sandbox", "Sandbox", "sandbox"),
		)),

	// One field and one outbound edge — the thinnest resource in the vocabulary.
	// A schedule has to be nameable because an edge needs two addressable ends,
	// and that buys "what runs on a schedule" as a graph query (resources.md §1.6).
	DefineGroup("crons",
		resource(
			commonAttributes(
				String("schedule", Required(), InSection("identity"), Doc("Schedule", "Cron expression, e.g. \"0 3 * * *\".")),
			),
			GroupDoc("A schedule. It fires whatever it edges to; the target declares nothing."), Icon("clock"), Category("trigger"),
			secIdentity, secAccess,
			Resource("crons", "Cron", "Cron", "cron"),
		)),

	DefineGroup("module",
		resource(
			commonAttributes(append(
				[]*Attribute{String("source/path", Path("source", "path"), InSection("source"), Doc("Path", "Subpath within the repository that holds the code."))},
				gitSource()...)...),
			GroupDoc("A WASM / Component Model artifact built from a git repository, referenced as a workload source."), Icon("book"), Category("code"),
			secIdentity, secAccess, secSource,
			Resource("module", "Module", "Module", "module"),
		)),

	// Build -> zip -> hoarder -> serve, unchanged. What left: domains, paths and
	// the serving section. A website is served because a domain edges to it.
	DefineGroup("websites",
		resource(
			commonAttributes(gitSource()...),
			GroupDoc("A static website built from a git repository. What serves it is an inbound domain edge."), Icon("globe"), Category("compute"),
			secIdentity, secAccess, secSource,
			Resource("website", "Website", "Website", "website"),
		)),

	// ---- data ----

	// A cache. Eventually consistent, AP, and it never gets CAS — an entity is what
	// serves a linearizable counter. It is NOT a database: entries have a lifetime,
	// losing one costs a recomputation, and nothing here is a system of record.
	DefineGroup("caches",
		resource(
			commonAttributes(
				String("match", RequiredUnless("useRegex"), InSection("keys"), Doc("Match", "Key or key-pattern this cache serves.")),
				Bool("useRegex", Path("regex"), InSection("keys"), Doc("Use Regex", "Treat match as a regular expression instead of a literal prefix.")),
				String("reach/network", Path("reach", "network"), InSet("all", "subnet", "host"), Default("all"), EnumBool("Local", []string{"host"}, []string{"all", "host"}, [2]string{"host", "all"}), NoAccessors(), InSection("keys"), Doc("Network Access", "Which peers may reach this cache: all, subnet, or host.")),
				Bytes("storage/size", Path("storage", "size"), Required(), InSection("keys"), Doc("Size", "Maximum size, as a human string (e.g. \"32MB\").")),
				Duration("storage/ttl", Path("storage", "ttl"), InSection("keys"), Doc("Time-To-Live", "Entry lifetime. A floor, not a guarantee: peers may disagree about existence for up to the clock skew.")),
				String("encryption/key", Path("encryption", "key"), NoAccessors(), InSection("encryption"), Doc("Encryption Key", "Key material or reference used to encrypt entries at rest.")),
			),
			GroupDoc("An eventually-consistent key-value cache served to the project's peers. Not a system of record."), Icon("bolt"), Category("data"),
			secIdentity, secAccess,
			Section("keys", "Keys", "Key matching, capacity, and reachability."),
			Section("encryption", "Encryption", "Encryption at rest."),
			Ops(ReadOp("get"), ReadOp("list"), ReadOp("watch"), WriteOp("put"), WriteOp("delete")),
			Resource("caches", "Cache", "Cache", "cache"),
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
	DefineGroup("databases",
		resource(
			commonAttributes(
				String("engine/kind", Path("engine", Either("distributed", "connector")), Key(), Field("Engine"), InSection("engine"), Doc("Engine", "distributed: tau's own, replicated across the swarm. connector: an external database reached through a connector.")),
				Bytes("engine/distributed/size", Path("engine", "distributed", "size"), RequiredWhen("engine", "distributed"), InSection("engine"), ShowWhen("engine", "distributed"), Doc("Size", "Maximum on-disk size, as a human string (e.g. \"1GB\").")),
				String("engine/connector", Path("engine", "connector"), RequiredWhen("engine", "connector"), Ref("connectors", Prefix("connectors/")), InSection("engine"), ShowWhen("engine", "connector"), Doc("Connector", "The connector holding this database's address and credentials, as \"connectors/<name>\".")),
			),
			GroupDoc("A database: tau's own distributed engine, or an external one reached through a connector."), Icon("database"), Category("data"),
			secIdentity, secAccess,
			Section("engine", "Engine", "Which engine backs it, and its settings."),
			Ops(ReadOp("query"), WriteOp("exec")), // exec is write: the host cannot classify arbitrary SQL
			Resource("databases", "Database", "Database", "database"),
		)),

	// NO `sql` KIND, deliberately. It was declared here with exactly one authored
	// field — a capacity number — because everything else about it (one writer,
	// SQLite file format, WAL-mode refusal, savepoints) is runtime behaviour, not
	// configuration. The thing a user would actually author is the schema and its
	// migrations, and that is undesigned and blocked on mushql's differential
	// harness against real C SQLite. A kind with nothing to author is the same
	// defect as `notification.email`: a lie with a type signature. It comes back
	// when there is something to write in it.

	DefineGroup("storages",
		resource(
			commonAttributes(
				String("type", Path(Either("object", "streaming")), Key(), InSection("storage"), Doc("Type", "Storage kind: object or streaming (the key selects the type block).")),
				String("match", RequiredUnless("useRegex"), InSection("storage"), Doc("Match", "Key or key-pattern this storage serves.")),
				Bool("useRegex", Path("regex"), InSection("storage"), Doc("Use Regex", "Treat match as a regular expression instead of a literal prefix.")),
				String("reach/network", Path("reach", "network"), InSet("all", "subnet", "host"), Default("all"), EnumBool("Public", []string{"all"}, []string{"all", "subnet", "host"}, [2]string{"all", "subnet"}), NoAccessors(), InSection("reach"), Doc("Network Access", "Which peers may reach this storage: all, subnet, or host.")),
				Bool("object/versioning", Path("object", "versioning"), NoSetter(), InSection("storage"), Doc("Versioning", "Keep historical versions of objects (object storage).")),
				Duration("streaming/ttl", Path("streaming", "ttl"), Field("Ttl"), Accessor("TTL"), NoSetter(), InSection("storage"), Doc("Time-To-Live", "Lifetime for streamed entries. A floor, not a guarantee.")),
				Bytes("size", Path(Either("object", "streaming"), "size"), Required(), InSection("storage"), Doc("Size", "Maximum storage size, as a human string (e.g. \"1GB\").")),
			),
			GroupDoc("Object or streaming storage, content-addressed, served to the project's peers."), Icon("bucket"), Category("data"),
			secIdentity, secAccess,
			Section("storage", "Storage", "Storage kind, key matching, and capacity."),
			Section("reach", "Reachability", "Which peers may reach this."),
			Ops(ReadOp("get"), ReadOp("list"), ReadOp("head"), WriteOp("put"), WriteOp("delete")),
			Resource("storages", "Storage", "Storage", "storage"),
		)),

	// A leased, quota'd local directory whose contents are continuously checkpointed
	// into the CAS as a snapshot chain. The chain IS the backup product.
	//
	// rpo-budget and on-budget-exceeded exist because RPO is NOT the checkpoint
	// interval — it is the age of the last fully uploaded, pointer-committed
	// snapshot, and it is unbounded while checkpoints cannot keep up with churn.
	// An unbounded RPO with no ceiling is silent data loss with a dashboard
	// (resources.md §2.4).
	DefineGroup("volumes",
		resource(
			commonAttributes(
				Bytes("size", Path("quota"), Required(), InSection("storage"), Doc("Size", "Hard quota for the directory, as a human string (e.g. \"10GB\").")),
				Duration("checkpoint/interval", Path("checkpoint", "interval"), Default("60s"), InSection("checkpoint"), Doc("Checkpoint Interval", "How often the volume is walked, chunked and uploaded.")),
				Duration("checkpoint/rpo-budget", Path("checkpoint", "rpo-budget"), InSection("checkpoint"), Doc("RPO Budget", "Ceiling on the age of the last committed snapshot before on-budget-exceeded fires.")),
				String("checkpoint/on-budget-exceeded", Path("checkpoint", "on-budget-exceeded"), InSet("alert", "freeze-writes"), Default("alert"), InSection("checkpoint"), Doc("On Budget Exceeded", "What to do when the committed-snapshot age passes rpo-budget.")),
			),
			GroupDoc("A single-writer leased directory with full POSIX semantics, checkpointed into the CAS. No RWX; bind mounts are refused."), Icon("drive"), Category("data"),
			secIdentity, secAccess,
			Section("storage", "Storage", "Capacity."),
			Section("checkpoint", "Checkpoint", "Durability out of band from the write path."),
			Ops(ReadOp("mount-ro"), WriteOp("mount-rw")),
			Resource("volumes", "Volume", "Volume", "volume"),
		)),

	// A declaration and a reference, never a value. The value lives outside git in
	// the secrets service, keyed <project>/<environment>/<key>, so rotation needs no
	// push. Read access is an edge with one verb; there is no write verb from a
	// workload, ever (resources.md §2.5, edges.md §3.3).
	DefineGroup("secrets",
		resource(
			commonAttributes(
				String("source", InSet("value", "oidc"), Default("value"), InSection("identity"), Doc("Source", "value: stored in the secrets service. oidc: minted per request as a short-lived token, nothing to rotate or leak.")),
				Duration("rotation", InSection("identity"), Doc("Rotation", "How often the value is expected to rotate. Advisory; rotation itself never requires a new release.")),
			),
			GroupDoc("A named reference to a secret value held outside git. The config never carries the value."), Icon("key"), Category("data"),
			secIdentity, secAccess,
			Ops(ReadOp("get")), // there is no write verb from a workload, ever
			Resource("secrets", "Secret", "Secret", "secret"),
		)),

	// ---- coordination ----

	// A named, durably-stored, single-writer compute instance: the fence is a quorum
	// lease and its epoch. CP, and the docs say so in the first paragraph — during a
	// partition the side that cannot reach K replicas cannot complete a write.
	// `shards` is fixed at creation because it is the lease and replica-set
	// granularity (resources.md §3.1).
	DefineGroup("entities",
		resource(
			wasmAttributes(
				Bool("durable", InSection("coordination"), Doc("Durable", "Journal every host call so an interrupted turn replays deterministically.")),
				Bytes("storage/size", Path("storage", "size"), InSection("limits"), Doc("Storage", "Per-instance SQL storage ceiling.")),
			),
			GroupDoc("A single-writer, durably-stored compute instance addressed by name. CP: a partition that cannot reach K replicas cannot write."), Icon("cube"), Category("compute"),
			secIdentity, secAccess, secCode, secLimits,
			Section("coordination", "Coordination", "Sharding and durability."),
			Ops(ReadOp("get"), WriteOp("call")),
			Resource("entities", "Entity", "Entity", "entity"),
		)),

	// Claim / visibility-timeout / ack / DLQ — not pop-and-delete. Today's
	// pkg/raft Queue.Pop deletes before returning, so a consumer that crashes
	// loses the job with no trace (resources.md §3.2).
	DefineGroup("queues",
		resource(
			commonAttributes(
				Duration("visibility-timeout", Default("30s"), Required(), InSection("delivery"), Doc("Visibility Timeout", "How long a claimed item stays invisible before it becomes deliverable again.")),
				Int("max-attempts", Default(5), InSection("delivery"), Doc("Max Attempts", "Deliveries before the item moves to the dead-letter queue.")),
				String("dead-letter", Ref("queues", Prefix("queues/")), InSection("delivery"), Doc("Dead Letter Queue", "Queue that receives items which exhausted max-attempts, as \"queues/<name>\".")),
			),
			GroupDoc("A global FIFO with claim, visibility timeout, retries and a dead-letter queue. Per-key ordering is an entity, not this."), Icon("list"), Category("data"),
			secIdentity, secAccess,
			Section("delivery", "Delivery", "Claim, retry and dead-letter behaviour."),
			Ops(ReadOp("receive"), WriteOp("send")),
			Resource("queues", "Queue", "Queue", "queue"),
		)),

	// ---- routing and naming ----

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
	DefineGroup("zones",
		resource(
			commonAttributes(
				String("fqdn", IsFqdn(), Required(), Field("Fqdn"), Accessor("FQDN"), EmitValidation("zone", "dns"), InSection("identity"), Doc("Zone", "The zone apex this cloud answers for, e.g. \"example.com\". Delegate it with NS records at your registrar.")),
				StringSlice("records", InSection("records"), Doc("Records", "Zone-file lines, e.g. \"www 300 IN A 1.2.3.4\", \"@ 3600 IN MX 10 mail.example.com.\".")),
			),
			GroupDoc("A DNS zone served authoritatively by this cloud, and the records you own in it."), Category("internet"), Icon("dns"),
			secIdentity, secAccess,
			Section("records", "Records", "Records this zone answers, in zone-file notation."),
			Resource("zones", "Zone", "Zone", "zone"),
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
	DefineGroup("gateways",
		resource(
			append(identityAttributes(),
				String("fqdn", IsFqdn(), Field("Fqdn"), Accessor("FQDN"), InSection("naming"), Doc("FQDN", "The name this gateway answers on. Omit it for a gateway that is only ever mounted beneath another one.")),
				String("certificate/source", Path("certificate", "source"), InSet("generated", "provided"), Default("generated"), Field("CertSource"), InSection("naming"), Doc("Certificate", "generated: issued on demand for this name. provided: supplied below.")),
				String("certificate/cert", Path("certificate", "cert"), RequiredWhen("certificate/source", "provided"), Field("CertFile"), InSection("naming"), ShowWhen("certificate/source", "provided"), Doc("Certificate PEM", "PEM-encoded certificate chain.")),
				String("certificate/key", Path("certificate", "key"), RequiredWhen("certificate/source", "provided"), Field("KeyFile"), InSection("naming"), ShowWhen("certificate/source", "provided"), Doc("Certificate Key", "PEM-encoded private key for the certificate.")),

				// The compiled keys are PATHS, not dashed names: TNS is a CRDT, so
				// `route/handler/namespace` is three mergeable segments while
				// `handler-namespace` is one opaque leaf that can only be replaced whole.
				String("route/path", Path("route", "path"), Required(), InSection("route"), Doc("Path", "The path served, e.g. \"/checkout\" or \"/\".")),
				String("route/match", Path("route", "match"), InSet("prefix", "exact", "regex"), Default("prefix"), InSection("route"), Doc("Match", "How the path is compared: prefix, exact, or regex.")),
				StringSlice("route/methods", Path("route", "methods"), InSection("route"), Doc("Methods", "HTTP methods answered. Omit for any. GET/HEAD/OPTIONS read, the rest write.")),

				// the switch: which kind handles it, and where that resource lives
				String("route/handler/kind", Path("route", "handler", Either(handlerKinds...)), Key(), Field("HandlerKind"), InSection("handler"), Doc("Handler", "Which kind serves this path (the key selects the handler block).")),
				String("route/handler/namespace", Path("route", "handler", Either(handlerKinds...), "namespace"), InSection("handler"), Doc("Namespace", "Path to the namespace holding the handler, e.g. \"/shop/eu\". Omitted means the project root.")),
				String("route/handler/name", Path("route", "handler", Either(handlerKinds...), "name"), Required(), IsVariableName(), InSection("handler"), Doc("Name", "The handler resource's name within that namespace.")),
				String("route/handler/port", Path("route", "handler", Either(handlerKinds...), "port"), IsVariableName(), InSection("handler"), Doc("Port", "Which of the handler's ports to reach, by name. A workload listening on more than one has to be told which, and the number is never written here — the platform assigns the external one, and there may be several instances of the workload on one host.")),

				StringSlice("cors/origins", Path("cors", "origins"), InSection("policy"), Doc("CORS Origins", "Origins allowed by CORS.")),
				StringSlice("cors/methods", Path("cors", "methods"), InSection("policy"), Doc("CORS Methods", "Methods allowed by CORS.")),
				Int("rate-limit/rpm", Path("rate-limit", "rpm"), InSection("policy"), Doc("Rate Limit", "Requests per minute.")),
				Int("rate-limit/burst", Path("rate-limit", "burst"), InSection("policy"), Doc("Burst", "Requests allowed above the sustained rate.")),
				String("rate-limit/key", Path("rate-limit", "key"), InSection("policy"), Doc("Rate Limit Key", "What the limit is counted per: \"ip\" or \"header:<name>\".")),
			),
			GroupDoc("A name, the policy at it, and what it serves."), Category("internet"), Icon("route"),
			secIdentity,
			Section("naming", "Naming", "The name served, and where its certificate comes from."),
			Section("route", "Route", "The path served and how it is matched."),
			Section("handler", "Handler", "What serves it."),
			Section("policy", "Policy", "CORS and rate limiting, at the name."),
			Resource("gateways", "Gateway", "Gateway", "gateway"),
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
	DefineGroup("proxies",
		resource(
			append(identityAttributes(),
				String("protocol", InSet("tcp", "udp", "sctp"), Default("tcp"), Required(), InSection("listener"),
					Doc("Protocol", "The transport carried. tcp is the transparent one; see this kind's docs for what a tunnel does to udp and sctp.")),
				String("fqdn", IsFqdn(), Required(), Field("Fqdn"), Accessor("FQDN"), EmitValidation("zone", "dns"), InSection("listener"),
					Doc("FQDN", "The name clients dial. It resolves to an allocated address — no port and no IP are written here, because both are assigned and published in DNS.")),
				String("service", IsVariableName(), InSection("listener"),
					Doc("Service", "IANA service name, e.g. \"postgres\". It builds the SRV label published beside this name, and gives clients that cannot do SRV lookups the conventional port to dial. Defaults to this resource's name.")),

				// Only `terminate` earns its keep: the proxy holds the certificate so a
				// plaintext workload can answer on a public name, reusing exactly the
				// gateway's cert machinery. There is no `passthrough` mode because there
				// is nothing to pass through to a decision — one proxy owns its address,
				// so nothing is multiplexed and no SNI is read.
				String("tls/mode", Path("tls", "mode"), InSet("none", "terminate"), Default("none"), Field("TLSMode"), InSection("listener"),
					Doc("TLS", "none: an opaque pipe, whatever the protocol does about TLS is its own business. terminate: the proxy answers TLS and hands plaintext to the workload.")),
				String("certificate/source", Path("certificate", "source"), InSet("generated", "provided"), RequiredWhen("tls/mode", "terminate"), Field("CertSource"), InSection("listener"), ShowWhen("tls/mode", "terminate"),
					Doc("Certificate", "generated: issued on demand for this name. provided: supplied below.")),
				String("certificate/cert", Path("certificate", "cert"), RequiredWhen("certificate/source", "provided"), Field("CertFile"), InSection("listener"), ShowWhen("certificate/source", "provided"),
					Doc("Certificate PEM", "PEM-encoded certificate chain.")),
				String("certificate/key", Path("certificate", "key"), RequiredWhen("certificate/source", "provided"), Field("KeyFile"), InSection("listener"), ShowWhen("certificate/source", "provided"),
					Doc("Certificate Key", "PEM-encoded private key for the certificate.")),

				// Same shape as a gateway route's handler, one kind wide.
				String("handler/kind", Path("handler", Either(proxyHandlerKinds...)), Key(), Field("HandlerKind"), InSection("handler"),
					Doc("Handler", "Which kind sits behind this proxy (the key selects the handler block).")),
				String("handler/namespace", Path("handler", Either(proxyHandlerKinds...), "namespace"), IsNamespacePath(), InSection("handler"),
					Doc("Namespace", "Path to the namespace holding the workload, e.g. \"/shop/eu\". Omitted means the project root.")),
				String("handler/name", Path("handler", Either(proxyHandlerKinds...), "name"), Required(), IsVariableName(), InSection("handler"),
					Doc("Name", "The workload's name within that namespace.")),
				String("handler/port", Path("handler", Either(proxyHandlerKinds...), "port"), IsVariableName(), InSection("handler"),
					Doc("Port", "Which of the workload's ports to stream to, by name. The number stays where the workload declared it.")),
			),
			GroupDoc("An allocated address that accepts a raw connection and streams it to a workload over p2p. For everything a gateway cannot parse."), Category("internet"), Icon("plug"),
			secIdentity,
			Section("listener", "Listener", "The protocol carried, the name it is reached by, and whether TLS stops here."),
			Section("handler", "Handler", "The workload on the other end."),
			Resource("proxies", "Proxy", "Proxy", "proxy"),
		)),

	// Both halves are declared now: the channel edges to its consumer with
	// `subscribe`, the producer edges back with `publish`. Two statements because
	// they are two capabilities — collapsing them into `write` erases a real
	// boundary, and today the publish half is not checked at all (resources.md §5.3).
	DefineGroup("messaging",
		resource(
			commonAttributes(
				Bool("local", InSection("channel"), Doc("Local", "Restrict the channel to the local node / project scope.")),
				String("channel/match", Path("channel", "match"), Required(), Field("Match"), Accessor("ChannelMatch"), NoSetter(), InSection("channel"), Doc("Match", "Channel name or pattern this resource matches.")),
				Bool("channel/regex", Path("channel", "regex"), NoSetter(), InSection("channel"), Doc("Regex", "Treat match as a regular expression.")),
				Bool("bridges/mqtt/enable", Path("bridges", "mqtt", "enable"), Accessor("MQTT"), NoSetter(), InSection("bridges"), Doc("MQTT", "Expose this channel over the MQTT bridge.")),
				Bool("bridges/websocket/enable", Path("bridges", "websocket", "enable"), Accessor("WebSocket"), NoSetter(), InSection("bridges"), Doc("WebSocket", "Expose this channel over the WebSocket bridge.")),
			),
			GroupDoc("A PubSub channel, optionally bridged to MQTT/WebSocket. It edges to its subscribers; publishers edge to it."), Icon("message"), Category("trigger"),
			secIdentity, secAccess,
			Section("channel", "Channel", "Channel matching."),
			Section("bridges", "Bridges", "External protocol bridges."),
			Ops(ReadOp("subscribe"), WriteOp("publish")), // never collapse these into "write"
			Resource("messaging", "Messaging", "Messaging", "messaging"),
		)),

	// Thin on purpose: the tools are already functions. Stateless Streamable HTTP,
	// POST only, no sessions. It compiles to route-table rows that additionally
	// predicate on the Mcp-Method / Mcp-Name headers the 2026-07-28 spec mirrors out
	// of the body — routing never reads the JSON-RPC body. The protocol-version
	// guard is a MUST and comes before the router, or header routing trusts
	// attacker-controlled headers (resources.md §5.4).
	DefineGroup("mcp",
		resource(
			commonAttributes(
				String("auth", InSet("none", "oauth"), Default("none"), InSection("identity"), Doc("Auth", "none, or oauth to validate tokens minted by a named authorization server. tau is a resource server only; it never issues.")),
				String("tools/cache-scope", Path("tools", "cache-scope"), InSet("none", "public", "principal"), Default("none"), InSection("tools"), Doc("Cache Scope", "How widely tools/list may be cached. public is cacheable across the whole swarm.")),
				Duration("tools/cache-ttl", Path("tools", "cache-ttl"), InSection("tools"), Doc("Cache TTL", "How long a cached tools/list stays valid.")),
			),
			GroupDoc("An MCP server grouping functions as tools. Stateless Streamable HTTP; subscriptions are declined in v1."), Icon("plug"), Category("internet"),
			secIdentity, secAccess,
			Section("tools", "Tools", "tools/list caching."),
			Resource("mcp", "MCP", "MCP", "mcp"),
		)),

	// One type for every external system: it holds the credentials, and for
	// source-like externals it holds the connection too — Lambda's event-source
	// mapping, where a separate poller joins the source and the invoked compute
	// never touches it. A connector that holds a live connection runs exactly once
	// per cloud: a quorum lease with an epoch on every event it emits, so a stale
	// poller's events are rejected rather than duplicated (resources.md §6).
	DefineGroup("connectors",
		resource(
			commonAttributes(
				String("type", Required(), InSection("identity"), Doc("Type", "Which external system this connects to, e.g. \"kafka\", \"s3\", \"slack\".")),
				String("credentials", Ref("secrets", Prefix("secrets/")), InSection("identity"), Doc("Credentials", "Secret holding this connector's credentials, as \"secrets/<name>\".")),
				Bool("polls", InSection("identity"), Doc("Polls", "This connector holds a live connection to the external system and emits events from it. Exactly one node in the cloud runs it.")),
			),
			GroupDoc("An external system: credentials, and for source-like externals the held connection. Events are delivered as CloudEvents over tau pubsub."), Icon("cable"), Category("trigger"),
			secIdentity, secAccess,
			Resource("connectors", "Connector", "Connector", "connector"),
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
func namespacesGroup(depth int) *Node {
	children := append([]*Node{}, V2Resources...)
	if depth > 1 {
		children = append(children, namespacesGroup(depth-1))
	}
	return DefineGroup("namespaces",
		DefineIterGroup(identityAttributes(), children...).
			With(Singular("Namespace"),
				GroupDoc("A named scope holding resources, and namespaces, of its own. Names resolve here first, then in the parent."),
				Icon("apps")))
}

var _ = func() any { attachAccess(); return nil }()

var v2Root = Root(
	rootAttributes(),
	append(append([]*Node{}, V2Resources...), namespacesGroup(MaxNamespaceDepth))...,
).With(Singular("Project"))

var TaubyteProject = SchemaDefinition(v2Root)

// GenerationRoot is the node list a generator walks: the resource groups plus the
// applications container, read off the real root so no curated list can drift.
func GenerationRoot() []*Node { return v2Root.Children }

// CompileRoot is the interpreter's entrypoint into the DSL: the whole project root.
func CompileRoot() *Node { return v2Root }

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
