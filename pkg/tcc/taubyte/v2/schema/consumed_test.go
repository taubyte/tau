package schema_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/tcc/engine"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// consumed_test runs the hygiene check the OTHER WAY ROUND.
//
// # The gap this closes
//
// services/worker/settings_test.go holds both ends of the Settings key space to
// the vocabulary: every key the compiler emits is an attribute, and every key
// the package spells still names one. Both walk from the RUNTIME to the
// vocabulary, so both are silent about the opposite drift — an attribute the
// vocabulary declares that nothing anywhere reads. An author writes it, the
// compiler files it into the object, the audit says nothing, the build is green,
// and the cloud does not do the thing the document says.
//
// The sharp one when this was written is `gateway.certificate.*`: a gateway may
// declare where its certificate comes from and which secret holds it, and the
// only reader of those keys is services/v2/proxy, for a proxy's own copy of
// them. node/boot binds a gateway with an http.Server carrying no TLSConfig, so
// a gateway that declares TLS is served in the clear.
//
// # What "read" means here, and what it deliberately excludes
//
// The runtime does not use the generated typed readers: of the 27 ReadX
// functions in pkg/specs/kinds.go, only ReadZone has a production consumer, and
// specs.Readers is consumed by its own test. What the runtime does instead is
// address compiled keys as strings. So an attribute is READ when at least one of
// these holds:
//
//   - A SPELLED READ. A file in `readers` performs a reader-method call on a
//     resource whose path matches the attribute's compiled key — literals, this
//     package's own string consts, a `Sub(...)` prefix it was reached through,
//     the elements of a literal list a loop reads it under, and the call sites
//     of a helper in the same file that reads at a path its caller supplies.
//   - A KEY THE VOCABULARY HANDS THE READER. Three shapes are addressed by
//     asking the schema for the field name rather than spelling it, so no file
//     mentions them: the identity pair (engine.IdentityOf), the access entry
//     (schema.AccessEntryShape, read by Resource.Reaches) and the block a kind
//     answers on a name in (engine.AnswersAtOf, read by schema.ReachableNames
//     for Project.Hosts).
//   - A TYPED FIELD SOMEBODY USES. The kind's generated reader has a production
//     consumer, and the struct field the attribute decodes into is named in one
//     of the files listed in `exposed` for it.
//   - A NESTED GROUP'S FIELD, read where it actually sits. AttributeNames
//     flattens the fields of a group the author keys — `variables`, `ports`,
//     `entry` — into the kind's list without the group's path, so `kind` is an
//     attribute of a workflow and `entry/start/kind` is where worker reads it.
//     See reaches for how those are told apart from a block's fields.
//
// Three consumers are NOT reads, on purpose:
//
//   - THE AUDIT. pkg/tcc/.../checks.go reads attributes to judge a config.
//     Being judged is not being applied: an attribute only the audit reads still
//     changes nothing about what the cloud does, which is exactly the class of
//     defect this test is for.
//   - THE EDITOR. tau-cli, the decompiler and the generator round-trip every
//     attribute by construction, so counting them would make every attribute
//     read and this test vacuous.
//   - THE LOWERING. What a lowered kind declares is compiled away into the keys
//     of the resources it produces, and those are what a tier reads. The
//     interpreter's Lowering keeps its terms unexported, so this cannot be asked
//     — those attributes are listed in `unread` with that as the reason, which
//     has the useful side effect of naming them.
//
// # Which way it errs
//
// Toward READ. A computed segment matches any segment, a block read whole counts
// for everything under it, and a file's reads count for every kind it is listed
// against — so a `*` file like services/worker/resolve.go, which resolves every
// kind, lends one kind's read to all of them: `host` is read for a database and
// therefore counts for a stream. That is the safe direction here. A false "read"
// costs one finding; a false "unread" would force an allowlist entry whose
// reason is not true, and an allowlist of untrue reasons is worse than none.
//
// Two things are deliberately NOT loosened, because they are what makes the
// findings real: a pattern is anchored at the start of the key and never matched
// as a suffix — suffix matching makes `certificate/secret/name` "read" by any
// file that reads a `name` — and a read with no literal segment in it at all
// says nothing, because the path came from a caller and the caller is where this
// can see it.

// # What an excuse can say, and what it cannot
//
// An excuse is one of these three:
//
//   - HANDLED. The value is not ignored; it is used somewhere this scan cannot
//     see. The compile consumed it (a reference's namespace, a lowering's
//     terms), or the runtime refuses by name the branch that holds it.
//   - INERT. It asks the cloud to do nothing: a label, an advisory note. There
//     is nothing for a runtime to apply, so there is nothing to ignore.
//   - PREPARATORY, which is said of a KIND and not an attribute: see
//     preparatoryKinds. Every declaration on it is ignored, and may be, because
//     the kind has NO RUNTIME AT ALL — no tier runs it and nothing dispatches
//     it. Its name must be spelled nowhere in services/ or node/; the day it
//     is, the kind has a runtime and the excuse fails.
//
// What an entry CANNOT say is that a RUNNABLE kind ignores a declaration. That
// is a deployment lie — the author writes it, it compiles, it deploys, and the
// cloud does something else — and the fix is in the runtime serving the kind:
// apply the value, or refuse it by name (worker's unservableKind is the
// pattern), which is itself a read this test sees. It is never tcc's to refuse:
// the compile answers what the config says, not what the platform can do.
type class int

