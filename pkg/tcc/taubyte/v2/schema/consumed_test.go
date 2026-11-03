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

// Holds the vocabulary to its readers: every declared attribute must be read
// somewhere, or excused in `unread`. An attribute is read by a typed read
// through pkg/specs' generated readers, a spelled read at its compiled key, a key
// the vocabulary hands the reader (identity, access entry, answers-at block), a
// generated struct named in `exposed`, or a nested group's field where it sits.
// The audit, the editor and the lowering are not readers. Matching errs toward
// read, but patterns are anchored, never suffix-matched.

// An excuse is HANDLED (used where the scan cannot see), INERT (asks nothing of
// the cloud), NOT SERVED BY THIS BUILD (a whole concept this build lacks), or a
// preparatory kind with no runtime. A runnable kind ignoring a declaration is
// never excusable: its runtime must apply it or refuse it by name.
type class int

const (
	handled class = iota + 1
	inert
	notServed
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

	packageScopeConsumed = excuse{handled, "the namespace half of a `package: {name: …}` reference. The compile resolved the name to the package's ID, so the scope was consumed resolving it."}

	dockerfileRefused = excuse{handled, "where a package's Dockerfile is and which stage is its image. The build service refuses a dockerfile build by name (ErrNotBuiltHere): storing the image it makes needs a registry this cloud lacks, so neither field is ever served weaker than written."}

	inputsRefused = excuse{handled, "another package's artifact a build reads. The build service refuses a package with inputs by name (ErrNotBuiltHere) rather than building it without what it would embed."}

	cacheDisposable = excuse{notServed, "directories a step keeps for its next build. A cache is disposable by definition — a build never depends on finding one — and this build keeps none, so every step starts clean: slower, never wrong."}

	watchEveryPush = excuse{notServed, "which paths' change rebuilds a package. This build rebuilds every package a pushed repository backs on every push, a superset of what watch would select: slower, never stale."}

	proxyCertScopeConsumed = excuse{handled, "the namespace half of a proxy's certificate secret. The compile resolved the name beside it to the secret's ID."}

	deadLetterScopeConsumed = excuse{handled, "the namespace half of a dead-letter reference. The compile resolved the name to another queue's ID, so the scope was consumed resolving it."}

	streamingStorageRefused = excuse{handled, "the lifetime of a streaming storage's entries. worker refuses any storage whose type is not `object` by name (unservableKind), so this branch's only field is never served weaker than written — it is never served."}

	oneRegion = excuse{notServed, "where a resource or a scope is placed across regions. Placement here is one region and reads no region, so the block is accepted and asks this build for nothing."}

	replicationRaftOnly = excuse{handled, "whether a cache keeps a copy on every peer. The vocabulary says it is only a question under raft consensus, because crdt and gossip both put a copy on every peer whatever it says - and worker refuses a raft cache by name (unservableKind), so the one branch where this field decides anything is never served."}

	externalStreamRefused = excuse{handled, "where somebody else's broker is and how to reach it: its host, its tls and its credentials, which only a kafka or nats stream declares. worker refuses a stream on any engine but tau's by name (unservableKind), so none of them is ever served weaker than written."}

	agentLowered = excuse{handled, "consumed by the agent's lowering: limits/timeout and limits/memory become the loop function's execution/timeout and execution/memory, and the memory block becomes a vector database's dimensions and size. The interpreter keeps its lowering terms unexported, so this test cannot see that consumption."}

	websiteLowered  = excuse{handled, "consumed by the website's lowering: host/* and certificate/* become its gateway's, and serve/* the package handler's index, mode and not_found, which the gateway reads. The interpreter keeps its lowering terms unexported, so this test cannot see that consumption."}
	workflowLowered = excuse{handled, "consumed by the workflow's lowering: a step's retry budget becomes its queue's max_attempts, delay and retry/*, `on_error` becomes its dead-letter queue, `call/*` becomes the step driver's grant on the function it calls, and `start` becomes the entry record worker reads at entry/start/*. The interpreter keeps its lowering terms unexported, so this test cannot see that consumption."}
)

