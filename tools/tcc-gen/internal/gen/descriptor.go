package gen

import (
	"strings"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

// descriptor is the per-resource Go-naming the templates emit. It is built from
// the DSL's Resource(...) annotation (no hardcoded table): only the irregular
// facts are declared; the rest derives.
type descriptor struct {
	Package     string // pkg/schema/<dir> accessor package
	Iface       string // exported accessor interface, e.g. "Database" ("SmartOps")
	Struct      string // private impl struct, e.g. "database" ("smartOps")
	Recv        string // method receiver letter, e.g. "d"
	Noun        string // WrapError noun + open.go local var, e.g. "database"
	FolderConst string // common.<X>Folder constant name
	Spec        string // structureSpec type name, e.g. "Database" ("SmartOp")
	SpecPkg     string // pkg/specs/<dir> addressing-helper package
}

// descriptorFor builds the descriptor for a resource from the Resource(...)
// annotation on its iterator node. Struct/Recv/Noun/FolderConst are derived from
// the declared Iface / spec package. Returns false for groups with no Resource
// annotation (applications/clouds).
func descriptorFor(iter *engine.Node) (descriptor, bool) {
	r, ok := iter.Meta["resource"].([4]string)
	if !ok {
		return descriptor{}, false
	}
	pkg, iface, specType, specPkg := r[0], r[1], r[2], r[3]
	return descriptor{
		Package:     pkg,
		Iface:       iface,
		Struct:      strings.ToLower(iface[:1]) + iface[1:],
		Recv:        strings.ToLower(iface[:1]),
		Noun:        specPkg,
		FolderConst: iface + "Folder",
		Spec:        specType,
		SpecPkg:     specPkg,
	}, true
}