const (
	handled class = iota + 1
	inert
)

type excuse struct {
	class  class
	reason string
}

// The reasons, said once each. A family of attributes excused for one reason
// gets one sentence and every member points at it.
var (
	noTagReader = excuse{inert, "a label the author writes for whoever reads the document. It asks the cloud for nothing."}

	advisoryRotation = excuse{inert, "how often a secret's value is expected to rotate. The vocabulary calls it advisory: it states an expectation and asks the cloud to do nothing."}

	secretScopeConsumed = excuse{handled, "the namespace half of a variable's secret reference. The compile resolved the name beside it to the secret's ID, so the scope had already done its work — see resolve.go's envOf."}

	componentScopeConsumed = excuse{handled, "the namespace half of a `source: {component: …}` reference. The compile resolved the name to the component's ID, so the scope was consumed resolving it."}

	proxyCertScopeConsumed = excuse{handled, "the namespace half of a proxy's certificate secret. The compile resolved the name beside it to the secret's ID."}

	deadLetterScopeConsumed = excuse{handled, "the namespace half of a dead-letter reference. The compile resolved the name to another queue's ID, so the scope was consumed resolving it."}

	streamingStorageRefused = excuse{handled, "the lifetime of a streaming storage's entries. worker refuses any storage whose type is not `object` by name (unservableKind), so this branch's only field is never served weaker than written — it is never served."}

	regionsRefused = excuse{handled, "where a resource's copies go, on a kind nothing places by region. worker reads the block's policy and refuses the grant by name (unservable), so the regions under it are never served weaker than written — the target is not served."}

	externalStreamRefused = excuse{handled, "the credentials for a stream on somebody else's broker. worker refuses a stream on any engine but tau's by name (unservableKind), so the credential is never served weaker than written."}

	agentLowered = excuse{handled, "consumed by the agent's lowering: limits/timeout and limits/memory become the loop function's execution/timeout and execution/memory, and the memory block becomes a vector database's dimensions and size. The interpreter keeps its lowering terms unexported, so this test cannot see that consumption."}

	workflowLowered = excuse{handled, "consumed by the workflow's lowering: a step's retry budget becomes its queue's max_attempts, delay and retry/*, `on_error` becomes its dead-letter queue, `call/*` becomes the step driver's grant on the function it calls, and `start` becomes the entry record worker reads at entry/start/*. The interpreter keeps its lowering terms unexported, so this test cannot see that consumption."}
)

// preparatoryKinds are the kinds with NO RUNTIME AT ALL, and why. Every
// declaration on one is ignored and may be, because nothing runs the kind to
// ignore it — checked, not trusted: see runtimeSpells.
var preparatoryKinds = map[string]string{
	"agent": "nothing runs an agent's loop. The lowering produces a function with `source: {builtin: agent}`, and no service or node dispatches that builtin — so which model it answers with, what it is told, and how far it may go reach nothing.",
}

// unread is every attribute the vocabulary declares that nothing reads, and
// WHY that is acceptable. A `*` kind excuses the attribute on every kind where
// nothing reads it.
//
// An entry here is a decision; a name missing from both this and a read is the
// drift this test exists to catch. An entry that stops being needed — because
// something started reading the attribute, or because no kind declares it any
// more — fails too, which is how the change that wires one up is made to take
// its excuse out.
var unread = map[string]excuse{
	"*/tags":             noTagReader,
	"*/secret/namespace": secretScopeConsumed,
	"*/source/namespace": componentScopeConsumed,

	"secret/rotation": advisoryRotation,

	"sync/queue/dead_letter/namespace":   deadLetterScopeConsumed,
	"proxy/certificate/secret/namespace": proxyCertScopeConsumed,

	"cache/regions/in":         regionsRefused,
	"cache/regions/main":       regionsRefused,
	"cache/regions/standby":    regionsRefused,
	"storage/regions/in":       regionsRefused,
	"storage/regions/main":     regionsRefused,
	"storage/regions/standby":  regionsRefused,
	"sync/regions/in":          regionsRefused,
	"sync/regions/main":        regionsRefused,
	"sync/regions/standby":     regionsRefused,
	"volume/regions/in":        regionsRefused,
	"volume/regions/main":      regionsRefused,
	"volume/regions/standby":   regionsRefused,
	"workflow/regions/in":      regionsRefused,
	"workflow/regions/main":    regionsRefused,
	"workflow/regions/standby": regionsRefused,

	"storage/streaming/ttl":        streamingStorageRefused,
	"stream/credentials/name":      externalStreamRefused,
	"stream/credentials/namespace": externalStreamRefused,

	"agent/limits/timeout":            agentLowered,
	"agent/limits/memory":             agentLowered,
	"agent/memory/size":               agentLowered,
	"agent/memory/embedder/name":      agentLowered,
	"agent/memory/embedder/namespace": agentLowered,

	"workflow/attempts":         workflowLowered,
	"workflow/backoff/interval": workflowLowered,
	"workflow/backoff/factor":   workflowLowered,
	"workflow/backoff/ceiling":  workflowLowered,
	"workflow/wait":             workflowLowered,
	"workflow/on_error":         workflowLowered,
	"workflow/start":            workflowLowered,
	"workflow/call/function":    workflowLowered,
	"workflow/call/namespace":   workflowLowered,
}

