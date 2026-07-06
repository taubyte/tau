package gen

// descriptor holds the per-resource facts that are NOT derivable from the DSL
// (irregular singularization, receiver letter, error noun, folder constant).
// Keyed by the DSL group name as it appears in definition.go's TaubyteRessources
// (note: the group is "websites" but the package is "website").
type descriptor struct {
	Package     string // go package / directory under pkg/schema
	Iface       string // exported resource interface, e.g. "Function"
	Struct      string // private impl struct, e.g. "function" ("smartOps")
	Recv        string // method receiver letter, e.g. "f"
	Noun        string // WrapError noun + open.go local var, e.g. "function"
	FolderConst string // common.<X>Folder constant name
	Spec        string // structureSpec type name, e.g. "Function" ("SmartOp")
}

var descriptors = map[string]descriptor{
	"databases": {Package: "databases", Iface: "Database", Struct: "database", Recv: "d", Noun: "database", FolderConst: "DatabaseFolder", Spec: "Database"},
	"domains":   {Package: "domains", Iface: "Domain", Struct: "domain", Recv: "d", Noun: "domain", FolderConst: "DomainFolder", Spec: "Domain"},
	"functions": {Package: "functions", Iface: "Function", Struct: "function", Recv: "f", Noun: "function", FolderConst: "FunctionFolder", Spec: "Function"},
	"libraries": {Package: "libraries", Iface: "Library", Struct: "library", Recv: "l", Noun: "library", FolderConst: "LibraryFolder", Spec: "Library"},
	"messaging": {Package: "messaging", Iface: "Messaging", Struct: "messaging", Recv: "m", Noun: "messaging", FolderConst: "MessagingFolder", Spec: "Messaging"},
	"services":  {Package: "services", Iface: "Service", Struct: "service", Recv: "s", Noun: "service", FolderConst: "ServiceFolder", Spec: "Service"},
	"smartops":  {Package: "smartops", Iface: "SmartOps", Struct: "smartOps", Recv: "s", Noun: "smartops", FolderConst: "SmartOpsFolder", Spec: "SmartOp"},
	"storages":  {Package: "storages", Iface: "Storage", Struct: "storage", Recv: "s", Noun: "storage", FolderConst: "StorageFolder", Spec: "Storage"},
	"websites":  {Package: "website", Iface: "Website", Struct: "website", Recv: "w", Noun: "website", FolderConst: "WebsiteFolder", Spec: "Website"},
}
