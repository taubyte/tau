package schema

import (
	"github.com/taubyte/tau/core/common/repositorytype"
	"github.com/taubyte/tau/pkg/tcc/interp"
)

// sourceShape is the introspectable shape for a function/smartop `source`: it must
// be either "." (inline code) or a "libraries/<name>" reference. Declared as data
// (StringShape) rather than a hand-written closure, so it both validates at load
// (with file:line context) and serializes to the exported JSON Schema. The
// driver's ResolveRefs then resolves the "libraries/<name>" arm to "libraries/<id>".
var sourceShape = StringShape([]string{"."}, []string{"libraries/"})

// TaubyteAttributes prepends the shared identity block (id/name/description/tags)
// to a resource's own attributes, so every resource — and the JSON schema — leads
// with those fields. Order is authoring/UI order only; it does not affect parsing
// or the generated structs (which always emit the common block first regardless).
func TaubyteAttributes(attrs ...*Attribute) []*Attribute {
	return append([]*Attribute{
		String("id", IsCID(), Required(), Section("identity"), Doc("Content-addressed identifier (CID) of this resource. Stable across renames.")),
		String("name", IsVariableName(), Section("identity"), Doc("Unique resource name within its project or application. Must be a valid variable name.")),
		String("description", Section("identity"), Doc("Free-form, human-readable description of this resource.")),
		StringSlice("tags", Section("identity"), Doc("Arbitrary labels for organizing and filtering this resource.")),
	}, attrs...)
}

// secIdentity is the shared "Identity" display section every resource carries (its
// id/name/description/tags). Declared once, applied per resource. Schema-only.
var secIdentity = SectionDefinition("identity", "Identity", "Resource identity and metadata.")

// taubyteRootAttributes is the project-root attribute list. It mirrors the shared
// TaubyteAttributes block (email is project-only) but carries two project-scope
// driver annotations the CompileDriver reads and no resource has: the project id
// emits a deferred `project_id` validation, and the root `tags` list is dropped
// from the compiled wire object. These must NOT live on the shared
// TaubyteAttributes — resources keep their tags and never emit an id validation.
func taubyteRootAttributes() []*Attribute {
	return []*Attribute{
		String("email", Path("notification", "email"), IsEmail(), Doc("Contact email for project notifications.")),
		String("id", IsCID(), Required(), EmitValidation("project_id", "project_id"), Doc("Content-addressed identifier (CID) of the project.")),
		String("name", IsVariableName(), Doc("Project name. Must be a valid variable name.")),
		String("description", Doc("Free-form, human-readable description of the project.")),
		StringSlice("tags", WireDrop(), Doc("Project-level labels.")),
	}
}