// readers names every non-test file that reads a compiled resource, and the
// kinds whose resources it reads. `*` is a file that reads every kind.
//
// THE ATTRIBUTION IS THE POINT. `certificate/source` is spelled in
// services/v2/proxy/tls.go, for a proxy; a check that only asked whether the
// string appears somewhere would report a gateway's identically named attribute
// as read and the config would go on saying TLS while the socket stays plain.
//
// A file that reads a resource and is missing here fails this test with what it
// reads, which is the prompt to say which kinds it serves.
var readers = map[string][]string{
	// pkg/specs is where a compiled project is read from at all: Source() and
	// Build() answer what a build needs of ANY kind, and the zone walk is the
	// one generated reader with a consumer.
	"pkg/specs/project.go": everyKind,

	// services/worker resolves a target of every kind onto a grant, which is
	// what carries an author's declarations to the tier that runs it.
	"services/worker/resolve.go": everyKind,
	// regions.go reads the regions block of what a tier runs, for placement to
	// choose a node by.
	"services/worker/regions.go": {"application", "function", "sandbox", "worker"},
	// schedule.go is the cron arm of the same service.
	"services/worker/schedule.go": {"cron"},

	// The two services that read a DECLARATION rather than the grant, each for
	// its own kind - see the file docs, which say why.
	"services/database/declarations.go":   {"database"},
	"services/primitives/declarations.go": {"sync"},

	// A gateway's server. bus.go is the arm that serves a bus reached through
	// one, and it reads the bus.
	"services/v2/gateway/gateway.go": {"gateway"},
	"services/v2/gateway/policy.go":  {"gateway"},
	"services/v2/gateway/route.go":   {"gateway"},
	"services/v2/gateway/bus.go":     {"bus"},

	// A proxy's. It terminates its own TLS, which is why it is the only reader
	// of a `certificate` block anywhere.
	"services/v2/proxy/proxy.go":  {"proxy"},
	"services/v2/proxy/route.go":  {"proxy"},
	"services/v2/proxy/shape.go":  {"proxy"},
	"services/v2/proxy/socket.go": {"proxy"},
	"services/v2/proxy/limits.go": {"proxy"},
	"services/v2/proxy/tls.go":    {"proxy"},
}

var everyKind = []string{"*"}

// exposed names the generated typed readers that have a production consumer, and
// the files that consume the struct. A field none of them names is decoded and
// dropped, however faithfully the generated reader read it.
var exposed = map[string][]string{
	// Project.Zones() is the only accessor in pkg/specs that hands a generated
	// struct out, and these two are what read it.
	"zone": {"node/zone/zone.go", "services/build/deploy/deploy.go"},
}

