package interp

import (
	"cmp"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/afero"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// inline.go reads the files a document POINTS AT into the document.
//
// # Why a compile has to do this
//
// An attribute marked Inlined names a file. A path is only useful to something
// that can open it, and nothing downstream of a compile can: a node holds the
// compiled object and an artifact store, never the repository. So the content is
// read here or it is read nowhere — which is exactly what happened to a zone's
// `file`, declared and decoded and consumed by nobody while the server went on
// answering from a list of strings in yaml.
//
// # Why it is not a lowering
//
// The lowering vocabulary composes values the document already holds — Sets takes
// Attr, Text and Concat. This reaches OUTSIDE the document for a value it does not
// have, which no term in that vocabulary can express and none should: a lowering
// is a rewrite of what was written, and this is a read.
//
// # Why the compile can see the file at all
//
// overlay() copies the whole repository into the compile's view, so a file beside
// the configuration is already in hand. A file that is NOT there is the mount
// protocol's case, not an error: see Mounts.
//
// # It speaks no vocabulary
//
// Which attributes carry content and which key that content lands under are the
// SCHEMA's answers, read off the annotation. Nothing here knows what a zone is.

// ErrInline is a pointed-at file that could not be read, or a document that says
// the same thing twice.
var ErrInline = errors.New("interp: that file could not be read into the object")

// inlineFiles reads every Inlined attribute's file into the object.
//
// ORDERED, because two resources naming the same unreadable file must produce the
// same first refusal whichever order a map happened to iterate in.
func (c *Compiler) inlineFiles(obj Object) error {
	var failed error
	eachInlined(c.compileRoot, obj, func(kind, id, name, at string, mark inlineMark, path string, _ map[string]any) {
		if failed != nil {
			return
		}

		// THE FILE WINS, AND records IS RECOMPUTED EVERY COMPILE.
		//
		// This used to refuse a document that wrote both, on the grounds that
		// there is no rule for which one wins that is not a surprise. There
		// is: the file is where the author put the records, so the key they
		// land under is DERIVED and a compile overwrites it. The refusal also
		// could not survive a round trip -- decompiling a compiled zone yields
		// a document carrying both, and a compile cannot tell that from a
		// person writing both.
		lines, err := readLines(c.inlineFS(), path)
		if err != nil {
			// NOT REACHABLE IS NOT A REFUSAL. A file this compile cannot see
			// is the mount protocol's case -- it lives in a repository nobody
			// has fetched -- and the whole point of that protocol is that tcc
			// reports what it needs rather than failing. Left for the check
			// that reports an unanswered mount, exactly as a delegated block
			// that was never filled is left.
			//
			// A compile that refused here would also refuse every project
			// whose zone file is somewhere else, which is the shape this is
			// for.
			return
		}

		// WRITTEN THROUGH THE OBJECT AND NOT INTO THE FLATTENED MAP. Flat()
		// builds a fresh map every call, so a value written into it goes into
		// a copy that is thrown away -- which the first shape here did,
		// silently, while the selector read it back correctly.
		held, err := resourceIn(c.compileRoot, obj, at, kind, id)
		if err != nil {
			failed = fmt.Errorf("%w: %s/%s: %v", ErrInline, kind, name, err)
			return
		}
		// SET ON THE RESOURCE, NOT ON ITS PARENT. Selector.Set writes the
		// NAMED CHILD -- so setting through it replaced the whole resource
		// with an object holding one key, and a zone came out of the compile
		// with its records and no fqdn. Object.Set adds a field to what is
		// already there.
		held.Set(mark.into, lines)
	})
	return failed
}

// InlinedFiles is the inverse of the compile's read: every file an Inlined attribute
// names, with the content the compile carried for it, keyed by the repository path the
// attribute holds. It is what a decompile writes back beside the documents, because
// the document keeps only the path and a repository holding a path to nothing does not
// compile to what it came from.
//
// A resource whose content key is absent names a file this compile never read -- the
// mount protocol's case -- and so has nothing to give back; it is left out rather than
// written as an empty file that would then be served as an empty zone.
//
// Asked of the COMPILED object, before a decompile rewrites it: the walk is keyed by
// id, and the decompile's last act is to key every resource by name instead.
func InlinedFiles(root *engine.Node, obj Object) map[string][]string {
	files := map[string][]string{}
	eachInlined(root, obj, func(_, _, _, _ string, mark inlineMark, path string, res map[string]any) {
		held, ok := res[mark.into].([]any)
		if !ok {
			return
		}
		lines := make([]string, 0, len(held))
		for _, l := range held {
			s, _ := l.(string)
			lines = append(lines, s)
		}
		files[path] = lines
	})
	return files
}

// eachInlined walks every resource and hands over each Inlined attribute that names a
// file. The compile and the decompile answer "which attributes carry a file, and on
// which resource" with this one walk, so the two cannot disagree about it.
func eachInlined(root *engine.Node, obj Object, fn func(kind, id, name, at string, mark inlineMark, path string, res map[string]any)) {
	flat := obj.Flat()
	scope := flat
	if inner, ok := flat[ProjectKey].(map[string]any); ok {
		scope = inner
	}
	walkResources(root, scope, "", func(kind string, iter *engine.Node, id, name string, res map[string]any, at string) {
		for _, mark := range inlinedIn(iter) {
			if path, _ := res[mark.attribute].(string); path != "" {
				fn(kind, id, name, at, mark, path, res)
			}
		}
	})
}

// inlineFS is the filesystem a pointed-at file is read from: the merged view when
// mounts were answered, the project's own tree otherwise.
//
// ALWAYS ROOTED, even at "/". A repository path is relative, and a MemMapFs does
// not resolve a relative name against anything -- it is simply a different key --
// so an unrooted in-memory view reads nothing, which is exactly what recompiling a
// decompiled project did. Rooting also keeps a path from reading outside the tree.
func (c *Compiler) inlineFS() afero.Fs {
	return afero.NewBasePathFs(c.baseFs, cmp.Or(c.basePath, "/"))
}

// inlineMark is one annotated attribute: where the path is written, and where its
// content belongs.
type inlineMark struct {
	attribute string
	into      string
}

// inlinedIn is every Inlined attribute under one kind's iterator.
//
// READ OFF THE NODE THE WALK ALREADY HANDS US, which is what delegableTop does for
// the mount protocol: the schema is the only thing that knows which attributes
// carry content, and asking it per resource costs a walk of one kind's attributes.
//
// ORDERED, so two attributes on one resource are read in one order and a failure
// names the same one twice.
func inlinedIn(iter *engine.Node) []inlineMark {
	if iter == nil {
		return nil
	}

	var marks []inlineMark
	var walk func(n *engine.Node)
	walk = func(n *engine.Node) {
		for _, a := range n.Attributes {
			into := engine.InlinedInto(a)
			if into == "" {
				continue
			}
			name := a.Name
			if cut := strings.Index(name, "/"); cut > 0 {
				name = name[:cut]
			}
			marks = append(marks, inlineMark{attribute: name, into: into})
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(iter)

	sort.Slice(marks, func(i, j int) bool { return marks[i].attribute < marks[j].attribute })
	return marks
}

// readLines is the file, split into the lines a consumer reads.
//
// LINES AND NOT BYTES, because every consumer of an inlined file in this
// vocabulary reads lines — a zone file is parsed one record at a time on purpose,
// so that one syntax error costs one record instead of a whole zone. Carrying the
// bytes would make each consumer split them again and invite two answers about
// what a line is.
//
// NOTHING IS STRIPPED. Comments and blank lines travel, because the thing that
// parses these is the same parser the author's own tooling uses and it already
// knows what to ignore — dropping them here would mean a compile deciding what a
// zone file says.
func readLines(fs afero.Fs, path string) ([]any, error) {
	body, err := afero.ReadFile(fs, path)
	if err != nil {
		return nil, err
	}

	raw := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	// A trailing newline is not a line.
	if n := len(raw); n > 0 && raw[n-1] == "" {
		raw = raw[:n-1]
	}

	lines := make([]any, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, line)
	}
	return lines, nil
}

// resourceIn selects one resource in the object: its scope chain, then its kind,
// then its id.
//
// The scope chain is NAMES, as walkResources hands it over, while a compiled
// container is keyed by its id under its group like any other resource. Each segment
// is the container whose name field holds it.
//
// BY ID AND NOT BY NAME for the resource itself. A resource's name comes from the
// file it was written in, so the document need not carry a `name` key at all -- a
// selector keyed on one matches nothing while looking correct.
func resourceIn(root *engine.Node, obj Object, at, kind, id string) (Object, error) {
	held := obj
	// The same wrapper Flat() is unwrapped for: a compiled object nests everything
	// under the project.
	if inner, err := obj.Child(ProjectKey).Object(); err == nil {
		held = inner
	}
	containers := ContainerKeys(root)
	_, nameField := engine.IdentityOf(root)
	for _, segment := range strings.Split(at, "/") {
		if segment == "" {
			continue
		}
		inner := containerNamed(held, containers, nameField, segment)
		if inner == nil {
			return nil, fmt.Errorf("scope %q: does not exist", at)
		}
		held = inner
	}
	byID, err := held.Child(kind).Object()
	if err != nil {
		return nil, fmt.Errorf("kind %q: %w", kind, err)
	}
	// REFUSED RATHER THAN CREATED. A selector for an id that is not there writes a
	// brand new resource, which is worse than failing: the compile succeeds and the
	// object carries a resource nobody declared.
	sel := byID.Child(id)
	if !sel.Exists() {
		return nil, fmt.Errorf("no %s with id %s", kind, id)
	}
	return sel.Object()
}

func containerNamed(held Object, containers []string, nameField, name string) Object {
	for _, key := range containers {
		group, err := held.Child(key).Object()
		if err != nil {
			continue
		}
		for _, id := range group.Children() {
			inst, err := group.Child(id).Object()
			if err != nil {
				continue
			}
			if got, _ := inst.Get(nameField).(string); got == name {
				return inst
			}
		}
	}
	return nil
}
