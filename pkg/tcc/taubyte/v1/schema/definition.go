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
				String("network-access", Path("access", "network"), InSet("all", "subnet", "host"), Default("all"), StructBool("Local")),
				Bytes("size", Path("storage", "size")),
				String("encryption-type", Path("encryption", "type")),
				String("encryption-key", Path("encryption", "key")),
			),
			Addressing(HasBasicPath, HasIndex, HasIndexPath),
			Embeds("Basic", "Indexer"),
			Spec("Database", "database"),
		)),
	DefineGroup("domains",
		DefineIter(
			TaubyteAttributes(
				String("fqdn", IsFqdn(), Field("Fqdn")),
				String("certificate-data", Path("certificate", "cert"), Field("CertFile"), Tag("cert-file")),
				String("certificate-key", Path("certificate", "key"), Field("KeyFile"), Tag("key-file")),
				String("certificate-type", Path("certificate", "type"), InSet("inline", "auto"), Default(""), Field("CertType"), Tag("cert-type")),
			),
			// domain's BasicPath is bespoke (fqdn-reversed), so it's not tagged here.
			Addressing(HasIndex),
			Embeds("Indexer"),
			Spec("Domain", "domain"),
		)),
	DefineGroup("functions",
		DefineIter(
			TaubyteAttributes(
				String("type", Path("trigger", "type"), InSet("http", "https", "pubsub", "p2p")),
				Bool("local", Path("trigger", "local")),
				String("pubsub-channel", Path("trigger", "channel")),
				String("p2p-protocol", Path("trigger", "protocol"), Compat("trigger", "service"), Default("")),
				String("p2p-command", Path("trigger", "command")),
				String("http-method", Path("trigger", "method"), IsHttpMethod()),
				StringSlice("http-methods", Path("trigger", "methods")), // TO IMPLEMENT
				StringSlice("http-domains", Path("trigger", "domains"), Compat("domains")),
				StringSlice("http-paths", Path("trigger", "paths")),
				String("source"),
				Duration("timeout", Path("execution", "timeout")),
				Bytes("memory", Path("execution", "memory")),
				String("call", Path("execution", "call")),
			),
			Addressing(HasBasicPath, HasIndex, HasHttp, HasWasmModule, HasServices),
			Embeds("Wasm"),
			Spec("Function", "function"),
			// secure is synthesized from type=="https" in pass1.
			DerivedBools("Secure"),
		)),
	DefineGroup("libraries",
		DefineIter(
			TaubyteAttributes(
				String("path", Path("source", "path")),
				String("branch", Path("source", "branch")),
				String("git-provider", Path("source", Either("github")), Key(), Field("Provider")),
				String("github-id", Path("source", "github", "id"), Field("RepoID"), Tag("repository-id")),
				String("github-fullname", Path("source", "github", "fullname"), Field("RepoName"), Tag("repository-name")),
			),
			Addressing(HasBasicPath, HasIndex, HasWasmModule, HasNameIndex),
			Embeds("Wasm"),
			Spec("Library", "library"),
		)),
	DefineGroup("messaging",
		DefineIter(
			TaubyteAttributes(
				Bool("local"),
				String("match", Path("channel", "match"), Field("Match")),
				Bool("regex", Path("channel", "regex")),
				Bool("mqtt", Path("bridges", "mqtt", "enable")),
				Bool("websocket", Path("bridges", "websocket", "enable")),
			),
			Addressing(HasBasicPath, HasIndex, HasWebSocket, HasEmptyPath),
			// messaging embeds Wasm beyond its capability flags — load-bearing in
			// the dream inject path (services/tns/mocks casts to structureSpec.Wasm).
			Embeds("Basic", "Wasm"),
			Spec("Messaging", "messaging"),
		)),
	DefineGroup("services",
		DefineIter(
			TaubyteAttributes(
				String("protocol"),
			),
			Addressing(HasIndex, HasEmptyPath),
			Embeds("Indexer"),
			Spec("Service", "service"),
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
			Spec("SmartOp", "smartops"),
		)),
	DefineGroup("storages",
		DefineIter(
			TaubyteAttributes(
				String("type", Path(Either("object", "streaming")), Key()),
				String("match"),
				Bool("useRegex", Path("regex"), Compat("useRegex")),
				String("network-access", Path("access", "network"), InSet("all", "subnet", "host"), Default("all"), StructBool("Public")),
				Bool("versioning", Path("object", "versioning")),
				Duration("ttl", Path("streaming", "ttl"), Field("Ttl")),
				Bytes("size", Path(Either("object", "streaming"), "size")),
			),
			Addressing(HasBasicPath, HasIndex, HasIndexPath),
			Embeds("Basic", "Indexer"),
			Spec("Storage", "storage"),
		)),
	DefineGroup("websites",
		DefineIter(
			TaubyteAttributes(
				StringSlice("domains", Path("domains")),
				StringSlice("paths", Path("paths"), Compat("source", "paths")), // TODO: add validation
				String("branch", Path("source", "branch")),                     // TODO: deprecate
				String("git-provider", Path("source", Either("github")), Key(), Field("Provider")),
				String("github-id", Path("source", "github", "id"), Field("RepoID"), Tag("repository-id")),
				String("github-fullname", Path("source", "github", "fullname"), Field("RepoName"), Tag("repository-name")),
			),
			Addressing(HasBasicPath, HasIndex, HasHttp, HasWasmModule),
			Embeds("Basic", "Wasm"),
			Spec("Website", "website"),
		)),
}

var TaubyteProject = SchemaDefinition(
	Root(
		TaubyteAttributes(
			String("email", Path("notification", "email"), IsEmail()),
		),
		append(TaubyteRessources,
			DefineGroup("applications",
				DefineIterGroup(
					TaubyteAttributes(),
					TaubyteRessources...,
				),
			),
			// clouds.<fqdn>.{account, plan} — DefineIter (not Group) so each
			// FQDN's attrs live directly under the map key in nested YAML.
			DefineGroup("clouds",
				DefineIter(
					[]*Attribute{
						String("account"),
						String("plan"),
					},
				),
			),
		)...,
	),
)