// TestEVERYATTRIBUTETHEVOCABULARYDECLARESISREADBYSOMETHING.
func TestEveryDeclaredAttributeIsReadBySomething(t *testing.T) {
	root := repoRoot(t)
	found := scan(t, root)

	// THE TABLE IS HELD TO THE TREE IN BOTH DIRECTIONS. A reader file nobody
	// classified would silently count for no kind, and a classified file that
	// stopped reading would keep a kind alive that nothing reads any more.
	var unclassified []string
	for file := range found {
		if _, known := readers[file]; !known {
			unclassified = append(unclassified, file+" reads "+strings.Join(sorted(found[file]), " "))
		}
	}
	sort.Strings(unclassified)
	require.Empty(t, unclassified,
		"these files read a compiled resource and are not in `readers` - add each with the kinds whose resources it reads (`*` for a file that reads every kind), so what it reads counts for them")
	for file := range readers {
		require.Contains(t, found, file,
			"`readers` names %s and nothing in it reads a compiled resource any more - drop the entry, or the kinds it covers are being kept alive by a file that stopped reading them", file)
	}

	perKind := map[string]map[string]bool{}
	for file, patterns := range found {
		for _, kind := range readers[file] {
			if perKind[kind] == nil {
				perKind[kind] = map[string]bool{}
			}
			for pattern := range patterns {
				perKind[kind][pattern] = true
			}
		}
	}

	// The keys no file spells because the vocabulary is asked for them.
	shape := schema.AccessEntryShape()
	idField, nameField := engine.IdentityOf(schema.CompileRoot())
	viaVocabulary := map[string]bool{
		shape.Ops: true, shape.Name: true, shape.Namespace: true, shape.Timeout: true,
		shape.MountPath: true, shape.DeliverPort: true, shape.DeliverPath: true, shape.Tools: true,
		idField: true, nameField: true,
	}

	// Every first segment the vocabulary declares anywhere - see reaches.
	heads := map[string]bool{}
	for _, group := range schema.V2Resources {
		kind, _ := group.Match.(string)
		for _, key := range schema.AttributeNames(kind) {
			head, _, _ := strings.Cut(key, "/")
			heads[head] = true
		}
	}

	consulted := map[string]bool{}
	dead := map[string][]string{}
	consultedKinds := map[string]bool{}
	for _, group := range schema.V2Resources {
		kind, _ := group.Match.(string)
		if kind == "" || len(group.Children) == 0 {
			continue
		}
		answersAt := engine.AnswersAtOf(group.Children[0])
		typed := typedFields(t, root, kind)
		inlined := inlinedInto(group.Children[0])

		for _, key := range schema.AttributeNames(kind) {
			switch {
			case viaVocabulary[key]:
				continue
			// The whole block, not the field: ReachableNames reads a kind's
			// naming block to compute what it answers at, addressing it by the
			// name the vocabulary gives rather than by a spelled path.
			case answersAt != "" && (key == answersAt || strings.HasPrefix(key, answersAt+"/")):
				continue
			case typed[key]:
				continue
			case reaches(perKind[kind], key, heads), reaches(perKind["*"], key, heads):
				continue
			// A FILE THE COMPILE READ. The attribute holds a path, the compile
			// reads that file into the key the vocabulary names, and what is
			// read is that key — so the path is read exactly when it is.
			case inlined[key] != "" && (typed[inlined[key]] || reaches(perKind[kind], inlined[key], heads) || reaches(perKind["*"], inlined[key], heads)):
				continue
			}
			if entry, ok := allowed(kind, key); ok {
				consulted[entry] = true
				continue
			}
			if _, ok := preparatoryKinds[kind]; ok {
				consultedKinds[kind] = true
				continue
			}
			dead[kind] = append(dead[kind], key)
		}
	}

	// A PREPARATORY KIND THAT HAS A RUNTIME has become the lie the class exists
	// to keep out: the kind runs now, and its declarations are ignored by what
	// runs it. And one nothing needed has expired, like any other excuse.
	for kind, reason := range preparatoryKinds {
		require.NotEmpty(t, reason, "preparatory kind %s has no reason", kind)
		require.Empty(t, runtimeSpells(t, root, kind),
			"%s is excused as having no runtime, and the runtime spells it now. An ignored declaration on a kind that runs is not excusable: apply it where the kind is served, or refuse it there by name.", kind)
		require.True(t, consultedKinds[kind],
			"%s is excused as having no runtime and nothing on it needed the excuse - drop it", kind)
	}

	if len(dead) > 0 {
		var lines []string
		for kind, keys := range dead {
			sort.Strings(keys)
			for _, key := range keys {
				lines = append(lines, kind+"/"+key)
			}
		}
		sort.Strings(lines)
		require.Empty(t, lines,
			"these attributes are declared and nothing reads them: an author can write one, it compiles, and the cloud does not do what the document says.\n"+
				"Read it where the kind is served - a tier, a service, pkg/specs - and apply it, or refuse it there BY NAME the way worker's unservableKind does.\n"+
				"`unread` does not take an ignored declaration on a kind that runs; it takes only what is handled elsewhere, inert, or on a kind with no runtime at all.\n"+
				"If it IS read and this test cannot see it, the reader is missing from `readers` or is reached in a way the scan does not follow; fix that rather than allowlisting a live attribute.")
	}

	// AND THE ALLOWLIST IS HELD TO THE TREE. An excuse nothing needed has
	// expired - the attribute is read now, or it is gone - and the next reader
	// takes an expired decision for a current one. This is what fails when the
	// change that makes an attribute live forgets to take its entry out.
	var expired []string
	for entry, reason := range unread {
		require.NotEmpty(t, reason.reason,
			"`unread` carries %s with no reason; an entry without one is a TODO pretending to be a decision", entry)
		require.NotZero(t, reason.class, "`unread` carries %s with no class", entry)
		if !consulted[entry] {
			expired = append(expired, entry)
		}
	}
	sort.Strings(expired)
	require.Empty(t, expired,
		"`unread` excuses these and nothing needed the excuse - something reads the attribute now, or no kind declares it any more. Drop the entries; an allowlist of expired decisions is where the next drift hides.")
}

