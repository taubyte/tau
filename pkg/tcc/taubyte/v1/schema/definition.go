package schema

import (
	//lint:ignore ST1001 keeps defintion clean
	. "github.com/taubyte/tau/pkg/tcc/engine"
)

func TaubyteAttributes(attrs ...*Attribute) []*Attribute {
	return append(
		attrs,
		String("id", IsCID(), Required()),
		String("name", IsVariableName()),
		String("description"),
		StringSlice("tags"),
	)
}

var TaubyteRessources = []*Node{
	DefineGroup("databases",
		DefineIter( //use name as "name"?
			TaubyteAttributes(
				String("match"),
				Bool("useRegex", Path("regex"), Compat("useRegex")),
				String("network-access", Path("access", "network"), InSet("all", "subnet", "host"), Default("all"), EnumBool("Local", []string{"host"}, []string{"all", "host"}, [2]string{"host", "all"}), NoAccessors()),
				Bytes("size", Path("storage", "size")),
				String("encryption-type", Path("encryption", "type"), NoAccessors(), NoStructField()),
				String("encryption-key", Path("encryption", "key"), NoAccessors()),
			),
			Addressing(HasBasicPath, HasIndex, HasIndexPath),
			Embeds("Basic", "Indexer"),
			Resource("databases", "Database", "Database", "database"),
		)),
	DefineGroup("domains",
		DefineIter(
			TaubyteAttributes(
				String("fqdn", IsFqdn(), Field("Fqdn"), Accessor("FQDN"), NoGetter()),
				String("certificate-data", Path("certificate", "cert"), Field("CertFile"), Tag("cert-file")),
				String("certificate-key", Path("certificate", "key"), Field("KeyFile"), Tag("key-file")),
				String("certificate-type", Path("certificate", "type"), InSet("inline", "auto"), Default(""), Field("CertType"), Tag("cert-type")),
			),
			// domain's BasicPath is bespoke (fqdn-reversed), so it's not tagged here.
			Addressing(HasIndex),
			Embeds("Indexer"),
			Resource("domains", "Domain", "Domain", "domain"),
		)),
	DefineGroup("functions",
		DefineIter(
			TaubyteAttributes(
				String("type", Path("trigger", "type"), InSet("http", "https", "pubsub", "p2p"), DerivedBool("Secure", map[string]bool{"http": false, "https": true}, map[bool]string{false: "http", true: "https"})),
				Bool("local", Path("trigger", "local")),
				String("pubsub-channel", Path("trigger", "channel"), Tag("channel")),
				String("p2p-protocol", Path("trigger", "protocol"), Compat("trigger", "service"), Tag("service"), Default("")),
				String("p2p-command", Path("trigger", "command"), Tag("command")),
				String("http-method", Path("trigger", "method"), IsHttpMethod(), Tag("method")),
				StringSlice("http-methods", Path("trigger", "methods"), Tag("methods"), NoAccessors(), NoStructField()), // TO IMPLEMENT
				StringSlice("http-domains", Path("trigger", "domains"), Compat("domains"), Tag("domains")),
				StringSlice("http-paths", Path("trigger", "paths"), Tag("paths")),
				String("source"),
				Duration("timeout", Path("execution", "timeout")),
				Bytes("memory", Path("execution", "memory")),
				String("call", Path("execution", "call")),
			),
			Addressing(HasBasicPath, HasIndex, HasHttp, HasWasmModule, HasServices),
			Embeds("Wasm"),
			Resource("functions", "Function", "Function", "function"),
		)),
	DefineGroup("libraries",
		DefineIter(
			TaubyteAttributes(
				String("path", Path("source", "path")),
				String("branch", Path("source", "branch")),
				String("git-provider", Path("source", Either("github")), Key(), Field("Provider"), Tag("provider")),
				String("github-id", Path("source", "github", "id"), Field("RepoID"), Tag("repository-id"), NoAccessors()),
				String("github-fullname", Path("source", "github", "fullname"), Field("RepoName"), Tag("repository-name"), NoAccessors()),
			),
			Addressing(HasBasicPath, HasIndex, HasWasmModule, HasNameIndex),
			Embeds("Wasm"),
			Resource("libraries", "Library", "Library", "library"),
		)),
	DefineGroup("messaging",
		DefineIter(
			TaubyteAttributes(
				Bool("local"),
				String("match", Path("channel", "match"), Field("Match"), Accessor("ChannelMatch"), NoSetter()),
				Bool("regex", Path("channel", "regex"), NoSetter()),
				Bool("mqtt", Path("bridges", "mqtt", "enable"), Accessor("MQTT"), NoSetter()),
				Bool("websocket", Path("bridges", "websocket", "enable"), Tag("webSocket"), Accessor("WebSocket"), NoSetter()),
			),
			Addressing(HasBasicPath, HasIndex, HasWebSocket, HasEmptyPath),
			// messaging embeds Wasm beyond its capability flags — load-bearing in
			// the dream inject path (services/tns/mocks casts to structureSpec.Wasm).
			Embeds("Basic", "Wasm"),
			Resource("messaging", "Messaging", "Messaging", "messaging"),
		)),
	DefineGroup("services",
		DefineIter(
			TaubyteAttributes(
				String("protocol"),
			),
			Addressing(HasIndex, HasEmptyPath),
			Embeds("Indexer"),
			Resource("services", "Service", "Service", "service"),
		)),
	DefineGroup("smartops",
		DefineIter(
			TaubyteAttributes(
				String("source"),
				Duration("timeout", Path("execution", "timeout")),
				Bytes("memory", Path("execution", "memory")),
				String("call", Path("execution", "call")),
			),
			Addressing(HasBasicPath, HasIndex, HasWasmModule),
			Embeds("Wasm"),
			Resource("smartops", "SmartOps", "SmartOp", "smartops"),
			// smartops attach to every resource: each compiled resource carries a
			// derived SmartOps []string field (key "smartops"), sourced here.
			AttachesToAll(),
		)),
	DefineGroup("storages",
		DefineIter(
			TaubyteAttributes(
				String("type", Path(Either("object", "streaming")), Key()),
				String("match"),
				Bool("useRegex", Path("regex"), Compat("useRegex")),
				String("network-access", Path("access", "network"), InSet("all", "subnet", "host"), Default("all"), EnumBool("Public", []string{"all"}, []string{"all", "subnet", "host"}, [2]string{"all", "subnet"}), NoAccessors()),
				Bool("versioning", Path("object", "versioning"), NoSetter()),
				Duration("ttl", Path("streaming", "ttl"), Field("Ttl"), Accessor("TTL"), NoSetter()),
				Bytes("size", Path(Either("object", "streaming"), "size")),
			),
			Addressing(HasBasicPath, HasIndex, HasIndexPath),
			Embeds("Basic", "Indexer"),
			Resource("storages", "Storage", "Storage", "storage"),
		)),
	DefineGroup("websites",
		DefineIter(
			TaubyteAttributes(
				StringSlice("domains", Path("domains")),
				StringSlice("paths", Path("paths"), Compat("source", "paths")), // TODO: add validation
				String("branch", Path("source", "branch")),                     // TODO: deprecate
				String("git-provider", Path("source", Either("github")), Key(), Field("Provider"), Tag("provider")),
				String("github-id", Path("source", "github", "id"), Field("RepoID"), Tag("repository-id"), NoAccessors()),
				String("github-fullname", Path("source", "github", "fullname"), Field("RepoName"), Tag("repository-name"), NoAccessors()),
			),
			Addressing(HasBasicPath, HasIndex, HasHttp, HasWasmModule),
			Embeds("Basic", "Wasm"),
			Resource("website", "Website", "Website", "website"),
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
		DefineIterGroup(TaubyteAttributes(), TaubyteRessources...).With(Singular("Application")))
}

// cloudsGroup: clouds.<fqdn>.{account, plan} — DefineIter (not Group, so no
// nested sub-groups) so each FQDN's attrs live directly under the map key in
// nested YAML. Pass1 flattens the active FQDN's entry to the project root and
// drops the map, so clouds compiles to no structureSpec type.
func cloudsGroup() *Node {
	return DefineGroup("clouds", DefineIter([]*Attribute{
		String("account"),
		String("plan"),
	}))
}

var taubyteRoot = Root(
	TaubyteAttributes(
		String("email", Path("notification", "email"), IsEmail()),
	),
	append(append([]*Node{}, TaubyteRessources...), applicationsGroup(), cloudsGroup())...,
)

var TaubyteProject = SchemaDefinition(taubyteRoot)

// GenerationRoot is the node list tcc-gen walks: the real project root's groups
// (the 9 resources + applications + clouds), so no curated list can drift from
// the schema. Every generator/test uses this one accessor.
func GenerationRoot() []*Node { return taubyteRoot.Children }