// preparatoryKinds are the kinds with NO RUNTIME AT ALL, and why. Every
// declaration on one is ignored and may be, because nothing runs the kind to
// ignore it — checked, not trusted: see runtimeSpells.
var preparatoryKinds = map[string]string{
	"agent": "nothing runs an agent's loop. The lowering produces a function with `source: {builtin: agent}`, and no service or node dispatches that builtin — so which model it answers with, what it is told, and how far it may go reach nothing.",
}

// unread is every declared attribute nothing reads, and why; `*` excuses it on
// every kind. An entry no longer needed fails too.
var unread = map[string]excuse{
	"*/tags":              noTagReader,
	"*/package/namespace": packageScopeConsumed,

	"package/build/dockerfile/path":    dockerfileRefused,
	"package/build/dockerfile/target":  dockerfileRefused,
	"package/build/inputs/*/name":      inputsRefused,
	"package/build/inputs/*/namespace": inputsRefused,
	"package/build/steps/*/cache":      cacheDisposable,
	"package/watch":                    watchEveryPush,

	"secret/rotation": advisoryRotation,

	"*/regions/policy":  oneRegion,
	"*/regions/in":      oneRegion,
	"*/regions/main":    oneRegion,
	"*/regions/standby": oneRegion,

	"sync/queue/dead_letter/namespace":   deadLetterScopeConsumed,
	"proxy/certificate/secret/namespace": proxyCertScopeConsumed,

	"cache/replicated":             replicationRaftOnly,
	"storage/streaming/ttl":        streamingStorageRefused,
	"stream/credentials/name":      externalStreamRefused,
	"stream/credentials/namespace": externalStreamRefused,
	"stream/host":                  externalStreamRefused,
	"stream/tls":                   externalStreamRefused,

	"agent/limits/timeout":            agentLowered,
	"agent/limits/memory":             agentLowered,
	"agent/memory/size":               agentLowered,
	"agent/memory/embedder/name":      agentLowered,
	"agent/memory/embedder/namespace": agentLowered,

	"website/host/kind":                    websiteLowered,
	"website/host/name":                    websiteLowered,
	"website/host/zone/in/name":            websiteLowered,
	"website/host/zone/in/namespace":       websiteLowered,
	"website/certificate/source":           websiteLowered,
	"website/certificate/secret/name":      websiteLowered,
	"website/certificate/secret/namespace": websiteLowered,
	"website/serve/index":                  websiteLowered,
	"website/serve/mode":                   websiteLowered,
	"website/serve/not_found":              websiteLowered,

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

// readers names every non-test file reading a compiled resource and the kinds it
// reads; `*` is every kind. Attribution matters: a proxy's `certificate/source`
// read must not count for a gateway's.
var readers = map[string][]string{
	// Source() and Build() answer what a build needs of any kind.
	"pkg/specs/project.go": everyKind,

	// services/worker resolves a target of every kind onto a grant, which is
	// what carries an author's declarations to the tier that runs it.
	"services/worker/resolve.go": everyKind,
	// unserved.go is resolve's arm for what a workload declares about itself:
	// carried to the tier that honours it, or refused by name.
	"services/worker/unserved.go": everyKind,
	// schedule.go is the cron arm of the same service.
	"services/worker/schedule.go": {"cron"},

	// The build service makes a package's artifact, the only kind that builds.
	"services/build/artifact/artifact.go": {"package"},

	// The two services that read a DECLARATION rather than the grant, each for
	// its own kind - see the file docs, which say why.
	"services/database/declarations.go":   {"database"},
	"services/primitives/declarations.go": {"sync"},

	// A gateway's server. bus.go is the arm that serves a bus reached through
	// one, and it reads the bus.
	"services/gateway/files.go":   {"gateway"},
	"services/gateway/gateway.go": {"gateway"},
	"services/gateway/policy.go":  {"gateway"},
	"services/gateway/route.go":   {"gateway"},
	"services/gateway/bus.go":     {"bus"},
	// tls.go is which certificate answers a gateway's name: the node binds
	// the listener, and the gateway is asked per handshake.
	"services/gateway/tls.go": {"gateway"},

	// A proxy's. It terminates its own TLS — it IS its socket, where a
	// gateway's is the node's.
	"services/proxy/proxy.go":  {"proxy"},
	"services/proxy/route.go":  {"proxy"},
	"services/proxy/shape.go":  {"proxy"},
	"services/proxy/socket.go": {"proxy"},
	"services/proxy/limits.go": {"proxy"},
	"services/proxy/tls.go":    {"proxy"},

	// A zone's own server, which takes the generated Zone it serves.
	"node/zone/zone.go": {"zone"},
}

var everyKind = []string{"*"}

// exposed names generated structs reached through pkg/specs accessors, which the
// typed scan does not follow, and the files consuming them.
var exposed = map[string][]string{
	// Project.Zones() is the one accessor in pkg/specs that hands a generated
	// struct out, and these two are what read it.
	"zone": {"node/zone/zone.go", "services/build/deploy/deploy.go"},
	// A workload runs its package, which the worker reads by its generated reader.
	"package": {"services/worker/resolve.go", "services/worker/unserved.go", "services/build/artifact/artifact.go"},
}

func TestEveryDeclaredAttributeIsReadBySomething(t *testing.T) {
	root := repoRoot(t)
	found := scan(t, root)

	// The table is held to the tree both ways: an unclassified reader counts for no
	// kind, and a classified file that stopped reading keeps a kind alive.
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

	// A preparatory kind that has a runtime is no longer preparatory.
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

	// An excuse nothing needs has expired and must be taken out.
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

// reaches reports whether any pattern addresses key. heads is every first segment
// any kind declares: a pattern whose first segment is no attribute is a nested
// group (`variables`, `ports`, `entry`) and is matched on its tail too; one under
// a block is not, so `certificate/secret/name` is not a read of `name`.
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

// scan is every non-test file that reads a compiled resource and the paths it
// reads. A file must speak pkg/specs: the CLI's `ctx.String("name")` has the same
// shape on another receiver.
func scan(t *testing.T, root string) map[string]map[string]bool {
	spelled, typed := scanReads(t, root)
	out := map[string]map[string]bool{}
	for _, reads := range []map[string]map[string]bool{spelled, typed} {
		for file, paths := range reads {
			if out[file] == nil {
				out[file] = map[string]bool{}
			}
			for path := range paths {
				out[file][path] = true
			}
		}
	}
	return out
}

// scanReads splits scan into spelled and typed reads; generated_readers_test.go
// holds the spelled half to what nothing generated covers.
func scanReads(t *testing.T, root string) (spelledReads, typedReads map[string]map[string]bool) {
	out := map[string]map[string]bool{}
	typedOut := map[string]map[string]bool{}
	types := generatedTypes(t, root)
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
		typed := typedReadsIn(file, typedPackageOf(dir, types), types)
		if len(paths) == 0 && len(typed) == 0 {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		require.NoError(t, err)
		if len(paths) > 0 {
			out[filepath.ToSlash(relative)] = paths
		}
		if len(typed) > 0 {
			typedOut[filepath.ToSlash(relative)] = typed
		}
		return nil
	})
	require.NoError(t, err)
	return out, typedOut
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

// readsIn is every path one file reads, as patterns. Beyond literal arguments it
// follows:
//
//	prefixes  `route := r.Sub("route")` then `route.Sub("handler").String("name")`
//	helpers   `value := func(path ...string) int { ... }` moves the literal to the call
//	loops     `for _, field := range []string{"identifier", "format"}`
func readsIn(file *ast.File, consts map[string]string) map[string]bool {
	options := loopValues(file, consts)
	helpers := dynamicReaders(file)

	// Field prefixes are file-scoped, local ones are not: a field is filled in one
	// function and read in another, while a leaked local `v` would credit a
	// variable's `secret/name` to the resource.
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
			// A struct literal's field name is an ident, so `Handler: route.Sub("handler")`
			// only reads as a field through its composite literal.
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
			// A loop over a list of paths is one call site reading several attributes.
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
		// Accumulated, not replaced: one name can be the loop variable of several loops
		// in a file.
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
	keys := generatedKeys(generated, reader)
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
			// A BLOCK'S FIELD IS NAMED THROUGH THE BLOCK — zone.Resolver.Name —
			// so the selector is matched qualified, and bare for a kind's own.
			names := []string{selector.Sel.Name}
			if outer, ok := selector.X.(*ast.SelectorExpr); ok {
				names = append(names, outer.Sel.Name+"."+selector.Sel.Name)
			}
			for _, name := range names {
				if key, ok := keys[name]; ok {
					out[key] = true
					named = true
				}
			}
			return true
		})
		require.True(t, named,
			"`exposed` names %s as a consumer of %s and it names none of its fields - drop it, or the kind's attributes are being kept alive by a file that stopped using them", consumer, reader)
	}
	return out
}