// allowed reports WHICH entry excuses this attribute - the entry rather than its
// reason, because an entry nothing needed is the thing to report.
func allowed(kind, key string) (string, bool) {
	if _, ok := unread[kind+"/"+key]; ok {
		return kind + "/" + key, true
	}
	if _, ok := unread["*/"+key]; ok {
		return "*/" + key, true
	}
	return "", false
}

// reaches reports whether any pattern addresses this compiled key.
//
// heads is every first segment any kind declares, and it is what tells a nested
// GROUP apart from a nested block. AttributeNames flattens a group's fields into
// a kind's list without the group's path - `entry/start/kind` is where the value
// sits and `kind` is what the vocabulary calls it, because the segment between
// them is an author's own word - so a pattern whose first segment is nowhere an
// attribute is read for its tail as well: `variables`, `ports` and `entry` are
// groups and nothing else. A pattern under a BLOCK is not read that way:
// `certificate/secret/name` is not a read of `name` and `sql/credentials/name`
// is not a read of `credentials/name`, which is what lets this test tell a
// gateway's unread certificate from a proxy's read one.
func reaches(patterns map[string]bool, key string, heads map[string]bool) bool {
	for pattern := range patterns {
		if matches(pattern, key) {
			return true
		}
		segments := strings.Split(pattern, "/")
		if len(segments) >= 3 && !heads[segments[0]] && matches(strings.Join(segments[2:], "/"), key) {
			return true
		}
	}
	return false
}

// matches compares one read path against a compiled key, ANCHORED at the start:
// `*` is a segment a read computed rather than spelled, and `**` is the rest of
// the key - a block read whole, or a path a caller supplied.
func matches(pattern, key string) bool {
	read := strings.Split(pattern, "/")
	have := strings.Split(key, "/")
	for i, segment := range read {
		if segment == "**" {
			return i <= len(have)
		}
		if i >= len(have) {
			return false
		}
		if segment == "*" || segment == have[i] {
			continue
		}
		return false
	}
	return len(read) == len(have)
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for item := range set {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func repoRoot(t *testing.T) string {
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		up := filepath.Dir(dir)
		require.NotEqual(t, up, dir, "no go.mod above the test's own directory")
		dir = up
	}
}

// ---- reading the tree ----

// The Resource methods a read goes through. Sub is here because it is how a read
// of a nested block starts, and Map/List/Keys because they take a block whole -
// every attribute under the path they name travels with it.
var (
	readMethods  = map[string]bool{"Has": true, "String": true, "Strings": true, "Bool": true, "Int": true, "Float": true, "Duration": true, "Sub": true}
	blockMethods = map[string]bool{"Map": true, "List": true, "Keys": true}
)

const specsImportPath = `"github.com/taubyte/tau/pkg/specs"`

// pathReaders are pkg/specs' own primitives, which take the resource first and
// the path after. Resource.Source() reaches for a BARE `source` through descend,
// because a scalar there is the short form of the block - read as a block it
// would find nothing, and read by this test as nothing at all.
var pathReaders = map[string]bool{"descend": true}

// skipped are directories with no reader in them, and two that would lie: a
// worktree holds another branch's copy of this tree, and a vendored or generated
// example is not this cloud's code.
var skipped = map[string]bool{
	".git": true, ".claude": true, ".agents": true, "agent": true,
	"node_modules": true, "vendor": true, "testdata": true, "legacy": true,
}

// scan is every non-test file that reads a compiled resource, and the paths it
// reads. A file qualifies by speaking pkg/specs - the CLI's `ctx.String("name")`
// is the same call shape on a different receiver, and reading it as a resource
// read would make half the vocabulary look consumed.
func scan(t *testing.T, root string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	consts := map[string]map[string]string{}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if skipped[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			return nil
		}
		// A GENERATED DECODER IS NOT A CONSUMER. pkg/specs/kinds.go decodes every
		// attribute of every kind, which is why "it is decoded" says nothing
		// about whether anybody reads it - the premise of this whole test. What
		// a generated reader is worth is asked through `exposed` instead.
		if generatedHeader(file) {
			return nil
		}

		speaks := file.Name.Name == "specs"
		for _, imported := range file.Imports {
			if imported.Path.Value == specsImportPath {
				speaks = true
			}
		}
		if !speaks {
			return nil
		}

		dir := filepath.Dir(path)
		if _, ok := consts[dir]; !ok {
			consts[dir] = stringConsts(dir)
		}
		paths := readsIn(file, consts[dir])
		if len(paths) == 0 {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		require.NoError(t, err)
		out[filepath.ToSlash(relative)] = paths
		return nil
	})
	require.NoError(t, err)
	return out
}

// stringConsts is every string constant a package spells, so a read written as
// `target.String(queueType, "consensus")` is the path it resolves to rather than
// a hole. services/worker names most of its keys this way on purpose.
func stringConsts(dir string) map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			continue
		}
		for _, declared := range file.Decls {
			group, ok := declared.(*ast.GenDecl)
			if !ok || group.Tok != token.CONST {
				continue
			}
			for _, spec := range group.Specs {
				values, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range values.Names {
					if i >= len(values.Values) {
						continue
					}
					if text, ok := stringLit(values.Values[i]); ok {
						out[name.Name] = text
					}
				}
			}
		}
	}
	return out
}

