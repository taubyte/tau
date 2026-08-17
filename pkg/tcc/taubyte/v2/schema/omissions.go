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
			"name, type, TTL and value, because a list of objects has no representation in the " +
			"DSL. Zone-file notation is the format every provider imports and exports, so nothing " +
			"is unexpressible — but the compiler cannot validate a record field by field, complete " +
			"a type, or point at the line that is wrong.",
		Instead:  "zones/<name>.records, one zone-file line per entry.",
		Unblocks: "the same missing shape as the route rules: an attribute that can hold a list of objects.",
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
			"targets — because the DSL has no list-of-objects and no arbitrary-keyed map under a " +
			"resource. Within one kind the pairing is POSITIONAL, so two paths to two different " +
			"functions line up by index and nothing enforces that the lists are the same length.",
		Instead:  "routes.<kind>.{paths, methods, to} on the gateway.",
		Unblocks: "an attribute type that can hold a list of objects.",
	},
	{
		Name:    "compile-time resolution of an access target",
		State:   Open,
		Decided: Forced,
		Why: "An access entry names its target as a map KEY — `caches: { sessions: { ops: [get] } }` " +
			"— and a key is not a reference, so nothing resolves it. A typo in a target name is " +
			"not a compile error the way `source: module/typo` is, and the compiled object carries " +
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
		Unblocks: "a per-target field on a use, which is the same missing shape as the route rules.",
	},
	{
		Name:    "header and query matching",
		State:   Blocked,
		Decided: Forced,
		Why: "The Gateway API matches on headers and query params alongside the path, and the MCP " +
			"transport depends on exactly that — it mirrors JSON-RPC fields into `Mcp-Method` and " +
			"`Mcp-Name` headers so an intermediary can route without parsing the body. A match is " +
			"a list of objects, and a route here carries one path and one match type.",
		Instead:  "path and method only.",
		Unblocks: "a list-of-objects attribute; until then an authenticated MCP server cannot be routed by header.",
	},
	{
		Name:    "per-resource route rules",
		State:   Blocked,
		Decided: Forced,
		Why: "Per-path headers, redirects, rewrites and status overrides are lists of objects, and " +
			"the DSL's attribute types stop at a list of strings. Four importers depend on exactly " +
			"those four with per-path granularity; without them a static-site import changes the " +
			"site's behaviour rather than moving it.",
		Instead:  "gateways carry CORS and rate limiting, which are scalars and lists of strings.",
		Unblocks: "an attribute type that can hold a list of objects, or a nested group under a resource.",
	},
	{
		Name:    "mount and event filters on a use",
		State:   Blocked,
		Decided: Forced,
		Why: "An access entry now carries `via` and `timeout`, which nesting made possible — " +
			"there is a per-target place to put them. What still has nowhere to go is `mount`, " +
			"which belongs to a route rather than to a use, and CloudEvents `filters`, which is " +
			"a list of attribute matches.",
		Instead:  "via and timeout on the entry; a gateway's route carries the path.",
		Unblocks: "a list-of-objects attribute, for the filters half.",
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
