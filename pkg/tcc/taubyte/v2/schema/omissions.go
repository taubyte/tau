package schema

// What is NOT in the vocabulary, and why.
//
// A closed enum only works if the closure is legible. Every kind left out was left
// out for a reason, and a reason that lives in a code comment is a reason nobody
// reading the config can find — so the omissions are declared here, beside the
// vocabulary, and served through the same surface the kinds are (see wasm/main.go).
//
// Three states, and the difference matters:
//
//	Never    — refused. Naming it now is what stops it being proposed again.
//	Blocked  — intended, but nothing to author yet. It returns when Unblocks is done.
//	Open     — undecided. What is built today is a default, not an answer.
type OmissionState string

const (
	Never   OmissionState = "never"
	Blocked OmissionState = "blocked"
	Open    OmissionState = "open"
)

// Who decided, which is a separate axis from the state and is the one that was
// missing. A technical constraint decides itself — a list of objects has no
// representation in the DSL, and no amount of preference changes that. A NAME, or
// whether a capability is worth its support cost, is a product call, and this
// register was stating those as settled fact with a rationale under them. Anything
// marked Default is the schema's current behaviour and nothing more.
type Decider string

const (
	ByOwner   Decider = "owner"   // decided, and not by this package
	ByDefault Decider = "default" // what the schema does today, pending a call
	Forced    Decider = "forced"  // the DSL or the runtime leaves no choice
)

type Omission struct {
	Name     string
	State    OmissionState
	Decided  Decider
	Why      string
	Instead  string // what to use in its place today, if anything
	Unblocks string // what has to be true for it to exist (Blocked / Open only)
}