// generatedHeader reports the marker tcc-gen and the go toolchain both write.
func generatedHeader(file *ast.File) bool {
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			break
		}
		for _, line := range group.List {
			if strings.Contains(line.Text, "Code generated by") && strings.Contains(line.Text, "DO NOT EDIT") {
				return true
			}
		}
	}
	return false
}

func stringLit(e ast.Expr) (string, bool) {
	literal, ok := e.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	text, err := strconv.Unquote(literal.Value)
	if err != nil || text == "" {
		return "", false
	}
	return text, true
}

// readsIn is every path one file reads, as patterns.
//
// Three things beyond the literal arguments, each of which was a whole class of
// live attribute this test would otherwise have called dead:
//
//	prefixes  `route := r.Sub("route")` then `route.Sub("handler").String("name")`
//	          is a read of route/handler/name, which is how a gateway reads its
//	          handler. A name's prefix is scoped: see readsIn.
//	helpers   `value := func(path ...string) int { n, _ := r.Int(path...) }` moves
//	          the literal to the CALL, which is where services/v2/proxy reads its
//	          rate limit.
//	loops     `for _, field := range []string{"identifier", "format"}` reads two
//	          attributes at one call site, which is how a model's are read.
func readsIn(file *ast.File, consts map[string]string) map[string]bool {
	options := loopValues(file, consts)
	helpers := dynamicReaders(file)

	// FIELDS ARE FILE-SCOPED, LOCALS ARE NOT. `Handler: route.Sub("handler")` is
	// filled in one function and read in another, so a field's prefix has to
	// outlive the function that set it. A local's must not: one function's `v`
	// is a variable block's entry and another's is the resource itself, and a
	// prefix that leaked between them reported the resource's own `secret/name`
	// as read because a VARIABLE's is.
	fields := map[string][]string{}
	out := map[string]bool{}

	// Twice, because a field can be filled in a function the file declares
	// before the one holding the local it was filled from.
	for round := 0; round < 2; round++ {
		for _, declared := range file.Decls {
			function, ok := declared.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			collectPrefixes(function.Body, consts, options, fields, map[string][]string{})
		}
	}

	for _, declared := range file.Decls {
		function, ok := declared.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		locals := map[string][]string{}
		for round := 0; round < 2; round++ {
			collectPrefixes(function.Body, consts, options, fields, locals)
		}
		readsUnder(function.Body, consts, options, helpers, fields, locals, out)
	}
	return out
}

// prefixOf resolves a receiver expression to the key prefix it stands for: a
// chain of Sub calls, or a name something assigned one to.
func prefixOf(e ast.Expr, consts map[string]string, options map[string][][]string, fields, locals map[string][]string) ([]string, bool) {
	switch value := e.(type) {
	case *ast.CallExpr:
		selector, ok := value.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Sub" || len(value.Args) == 0 {
			return nil, false
		}
		base, _ := prefixOf(selector.X, consts, options, fields, locals)
		paths := pathsOf(value, consts, options)
		if len(paths) != 1 {
			return nil, false
		}
		return append(append([]string{}, base...), paths[0]...), true
	case *ast.Ident:
		held, ok := locals[value.Name]
		return held, ok
	case *ast.SelectorExpr:
		held, ok := fields[value.Sel.Name]
		return held, ok
	}
	return nil, false
}

// collectPrefixes records what a name stands for - a struct field in the
// file-wide map, a local in the one the caller opened for this function.
func collectPrefixes(body ast.Node, consts map[string]string, options map[string][][]string, fields, locals map[string][]string) {
	record := func(target ast.Expr, value ast.Expr, isField bool) {
		prefix, ok := prefixOf(value, consts, options, fields, locals)
		if !ok {
			return
		}
		name, ok := nameOf(target)
		if !ok || name == "_" {
			return
		}
		if isField {
			fields[name] = prefix
			return
		}
		locals[name] = prefix
	}
	ast.Inspect(body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			for i, right := range value.Rhs {
				if i < len(value.Lhs) {
					// `x := …` is a local; `s.field = …` is a field.
					_, isField := value.Lhs[i].(*ast.SelectorExpr)
					record(value.Lhs[i], right, isField)
				}
			}
		case *ast.KeyValueExpr:
			// A STRUCT LITERAL'S FIELD NAME IS AN IDENT, not a selector, so
			// `Handler: route.Sub("handler")` looks exactly like a local unless
			// the composite literal it sits in says otherwise. It is the shape a
			// gateway's handler is reached through.
			record(value.Key, value.Value, true)
		}
		return true
	})
}

