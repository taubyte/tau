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

// inline.go reads the files a document points at into the document: nothing
// downstream of a compile can open the repository, so the content is read here
// or nowhere. It is a read, not a lowering. overlay() puts the repository in the
// compile's view; a file not there is the mount protocol's case. Which attributes
// carry content is the schema's answer.

// ErrInline is a pointed-at file that could not be read, or a document that says
// the same thing twice.
var ErrInline = errors.New("interp: that file could not be read into the object")

// inlineFiles reads every Inlined attribute's file into the object, in a fixed
// order so the first refusal is the same every run.
func (c *Compiler) inlineFiles(obj Object) error {
	var failed error
	eachInlined(c.compileRoot, obj, func(kind, id, name, at string, mark inlineMark, path string, _ map[string]any) {
		if failed != nil {
			return
		}

		// The file wins, and the content key is recomputed every compile: it is derived.
		// A decompiled zone carries both, and a compile cannot tell that from a person
		// writing both.
		lines, err := readLines(c.inlineFS(), path)
		if err != nil {
			// Not reachable is not a refusal: a file in a repository nobody fetched is the
			// mount protocol's case, reported by the unanswered-mount check.
			return
		}

		// Written through the object, not the flattened map: Flat() returns a fresh copy.
		held, err := resourceIn(c.compileRoot, obj, at, kind, id)
		if err != nil {
			failed = fmt.Errorf("%w: %s/%s: %v", ErrInline, kind, name, err)
			return
		}
		// Object.Set adds a field; Selector.Set would replace the whole resource with a
		// one-key object.
		held.Set(mark.into, lines)
	})
	return failed
}

// InlinedFiles is every file an Inlined attribute names, with the content the
// compile carried, keyed by repository path: what a decompile writes back. A
// resource whose content key is absent is left out rather than written empty.
// Asked of the compiled object, which is keyed by id.
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

// inlineFS is where a pointed-at file is read: the merged view when mounts were
// answered, the project's tree otherwise. Always rooted, since a MemMapFs
// resolves no relative name, and rooting keeps a path inside the tree.
func (c *Compiler) inlineFS() afero.Fs {
	return afero.NewBasePathFs(c.baseFs, cmp.Or(c.basePath, "/"))
}

// inlineMark is one annotated attribute: where the path is written, and where its
// content belongs.
type inlineMark struct {
	attribute string
	into      string
}

// inlinedIn is every Inlined attribute under one kind's iterator, in order.
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

// readLines is the file as lines, since every consumer reads lines (a zone file
// is parsed a record at a time). Nothing is stripped: the consumer's parser
// knows what to ignore.
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

// resourceIn selects one resource: its scope chain of container names, then its
// kind, then its id. By id: a resource's name comes from its file, so the
// document may carry no `name` key.
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