var Omissions = []Omission{
	{
		Name:    "an external port, on a workload or on a proxy",
		State:   Never,
		Decided: ByOwner,
		Why: "A workload declares the ports it binds INSIDE its container, by name, because its " +
			"own code has to agree with those numbers. The port anything outside reaches it on is " +
			"assigned by the platform and tunnelled over a p2p stream — so writing it here would " +
			"be a number the workload is told to ignore, and a second place for the real one to " +
			"disagree with it. It is also the only value in this vocabulary that would collide " +
			"ACROSS projects: two of them wanting 5432 on one edge address is not an error either " +
			"project's schema can see.",
		Instead: "a named port on the workload, referred to by name from a gateway route, a proxy " +
			"or a health probe. At L4, where the port IS the address, the platform allocates an " +
			"address per proxy and publishes A/AAAA plus an SRV record, so clients are told rather " +
			"than configured.",
	},
	{
		Name:    "a reference that requires a particular KIND of target",
		State:   Open,
		Decided: Forced,
		Why: "Ref() resolves a name to an id and stops there. So a database's engine can name a " +
			"connector to Kafka and compile clean, and the resulting config claims a SQL engine that " +
			"answers no query. `connector.type` cannot be closed to fix it — a connector is one of " +
			"the two seams where this vocabulary stays extensible, and a new external system must " +
			"not require editing an enum.",
		Instead: "`provides` on the connector: `type` stays open, and the ROLE it can play — " +
			"database, stream, object-store, endpoint — is a closed set the rest of the vocabulary " +
			"can reach for. That makes the config self-describing and the check writable; it does " +
			"not make it checked.",
		Unblocks: "the publish-time pass. Every case is the same shape — read the target and " +
			"compare one field — and it is the fourth thing waiting on that pass.",
	},
	{
		Name:    "reaching two resources that share a name in different namespaces",
		State:   Open,
		Decided: Forced,
		Why: "An access entry is KEYED by the target's name, and a yaml mapping cannot hold the " +
			"same key twice. So a function in /shop can reach its own `sessions` cache or eu's, " +
			"never both — `caches: { sessions: …, sessions: { namespace: eu } }` is not a document " +
			"that parses. The keyed shape is what made per-target fields possible (ops, timeout, " +
			"deliver) and it is worth more than this case costs, but the limit is real and it is " +
			"invisible until someone hits it: this repo's own fixture claimed to reach both for " +
			"weeks, in its description, while declaring one.",
		Instead: "name the two resources differently, or reach the far one through a function in " +
			"its own namespace.",
		Unblocks: "an authored key that is not the target's name — an arbitrary label with `name` " +
			"as a field under it. That is a bigger change than it looks: the key is what every " +
			"consumer reads a target from today.",
	},
	{
		Name:    "spelling out a generated name inside the config",
		State:   Open,
		Decided: Forced,
		Why: "A host under the cloud's own zone is `<label>.<project label>.<cloud fqdn>`. The " +
			"project label is derived from the project id and IS computable here; the cloud's " +
			"own fqdn is not, and never will be — it belongs to the deployment. So any field " +
			"that needs the FULL name cannot hold it. `cors.origins` is the live case: it takes " +
			"literal origins, and an author cannot write an origin they will not know until the " +
			"project is deployed. Today that field is only usable with a zone you own.",
		Instead: "`cors.allow-self`, which says \"the origin this gateway answers on\" without " +
			"naming it. A literal origin for some OTHER generated host is still unwritable.",
		Unblocks: "origins expressed as references to a host rather than as strings, resolved the " +
			"same way every other reference is.",
	},
	{
		Name:    "a mutual exclusion the DSL cannot state",
		State:   Open,
		Decided: Forced,
		Why: "A variables entry carries a literal `value` or a `secret` reference, and nothing in " +
			"the schema can say \"exactly one of these\" — a mutual exclusion has no representation " +
			"here. The same is true of `path`, which is required only when the entry is delivered " +
			"as a file.",
		Instead: "named values only, and a publish-time check for the exclusion — the fifth thing " +
			"that pass owes.",
		Unblocks: "an `as: file` with a path when something needs one, which is additive.",
	},
	{
		Name:    "SNI routing / multiplexing several proxies onto one address",
		State:   Never,
		Decided: Forced,
		Why: "A proxy owns its address, so there is nothing to multiplex and no decision to route " +
			"on. SNI only earns its keep when several services share a port, which is the shape " +
			"chosen to avoid: it works solely for TLS-speaking clients, and everything the " +
			"vocabulary would gain is already bought by allocating an address.",
		Instead: "one address per proxy. `tls.mode: terminate` is there when the proxy should hold " +
			"the certificate; `none` is an opaque pipe, which is the right answer for a protocol " +
			"like Postgres that negotiates TLS inside the stream.",
	},
	{
		Name:    "a transparency guarantee for tunnelled UDP and SCTP",
		State:   Open,
		Decided: Forced,
		Why: "Both are in the `protocol` enum and neither survives a reliable ordered stream " +
			"unchanged. Tunnelled UDP turns packet loss into a latency spike and inherits " +
			"head-of-line blocking — it works, but not the way an application that chose UDP was " +
			"written against. SCTP is worse: multi-streaming and multi-homing are the whole reason " +
			"to pick it, and a tunnel keeps neither. This is a runtime property with no config " +
			"expression, so the schema cannot warn about it at the point it matters.",
		Instead: "tcp, which is transparent. The enum still carries all three, because which " +
			"protocols the runtime actually serves is the runtime's statement to make.",
		Unblocks: "unreliable datagrams over QUIC (RFC 9221) for the UDP path, which would make it " +
			"honest instead of merely working.",
	},
	{
		Name:    "HTTP methods as a kind's ops",
		State:   Never,
		Decided: ByOwner,
		Why: "A method belongs to a gateway's route, which is where a request enters and where " +
			"the method is matched. A workload that also carried GET as an op would be nameable " +
			"as a direct target — a second door into it that no route knows about, with no path, " +
			"no gateway policy and no name in front of it.",
		Instead: "route.methods on the gateway. Ops are for what one resource does to another " +
			"inside the cloud: invoke, get, put, publish.",
	},
	{
		Name:    "replicas / replication factor / instance count",
		State:   Never,
		Decided: ByOwner,
		Why: "How many copies run, and where, is the platform's job: it places, it reconciles, " +
			"and it re-places when a node dies. An authored number is a knob the system then has " +
			"to either obey while it is wrong or override while it is authored, and either way a " +
			"human is in the failure path — which is the one thing the design does not allow.",
		Instead: "nothing to write. Placement is derived from the fleet and from what the resource " +
			"needs; a database's durability comes from the quorum it is acknowledged by, not from " +
			"a count in git.",
	},
	{
		Name:    "workflows",
		State:   Open,
		Decided: ByDefault,
		Why: "Two claims were run together here, and only one of them is this package's to make. " +
			"FORCED: a workflow engine built beside entities is a second fence over the same " +
			"state, and the mechanism — epoch, journal, timer, suspend — is most of the entity's " +
			"already. PRODUCT, and not decided here: whether a workflow-shaped surface ships at " +
			"all. Plenty of buyers shop for that noun; `durable: true` on an entity is a different " +
			"sale even when it is the same machinery.",
		Instead: "entities with `durable: true` — the epoch fence, journal and timer are the entity's own.",
		Unblocks: "an owner call on the surface. If it ships, it is a library and a CLI over entities, " +
			"never a second execution engine and never a step-function language.",
	},
	{
		Name:    "domains",
		State:   Never,
		Decided: ByDefault,
		Why: "A name with nothing on it but a certificate, referenced from the thing that actually " +
			"served it. Once ingress became an inbound use, the name and the routing under it are " +
			"one statement — and a gateway already had to be nameable, because its mount is a " +
			"confinement.",
		Instead: "gateways: `fqdn` is optional there, so a named gateway is reachable from the " +
			"internet and an unnamed one is reachable only by being mounted inside another.",
		Unblocks: "confirmation. The collapse is a judgement about how many kinds a name is worth, " +
			"and it is reversible: splitting it back out is a group and a ref.",
	},
	{
		Name:    "per-name record fields",
		State:   Blocked,
		Decided: Forced,
		Why: "A zone's records are authored as zone-file lines rather than as objects with a " +
			"name, type, TTL and value, because when they were written a list of objects had no " +
			"representation in the DSL. Zone-file notation is the format every provider imports " +
			"and exports, so nothing is unexpressible — but the compiler cannot validate a record " +
			"field by field, complete a type, or point at the line that is wrong.",
		Instead: "zone/<name>.records, one zone-file line per entry.",
		Unblocks: "nothing any more — DefineList arrived with the transformer chain. What is left " +
			"is the modelling: one element per record type, or one with a type key.",
	},
	{
		Name:    "smartops",
		State:   Never,
		Decided: ByOwner,
		Why: "A first-class resource with a full schema, a build pipeline, indexing and a host-ABI " +
			"plugin, whose nine call sites all returned nothing. It was policy that never ran.",
		Instead: "authorizers for policy, evaluated per request with the operation in hand; " +
			"platform-enforced quotas at dispatch for limits, which a guest cannot bypass.",
	},
	{
		Name:    "counters",
		State:   Never,
		Decided: ByOwner,
		Why: "A substrate component, not a resource. Promoting it to the vocabulary would ship a " +
			"user-facing type over a CRDT that cannot give a linearizable increment.",
		Instead: "entities for a counter anyone reads back; metering is a property of every resource, not a kind.",
	},
	{
		Name:    "https (as a trigger type)",
		State:   Never,
		Decided: ByOwner,
		Why: "It encoded a decision already made for the user — the substrate redirects plaintext " +
			"anyway — and with the trigger block deleted there is nothing left to collapse.",
		Instead: "the gateway that serves the name, which is where TLS was always decided.",
	},
	{
		Name:    "a route as a named object",
		State:   Blocked,
		Decided: Forced,
		Why: "A gateway's routes are per-kind sub-blocks of parallel lists — paths, methods, " +
			"targets — because when they were written the DSL had no list of objects. Within one " +
			"kind the pairing is POSITIONAL, so two paths to two different functions line up by " +
			"index and nothing enforces that the lists are the same length.",
		Instead: "one route per gateway, which is what v2 ships.",
		Unblocks: "nothing any more — DefineList arrived with the transformer chain. Whether a " +
			"gateway should carry several routes is a modelling question, not a missing shape.",
	},
	{
		Name:    "compile-time resolution of an access target",
		State:   Open,
		Decided: Forced,
		Why: "An access entry names its target as a map KEY — `caches: { sessions: { ops: [get] } }` " +
			"— and a key is not a reference, so nothing resolves it. A typo in a target name is " +
			"not a compile error the way `source: modules/typo` is, and the compiled object carries " +
			"a name rather than an id. It is the price of naming the target once with its ops and " +
			"its namespace beside it; the verb-keyed list it replaced resolved to ids but could not " +
			"name another namespace at all, which is worse.",
		Instead:  "the target is (namespace, name), resolved at publish, like a gateway handler.",
		Unblocks: "a reference that can resolve a KEY rather than a value, or a publish-time check.",
	},
	{
		Name:    "authorisation of a cross-namespace access",
		State:   Open,
		Decided: ByDefault,
		Why: "`namespace: /billing` is grammar for naming a sibling, which v1 did not have — " +
			"isolation used to hold because there was no way to write it down. Now there is, and " +
			"nothing decides whether it is allowed: the connection between two scopes and the " +
			"target-side grant that is meant to bound it are control-plane objects that do not " +
			"exist.",
		Instead:  "it compiles; the check has to live at publish or at runtime.",
		Unblocks: "the connection table and the grant, which are not tcc's to hold.",
	},
	{
		Name:    "resolved references below the first namespace level",
		State:   Blocked,
		Decided: Forced,
		Why: "Namespaces nest, but the interpreter's scope walk covers the project root and ONE " +
			"level of the container, so a reference written inside a namespace below the first " +
			"level compiles to a NAME rather than an id. It is not an error and nothing reports " +
			"it, which is the part that matters: the config compiles and the reference is simply " +
			"never resolved.",
		Instead:  "keep references at or above the first namespace level, where they resolve.",
		Unblocks: "a scope walk that descends into every container and every level, rather than the first of each.",
	},
	{
		Name:    "traffic splitting",
		State:   Blocked,
		Decided: Forced,
		Why: "The Gateway API gives each backendRef a `weight`, which is how every canary and " +
			"blue/green rollout in the category is expressed. A route here targets a list of names " +
			"with no per-target field, so there is nowhere to put the weight.",
		Instead:  "nothing. A route sends everything it matches to its target.",
		Unblocks: "a per-target field on a use, which nesting has since made possible.",
	},
	{
		Name:    "authenticated MCP",
		State:   Blocked,
		Decided: Forced,
		Why: "`auth: none | oauth` was declared with nowhere to name the authorization server " +
			"whose tokens it claimed to validate — no issuer, no audience, no keys — so the value " +
			"promised a protection nothing could perform. Deleted rather than completed, because " +
			"the prerequisite is blocked too: an authenticated MCP is routed on the Mcp-Method and " +
			"Mcp-Name headers, and this vocabulary cannot match on a header.",
		Instead: "unauthenticated MCP. A gateway in front of it carries what policy there is.",
		Unblocks: "a resource-server runtime, plus header matching. The shape it returns as, so " +
			"this is a list entry and not a redesign: an `auth` block whose oauth branch requires " +
			"ONE field, `issuer` — an https authorization-server identifier — with `scopes` " +
			"optional. The AUDIENCE is never authored: it is the canonical URL this server is " +
			"reached at, which ends in the cloud's fqdn and so cannot be written here (the same " +
			"reason cors origins cannot be); the platform derives it from the gateway route that " +
			"serves it. `jwks-uri` is not authored either — it is discovered from the issuer's " +
			"own metadata, and a second copy of it in config is a key source that can go stale.",
	},
	{
		Name:    "header and query matching",
		State:   Blocked,
		Decided: Forced,
		Why: "The Gateway API matches on headers and query params alongside the path, and the MCP " +
			"transport depends on exactly that — it mirrors JSON-RPC fields into `Mcp-Method` and " +
			"`Mcp-Name` headers so an intermediary can route without parsing the body. A match is " +
			"a list of objects, and a route here carries one path and one match type.",
		Instead: "path and method only.",
		Unblocks: "nothing any more — DefineList arrived with the transformer chain, so a match " +
			"list is now expressible. Until it is modelled, an authenticated MCP server cannot " +
			"be routed by header.",
	},
	{
		Name:    "per-resource route rules",
		State:   Blocked,
		Decided: Forced,
		Why: "Per-path headers, redirects, rewrites and status overrides are lists of objects, and " +
			"when this was written the DSL's shapes stopped at a list of strings. Four importers " +
			"depend on exactly those four with per-path granularity; without them a static-site " +
			"import changes the site's behaviour rather than moving it.",
		Instead: "gateways carry CORS and rate limiting, which are scalars and lists of strings.",
		Unblocks: "nothing any more — DefineList arrived with the transformer chain. The four " +
			"rule kinds still have to be modelled and given a runtime.",
	},
	{
		Name:    "authorizers",
		State:   Never,
		Decided: ByOwner,
		Why: "It was declared with exactly a function's fields and nothing else. What makes a " +
			"function an authorizer is the host interface it is built against — it is handed the " +
			"operation and answers yes or no — and that is a property of the artifact, not of the " +
			"config. A kind that adds no field is a second name for the same thing.",
		Instead: "a function, named by an access entry's `via`.",
	},
	{
		Name:    "services (p2p)",
		State:   Open,
		Decided: ByOwner,
		Why: "Carried forward from v1 without being re-thought: a `protocol` string and a single " +
			"`call` op, where the notes say a p2p service's own declared commands are its verbs — " +
			"which is per-instance data, not a fixed op set. p2p gets a shape of its own; it does " +
			"not have one yet.",
		Instead:  "nothing. A p2p service cannot be declared in v2 today.",
		Unblocks: "a decision on the shape.",
	},
	{
		Name:    "traffic transformers on a route",
		State:   Open,
		Decided: ByDefault,
		Why: "A gateway can deny (an authorizer) but cannot TRANSFORM: strip a prefix, rewrite a " +
			"path, set a header, compress, redirect, retry, break a circuit. Traefik models these " +
			"as named middlewares with a type and per-type settings, chained in order on the " +
			"router, and that shape now fits — a type is an Either switch and a chain is an " +
			"ordered list of references. What is undecided is the set: shipping half of " +
			"Traefik's two dozen is worse than shipping a named few, and which few is a product " +
			"call.",
		Instead:  "nothing. A route matches and dispatches; it does not touch the request.",
		Unblocks: "a decision on the set, then one kind with a type switch.",
	},
	{
		Name:    "validation of a resource's own name",
		State:   Blocked,
		Decided: Forced,
		Why: "A resource's name comes from its FILE name, and the compile driver sets it without " +
			"checking — so `paid-tier.yaml` yields a name that IsVariableName() would reject, " +
			"while a reference written to it is checked and fails. The two ends disagree.",
		Instead:  "keep resource file names to variable names by convention.",
		Unblocks: "the driver validating the name it derives, which is not this package's to change.",
	},
	{
		Name:    "mount and event filters on a use",
		State:   Blocked,
		Decided: Forced,
		Why: "An access entry now carries `via` and `timeout`, which nesting made possible — " +
			"there is a per-target place to put them. What still has nowhere to go is `mount`, " +
			"which belongs to a route rather than to a use, and CloudEvents `filters`, which is " +
			"a list of attribute matches.",
		Instead: "via and timeout on the entry; a gateway's route carries the path.",
		Unblocks: "nothing any more for the filters half — DefineList arrived with the transformer " +
			"chain, so a filter list is expressible and only unmodelled.",
	},
	{
		Name:    "shards / replica-set granularity on an entity",
		State:   Never,
		Decided: ByOwner,
		Why: "Same family as a replica count: how the key space is partitioned across the fleet " +
			"is the platform's, and an authored number is one it must then either obey while it " +
			"is wrong or override while it is authored.",
		Instead: "nothing to write. The lease and its epoch are the fence; granularity follows the fleet.",
	},
	{
		Name:    "cross-scope addressing",
		State:   Open,
		Decided: Forced,
		Why: "A use names a resource in the authoring scope, walking up to the project root. There " +
			"is no way to write down a sibling namespace's resource or another project's — which " +
			"is why isolation currently holds by absence of grammar rather than by a check.",
		Instead:  "nothing. A sibling's resource simply does not resolve, and the compile fails.",
		Unblocks: "the address grammar, and with it the connection and target grant a cross-scope use is meant to require.",
	},
}