// readsUnder collects the paths one function reads.
func readsUnder(body ast.Node, consts map[string]string, options map[string][][]string, helpers map[string]bool, fields, locals map[string][]string, out map[string]bool) {
	keep := func(segments []string) {
		spelled := false
		for _, segment := range segments {
			if segment != "*" && segment != "**" {
				spelled = true
				break
			}
		}
		// A read with nothing spelled in it reads whatever its caller passed,
		// and the caller is where this test can see the path.
		if !spelled {
			return
		}
		out[strings.Join(segments, "/")] = true
	}

	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		if name, ok := call.Fun.(*ast.Ident); ok && helpers[name.Name] {
			for _, path := range pathsOf(call, consts, options) {
				keep(path)
			}
			return true
		}
		if name, ok := call.Fun.(*ast.Ident); ok && pathReaders[name.Name] && len(call.Args) > 1 {
			tail := &ast.CallExpr{Fun: call.Fun, Args: call.Args[1:], Ellipsis: call.Ellipsis}
			for _, path := range pathsOf(tail, consts, options) {
				keep(path)
			}
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		method := selector.Sel.Name
		if !readMethods[method] && !blockMethods[method] {
			return true
		}
		prefix, prefixed := prefixOf(selector.X, consts, options, fields, locals)
		for _, path := range pathsOf(call, consts, options) {
			if blockMethods[method] {
				path = append(append([]string{}, path...), "**")
			}
			if prefixed && len(prefix) > 0 {
				keep(append(append([]string{}, prefix...), path...))
				continue
			}
			keep(path)
		}
		return true
	})
}

// pathsOf is the path one call reads. More than one when an argument is a loop
// variable over a literal list; a `*` where an argument is computed, and a
// trailing `**` where the path is spread from something this cannot resolve.
func pathsOf(call *ast.CallExpr, consts map[string]string, options map[string][][]string) [][]string {
	paths := [][]string{{}}
	grow := func(choices [][]string) {
		next := make([][]string, 0, len(paths)*len(choices))
		for _, path := range paths {
			for _, choice := range choices {
				next = append(next, append(append([]string{}, path...), choice...))
			}
		}
		paths = next
	}
	one := func(segment string) [][]string { return [][]string{{segment}} }

	for i, argument := range call.Args {
		spread := call.Ellipsis.IsValid() && i == len(call.Args)-1
		switch value := argument.(type) {
		case *ast.BasicLit:
			if text, ok := stringLit(value); ok && !spread {
				grow(one(text))
				continue
			}
			grow(one("*"))
		case *ast.Ident:
			if text, ok := consts[value.Name]; ok && !spread {
				grow(one(text))
				continue
			}
			// A LOOP OVER A LIST OF PATHS IS ONE CALL SITE READING SEVERAL
			// ATTRIBUTES, spread or not: services/worker reads six of a model's
			// inference fields at one `target.String(path...)`.
			if choices, ok := options[value.Name]; ok {
				grow(choices)
				continue
			}
			grow(one("*"))
			if spread {
				paths = trailing(paths)
			}
		default:
			grow(one("*"))
			if spread {
				paths = trailing(paths)
			}
		}
	}
	return paths
}

// trailing turns the last segment of every path into "the rest of the key",
// which is what a path spread from a slice this cannot resolve reads.
func trailing(paths [][]string) [][]string {
	for i, path := range paths {
		if len(path) > 0 {
			path = path[:len(path)-1]
		}
		paths[i] = append(path, "**")
	}
	return paths
}

// loopValues is every variable a range over a literal list of paths binds, and
// the paths it can be. Both shapes the runtime uses:
//
//	for _, field := range []string{"identifier", "format"}        one segment each
//	for _, path := range [][]string{{"inference", "local", "port"}} a whole path each
func loopValues(file *ast.File, consts map[string]string) map[string][][]string {
	out := map[string][][]string{}
	ast.Inspect(file, func(node ast.Node) bool {
		loop, ok := node.(*ast.RangeStmt)
		if !ok || loop.Value == nil {
			return true
		}
		name, ok := nameOf(loop.Value)
		if !ok {
			return true
		}
		literal, ok := loop.X.(*ast.CompositeLit)
		if !ok {
			return true
		}
		var choices [][]string
		for _, element := range literal.Elts {
			switch value := element.(type) {
			case *ast.BasicLit:
				text, ok := stringLit(value)
				if !ok {
					return true
				}
				choices = append(choices, []string{text})
			case *ast.Ident:
				text, ok := consts[value.Name]
				if !ok {
					return true
				}
				choices = append(choices, []string{text})
			case *ast.CompositeLit:
				var path []string
				for _, segment := range value.Elts {
					text, ok := stringLit(segment)
					if !ok {
						return true
					}
					path = append(path, text)
				}
				if len(path) == 0 {
					return true
				}
				choices = append(choices, path)
			default:
				return true
			}
		}
		// ACCUMULATED AND NOT REPLACED: one name is the loop variable of several
		// loops in a file - `field` reads a model's two labels in one function
		// and a database's seven in another - and keeping only the last read
		// would report the first function's attributes as unread.
		out[name] = append(out[name], choices...)
		return true
	})
	return out
}