var TaubyteRessources = []*Node{
	DefineGroup("databases",
		DefineIter( //use name as "name"?
			TaubyteAttributes(
				String("match", Section("storage"), Doc("Key or key-pattern this database serves (a literal prefix, or a regex when useRegex is set).")),
				Bool("useRegex", Path("regex"), Compat("useRegex"), Section("storage"), Doc("Treat match as a regular expression instead of a literal prefix.")),
				String("network-access", Path("access", "network"), InSet("all", "subnet", "host"), Default("all"), EnumBool("Local", []string{"host"}, []string{"all", "host"}, [2]string{"host", "all"}), NoAccessors(), Section("storage"), Doc("Which peers may reach this database: all, subnet (project peers only), or host (local node only).")),
				Bytes("size", Path("storage", "size"), Section("storage"), Doc("Maximum storage size, as a human string (e.g. \"32MB\", \"1GB\").")),
				String("encryption-type", Path("encryption", "type"), NoAccessors(), NoStructField(), Doc("Encryption scheme for data at rest.")),
				String("encryption-key", Path("encryption", "key"), NoAccessors(), Section("encryption"), Doc("Key material or reference used to encrypt data at rest.")),
			),
			GroupDoc("A key-value database served to the project's peers."),
			secIdentity,
			SectionDefinition("storage", "Storage", "Key matching, capacity, and reachability."),
			SectionDefinition("encryption", "Encryption", "Encryption at rest."),
			Addressing(HasBasicPath, HasIndex, HasIndexPath),
			Embeds("Basic", "Indexer"),
			Resource("databases", "Database", "Database", "database"),
			interp.IndexByName(HasIndexPath),
		)),
	DefineGroup("domains",
		DefineIter(
			TaubyteAttributes(
				String("fqdn", IsFqdn(), Field("Fqdn"), Accessor("FQDN"), NoGetter(), EmitValidation("domain", "dns"), Section("identity"), Doc("Fully-qualified domain name this resource represents.")),
				String("certificate-data", Path("certificate", "cert"), Field("CertFile"), Tag("cert-file"), Section("tls"), ShowWhen("certificate-type", "inline"), Doc("PEM-encoded TLS certificate for the domain (inline certificate-type).")),
				String("certificate-key", Path("certificate", "key"), Field("KeyFile"), Tag("key-file"), Section("tls"), ShowWhen("certificate-type", "inline"), Doc("PEM-encoded private key for the certificate (inline certificate-type).")),
				String("certificate-type", Path("certificate", "type"), InSet("inline", "auto"), Default(""), Field("CertType"), Tag("cert-type"), Section("tls"), Doc("How the TLS certificate is provisioned: inline (supplied here) or auto (managed).")),
			),
			GroupDoc("A DNS domain and its TLS configuration, referenced by functions and websites."),
			secIdentity,
			SectionDefinition("tls", "TLS", "Certificate configuration."),
			// domain's BasicPath is bespoke (fqdn-reversed), so it's not tagged here.
			Addressing(HasIndex),
			Embeds("Indexer"),
			Resource("domains", "Domain", "Domain", "domain"),
			interp.IndexPlaceholder("fqdn"),
		)),
	DefineGroup("functions",
		DefineIter(
			TaubyteAttributes(
				String("type", Path("trigger", "type"), InSet("http", "https", "pubsub", "p2p"), DerivedBool("Secure", map[string]bool{"http": false, "https": true}, map[bool]string{false: "http", true: "https"}), Section("trigger"), Doc("Trigger that invokes the function: http, https, pubsub, or p2p.")),
				Bool("local", Path("trigger", "local"), Section("trigger"), Doc("Restrict the trigger to the local node / project scope.")),
				String("pubsub-channel", Path("trigger", "channel"), Tag("channel"), Section("pubsub"), Doc("PubSub channel the function subscribes to (pubsub trigger).")),
				String("p2p-protocol", Path("trigger", "protocol"), Compat("trigger", "service"), Tag("service"), OnlyWhen("type", "p2p"), Default(""), Section("p2p"), Doc("libp2p protocol the function serves (p2p trigger).")),
				String("p2p-command", Path("trigger", "command"), Tag("command"), Section("p2p"), Doc("Command name within the p2p protocol this function handles.")),
				String("http-method", Path("trigger", "method"), IsHttpMethod(), Tag("method"), Section("http"), Doc("HTTP method the function handles (http/https trigger).")),
				StringSlice("http-methods", Path("trigger", "methods"), Tag("methods"), NoAccessors(), NoStructField()), // TO IMPLEMENT
				StringSlice("http-domains", Path("trigger", "domains"), Compat("domains"), Tag("domains"), Ref("domains"), Section("http"), Doc("Domains that route to this function. Each must name a defined domain.")),
				StringSlice("http-paths", Path("trigger", "paths"), Tag("paths"), Section("http"), Doc("URL path patterns that route to this function (http/https trigger).")),
				String("source", Ref("libraries", Prefix("libraries/")), sourceShape, Section("code"), Doc("Code source: \".\" for inline code, or \"libraries/<name>\" to build from a defined library.")),
				Duration("timeout", Path("execution", "timeout"), Section("limits"), Doc("Maximum execution time, as a human string (e.g. \"30s\").")),
				Bytes("memory", Path("execution", "memory"), Section("limits"), Doc("Maximum memory the function may use, as a human string (e.g. \"32MB\").")),
				String("call", Path("execution", "call"), Section("code"), Doc("Exported entrypoint symbol invoked in the WASM module.")),
			),
			GroupDoc("A serverless function triggered over HTTP(S), PubSub, or p2p."),
			secIdentity,
			SectionDefinition("trigger", "Trigger", "How the function is invoked."),
			SectionDefinitionWhen("http", "HTTP", "HTTP(S) routing.", "type", "http", "https"),
			SectionDefinitionWhen("pubsub", "PubSub", "PubSub subscription.", "type", "pubsub"),
			SectionDefinitionWhen("p2p", "P2P", "libp2p protocol handling.", "type", "p2p"),
			SectionDefinition("code", "Code", "The function's code source and entrypoint."),
			SectionDefinition("limits", "Limits", "Runtime resource limits."),
			Addressing(HasBasicPath, HasIndex, HasHttp, HasWasmModule, HasServices),
			Embeds("Wasm"),
			Resource("functions", "Function", "Function", "function"),
			interp.IndexByName(HasWasmModule),
			interp.IndexForeignKey(HasHttp, "domains", "domains", "fqdn"),
		)),
	DefineGroup("libraries",
		DefineIter(
			TaubyteAttributes(
				String("path", Path("source", "path"), Section("source"), Doc("Subpath within the repository that holds the library code.")),
				String("branch", Path("source", "branch"), Section("source"), Doc("Git branch to build the library from.")),
				String("git-provider", Path("source", Either("github")), Key(), Field("Provider"), Tag("provider"), Section("source"), Doc("Source-control provider hosting the repository (the key selects the provider block).")),
				String("github-id", Path("source", "github", "id"), Field("RepoID"), Tag("repository-id"), NoAccessors(), Section("source"), Doc("GitHub repository numeric id.")),
				String("github-fullname", Path("source", "github", "fullname"), Field("RepoName"), Tag("repository-name"), NoAccessors(), Section("source"), Doc("GitHub repository full name (owner/repo).")),
			),
			GroupDoc("A reusable code library backed by a git repository, referenced as a function/smartop source."),
			secIdentity,
			SectionDefinition("source", "Source", "Where the library's code is sourced from."),
			Addressing(HasBasicPath, HasIndex, HasWasmModule, HasNameIndex),
			Embeds("Wasm"),
			Resource("libraries", "Library", "Library", "library"),
			interp.IndexByName(HasWasmModule),
			interp.IndexRepo(repositorytype.LibraryRepository),
			interp.IndexName(),
		)),
	DefineGroup("messaging",
		DefineIter(
			TaubyteAttributes(
				Bool("local", Section("channel"), Doc("Restrict the channel to the local node / project scope.")),
				String("match", Path("channel", "match"), Field("Match"), Accessor("ChannelMatch"), NoSetter(), Section("channel"), Doc("Channel name or pattern this messaging resource matches.")),
				Bool("regex", Path("channel", "regex"), NoSetter(), Section("channel"), Doc("Treat match as a regular expression.")),
				Bool("mqtt", Path("bridges", "mqtt", "enable"), Accessor("MQTT"), NoSetter(), Section("bridges"), Doc("Expose this channel over the MQTT bridge.")),
				Bool("websocket", Path("bridges", "websocket", "enable"), Tag("webSocket"), Accessor("WebSocket"), NoSetter(), Section("bridges"), Doc("Expose this channel over the WebSocket bridge.")),
			),
			GroupDoc("A PubSub messaging channel, optionally bridged to MQTT/WebSocket."),
			secIdentity,
			SectionDefinition("channel", "Channel", "Channel matching."),
			SectionDefinition("bridges", "Bridges", "External protocol bridges (MQTT / WebSocket)."),
			Addressing(HasBasicPath, HasIndex, HasWebSocket, HasEmptyPath),
			// messaging embeds Wasm beyond its capability flags — load-bearing in
			// the dream inject path (services/tns/mocks casts to structureSpec.Wasm).
			Embeds("Basic", "Wasm"),
			Resource("messaging", "Messaging", "Messaging", "messaging"),
			interp.IndexByScope(HasWebSocket),
		)),
	DefineGroup("services",
		DefineIter(
			TaubyteAttributes(
				String("protocol", Section("identity"), Doc("libp2p protocol identifier this service advertises.")),
			),
			GroupDoc("A libp2p service advertised on the network."),
			secIdentity,
			Addressing(HasIndex, HasEmptyPath),
			Embeds("Indexer"),
			Resource("services", "Service", "Service", "service"),
		)),
	DefineGroup("smartops",
		DefineIter(
			TaubyteAttributes(
				String("source", Ref("libraries", Prefix("libraries/")), sourceShape, Section("code"), Doc("Code source: \".\" for inline code, or \"libraries/<name>\" to build from a defined library.")),
				Duration("timeout", Path("execution", "timeout"), Section("limits"), Doc("Maximum execution time, as a human string (e.g. \"30s\").")),
				Bytes("memory", Path("execution", "memory"), Section("limits"), Doc("Maximum memory the smartop may use, as a human string (e.g. \"32MB\").")),
				String("call", Path("execution", "call"), Section("code"), Doc("Exported entrypoint symbol invoked in the WASM module.")),
			),
			GroupDoc("A smartop: policy/hook code (inline or backed by a library) attached to every resource."),
			secIdentity,
			SectionDefinition("code", "Code", "The smartop's code source and entrypoint."),
			SectionDefinition("limits", "Limits", "Runtime resource limits."),
			Addressing(HasBasicPath, HasIndex, HasWasmModule),
			Embeds("Wasm"),
			Resource("smartops", "SmartOps", "SmartOp", "smartops"),
			// smartops attach to every resource: each compiled resource carries a
			// derived SmartOps []string field (key "smartops"), sourced here.
			AttachesToAll(),
			interp.IndexByName(HasWasmModule),
		)),
	DefineGroup("storages",
		DefineIter(
			TaubyteAttributes(
				String("type", Path(Either("object", "streaming")), Key(), Section("storage"), Doc("Storage kind: object or streaming (the key selects the type block).")),
				String("match", Section("storage"), Doc("Key or key-pattern this storage serves (a literal prefix, or a regex when useRegex is set).")),
				Bool("useRegex", Path("regex"), Compat("useRegex"), Section("storage"), Doc("Treat match as a regular expression instead of a literal prefix.")),
				String("network-access", Path("access", "network"), InSet("all", "subnet", "host"), Default("all"), EnumBool("Public", []string{"all"}, []string{"all", "subnet", "host"}, [2]string{"all", "subnet"}), NoAccessors(), Section("access"), Doc("Which peers may reach this storage: all, subnet (project peers only), or host (local node only).")),
				Bool("versioning", Path("object", "versioning"), NoSetter(), Section("storage"), Doc("Keep historical versions of objects (object storage).")),
				Duration("ttl", Path("streaming", "ttl"), Field("Ttl"), Accessor("TTL"), NoSetter(), Section("storage"), Doc("Time-to-live for streamed entries, as a human string (e.g. \"1h\") (streaming storage).")),
				Bytes("size", Path(Either("object", "streaming"), "size"), Section("storage"), Doc("Maximum storage size, as a human string (e.g. \"1GB\").")),
			),
			GroupDoc("Object or streaming storage served to the project's peers."),
			secIdentity,
			SectionDefinition("storage", "Storage", "Storage kind, key matching, and capacity."),
			SectionDefinition("access", "Access", "Network reachability."),
			Addressing(HasBasicPath, HasIndex, HasIndexPath),
			Embeds("Basic", "Indexer"),
			Resource("storages", "Storage", "Storage", "storage"),
			interp.IndexByName(HasIndexPath),
		)),
	DefineGroup("websites",
		DefineIter(
			TaubyteAttributes(
				StringSlice("domains", Path("domains"), Ref("domains"), Section("serving"), Doc("Domains that serve this website. Each must name a defined domain.")),
				StringSlice("paths", Path("paths"), Compat("source", "paths"), Section("serving"), Doc("URL path patterns served by this website.")), // TODO: add validation
				String("branch", Path("source", "branch"), Section("source"), Doc("Git branch to build the website from.")),                          // TODO: deprecate
				String("git-provider", Path("source", Either("github")), Key(), Field("Provider"), Tag("provider"), Section("source"), Doc("Source-control provider hosting the repository (the key selects the provider block).")),
				String("github-id", Path("source", "github", "id"), Field("RepoID"), Tag("repository-id"), NoAccessors(), Section("source"), Doc("GitHub repository numeric id.")),
				String("github-fullname", Path("source", "github", "fullname"), Field("RepoName"), Tag("repository-name"), NoAccessors(), Section("source"), Doc("GitHub repository full name (owner/repo).")),
			),
			GroupDoc("A static website built from a git repository and served over one or more domains."),
			secIdentity,
			SectionDefinition("serving", "Serving", "Domains and paths served."),
			SectionDefinition("source", "Source", "Where the website's code is sourced from."),
			Addressing(HasBasicPath, HasIndex, HasHttp, HasWasmModule),
			Embeds("Basic", "Wasm"),
			Resource("website", "Website", "Website", "website"),
			interp.IndexForeignKey(HasHttp, "domains", "domains", "fqdn"),
			interp.IndexRepo(repositorytype.WebsiteRepository),
		)),
}