// generatedKeys is every compiled key a generated reader decodes, by the field
// a consumer names it through: `Fqdn` for a kind's own field, `Resolver.Name`
// for one inside a block, and the same for an entry's. It follows the reader
// into the block and entry readers it calls, carrying the prefix an entry is
// read under (`r.Sub("ports", name)`, `range r.List("steps")`).
func generatedKeys(file *ast.File, reader string) map[string]string {
	funcs := map[string]*ast.FuncDecl{}
	for _, declared := range file.Decls {
		if function, ok := declared.(*ast.FuncDecl); ok {
			funcs[function.Name.Name] = function
		}
	}
	literal := func(call *ast.CallExpr) []string {
		var out []string
		for _, arg := range call.Args {
			if text, ok := stringLit(arg); ok {
				out = append(out, text)
			}
		}
		return out
	}

	keys := map[string]string{}
	var walk func(name string, prefix []string, qualifier string, depth int)
	walk = func(name string, prefix []string, qualifier string, depth int) {
		function, ok := funcs[name]
		if !ok || depth > 8 {
			return
		}
		lists := map[string][]string{}
		ast.Inspect(function, func(node ast.Node) bool {
			if loop, ok := node.(*ast.RangeStmt); ok {
				if call, ok := loop.X.(*ast.CallExpr); ok {
					if value, ok := loop.Value.(*ast.Ident); ok {
						lists[value.Name] = literal(call)
					}
				}
			}
			assign, ok := node.(*ast.AssignStmt)
			if !ok || len(assign.Lhs) == 0 {
				return true
			}
			target := assign.Lhs[0]
			if index, ok := target.(*ast.IndexExpr); ok {
				target = index.X
			}
			field, ok := target.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			for _, right := range assign.Rhs {
				ast.Inspect(right, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					callee, ok := call.Fun.(*ast.Ident)
					if ok && strings.HasPrefix(callee.Name, "Read") && len(call.Args) == 1 {
						inner := prefix
						switch arg := call.Args[0].(type) {
						case *ast.CallExpr:
							inner = append(append([]string{}, prefix...), literal(arg)...)
						case *ast.Ident:
							if under, ok := lists[arg.Name]; ok {
								inner = append(append([]string{}, prefix...), under...)
							}
						}
						walk(callee.Name, inner, qualifier+field.Sel.Name+".", depth+1)
						return false
					}
					method, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					// A PRESENCE TEST IS NOT A VALUE: Declared says a block was
					// written, and naming it reads none of the block's fields.
					if method.Sel.Name == "Has" {
						return false
					}
					for _, path := range pathsOf(call, nil, nil) {
						key := strings.Join(append(append([]string{}, prefix...), path...), "/")
						qualified := strings.Split(qualifier+field.Sel.Name, ".")
						keys[strings.Join(qualified, ".")] = key
						if n := len(qualified); n > 2 {
							// What a consumer can spell in one selector chain.
							keys[strings.Join(qualified[n-2:], ".")] = key
						}
					}
					return false
				})
			}
			return true
		})
	}
	walk(reader, nil, "", 0)
	return keys
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