// dynamicReaders names the functions in a file that read a resource at a path
// their caller hands them, so their call sites are read as reads.
func dynamicReaders(file *ast.File) map[string]bool {
	out := map[string]bool{}
	spreads := func(body ast.Node) bool {
		found := false
		ast.Inspect(body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || !call.Ellipsis.IsValid() {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if readMethods[selector.Sel.Name] || blockMethods[selector.Sel.Name] {
				found = true
			}
			return true
		})
		return found
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.FuncDecl:
			if value.Body != nil && spreads(value.Body) {
				out[value.Name.Name] = true
			}
		case *ast.AssignStmt:
			for i, right := range value.Rhs {
				function, ok := right.(*ast.FuncLit)
				if !ok || i >= len(value.Lhs) {
					continue
				}
				if !spreads(function.Body) {
					continue
				}
				if name, ok := nameOf(value.Lhs[i]); ok {
					out[name] = true
				}
			}
		}
		return true
	})
	return out
}

func nameOf(e ast.Expr) (string, bool) {
	switch value := e.(type) {
	case *ast.Ident:
		return value.Name, true
	case *ast.SelectorExpr:
		return value.Sel.Name, true
	}
	return "", false
}

// typedFields is the compiled keys a kind's generated reader decodes AND a
// consumer of that reader names. The field-to-key map comes out of the generated
// ReadX itself, so a regenerated reader moves both halves at once.
func typedFields(t *testing.T, root, kind string) map[string]bool {
	consumers, ok := exposed[kind]
	if !ok {
		return nil
	}

	generated, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "pkg/specs/kinds.go"), nil, 0)
	require.NoError(t, err)

	reader := "Read" + strings.ToUpper(kind[:1]) + kind[1:]
	keys := map[string]string{}
	for _, declared := range generated.Decls {
		function, ok := declared.(*ast.FuncDecl)
		if !ok || function.Name.Name != reader {
			continue
		}
		ast.Inspect(function, func(node ast.Node) bool {
			assign, ok := node.(*ast.AssignStmt)
			if !ok || len(assign.Lhs) == 0 {
				return true
			}
			field, ok := assign.Lhs[0].(*ast.SelectorExpr)
			if !ok {
				return true
			}
			for _, right := range assign.Rhs {
				call, ok := right.(*ast.CallExpr)
				if !ok {
					continue
				}
				for _, path := range pathsOf(call, nil, nil) {
					keys[field.Sel.Name] = strings.Join(path, "/")
				}
			}
			return true
		})
	}
	require.NotEmpty(t, keys, "`exposed` names %s and pkg/specs/kinds.go has no %s to read its fields off", kind, reader)

	out := map[string]bool{}
	for _, consumer := range consumers {
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, consumer), nil, 0)
		require.NoError(t, err, "`exposed` names %s as a consumer of %s and it cannot be read", consumer, reader)
		named := false
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if key, ok := keys[selector.Sel.Name]; ok {
				out[key] = true
				named = true
			}
			return true
		})
		require.True(t, named,
			"`exposed` names %s as a consumer of %s and it names none of its fields - drop it, or the kind's attributes are being kept alive by a file that stopped using them", consumer, reader)
	}
	return out
}

// inlinedInto is each attribute of a kind that names a file the compile reads
// into the object, and the key it is read into.
func inlinedInto(iter *engine.Node) map[string]string {
	out := map[string]string{}
	for _, a := range iter.Attributes {
		if into := engine.InlinedInto(a); into != "" {
			out[a.Name] = into
		}
	}
	return out
}

// runtimeSpells is every non-test file under services/ and node/ that spells the
// kind as a string, which is what a runtime serving a kind cannot avoid doing:
// it names what it dispatches, or the constant it switches on.
func runtimeSpells(t *testing.T, root, kind string) []string {
	if spelled == nil {
		spelled = map[string][]string{}
		for _, top := range []string{"services", "node"} {
			err := filepath.WalkDir(filepath.Join(root, top), func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if entry.IsDir() {
					if skipped[entry.Name()] {
						return filepath.SkipDir
					}
					return nil
				}
				if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return nil
				}
				file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
				if err != nil {
					return nil
				}
				relative, _ := filepath.Rel(root, path)
				seen := map[string]bool{}
				ast.Inspect(file, func(n ast.Node) bool {
					if e, ok := n.(ast.Expr); ok {
						if text, ok := stringLit(e); ok && !seen[text] {
							seen[text] = true
							spelled[text] = append(spelled[text], filepath.ToSlash(relative))
						}
					}
					return true
				})
				return nil
			})
			require.NoError(t, err)
		}
	}
	return spelled[kind]
}

// spelled is every string the runtime spells, and where; built once.
var spelled map[string][]string