// applicationsGroup is the applications container: a group whose iterator (a
// DefineIterGroup) holds a nested copy of every resource group. Because it's the
// only such nested container with no Resource descriptor, tcc-gen recognizes it
// structurally and generates pkg/specs/structure/application.go — a bare struct
// of the common fields, no object-addressing methods and no pkg/schema accessor
// package (it's a container identity, not a config-decode resource).
func applicationsGroup() *Node {
	return DefineGroup("applications",
		DefineIterGroup(TaubyteAttributes(), TaubyteRessources...).With(Singular("Application"), GroupDoc("An application: a named grouping of resources with its own scope within the project.")))
}

// cloudsGroup: clouds.<fqdn>.{account, plan} — DefineIter (not Group, so no
// nested sub-groups) so each FQDN's attrs live directly under the map key in
// nested YAML. PromoteEnvKeyed selects the entry keyed by the compile env's
// "cloud" var (set by WithCloud), hoists account/plan to the project root, and
// drops the map — so clouds compiles to no structureSpec type. Pure declaration
// data: the generic projection lives in interp, no taubyte closure here.
func cloudsGroup() *Node {
	return DefineGroup("clouds", DefineIter(
		[]*Attribute{
			String("account"),
			String("plan"),
		},
		interp.PromoteEnvKeyed("clouds", "cloud", []string{"account", "plan"}, true),
	))
}

var taubyteRoot = Root(
	taubyteRootAttributes(),
	append(append([]*Node{}, TaubyteRessources...), applicationsGroup(), cloudsGroup())...,
)

var TaubyteProject = SchemaDefinition(taubyteRoot)

// GenerationRoot is the node list tcc-gen walks: the real project root's groups
// (the 9 resources + applications + clouds), so no curated list can drift from
// the schema. Every generator/test uses this one accessor.
func GenerationRoot() []*Node { return taubyteRoot.Children }

// CompileRoot is the CompileDriver's entrypoint into the DSL: the whole project
// root node. Its Attributes carry the project-scope driver annotations (id ->
// project_id validation, tags -> wire drop) and its Children are the resource,
// applications-container and clouds groups. Distinct from GenerationRoot, which
// exposes only the children (all a code generator needs).
func CompileRoot() *Node { return taubyteRoot }
