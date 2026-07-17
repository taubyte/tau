package seer

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/afero"
)

// child returns a new Query deriving from n via op o, without mutating n.
func (n *Query) child(o op) *Query {
	return &Query{seer: n.seer, parent: n, op: o, errors: n.errors}
}

// queryFromOps rebuilds a linked Query chain from a flat op slice (WAL replay).
func queryFromOps(s *Seer, ops []op) *Query {
	q := &Query{seer: s}
	for _, o := range ops {
		q = q.child(o)
	}
	return q
}

func (n *Query) Set(value interface{}) *Query {
	return n.child(op{opType: opTypeSet, value: value, handler: opSetInYaml})
}

func (n *Query) Delete() *Query {
	return n.child(op{opType: opTypeSet, handler: opDelete})
}

func (n *Query) Get(name string) *Query {
	return n.child(op{opType: opTypeGetOrCreate, name: name, handler: opGetOrCreate})
}

// Document reinterprets the last Get as a document boundary: it replaces that op
// with a CreateDocument for the same name, parented to whatever preceded the Get.
func (n *Query) Document() *Query {
	if n.parent == nil {
		// no prior Get to convert
		errs := append(append([]error(nil), n.errors...), errors.New("can't convert root to a document"))
		return &Query{seer: n.seer, parent: n, errors: errs}
	}
	return &Query{
		seer:   n.seer,
		parent: n.parent,
		op:     op{opType: opTypeCreateDocument, name: n.op.name, handler: opCreateDocument},
		errors: n.errors,
	}
}

// return a copy of the accumulated errors
func (n *Query) Errors() []error {
	ret := make([]error, len(n.errors))
	copy(ret, n.errors)
	return ret
}

// logicalPath is the sequence of Get/Document names from root to n — the raw
// requested path, without the ".yaml" suffix the resolver appends to documents.
func (n *Query) logicalPath() []string {
	if n.parent == nil {
		return nil
	}
	p := n.parent.logicalPath()
	switch n.op.opType {
	case opTypeGetOrCreate, opTypeCreateDocument:
		return append(p, n.op.name)
	default: // Set/Delete contribute no path segment
		return p
	}
}

// opChain linearizes the ops from root to n, in execution order.
func (n *Query) opChain() []op {
	if n.parent == nil {
		return nil
	}
	return append(n.parent.opChain(), n.op)
}

// resolve walks the chain root->n, applying each op to the previous result. Read
// resolutions (write == false) are memoized per node and invalidated by the Seer
// generation counter. Caller must hold seer.lock.
func (n *Query) resolve(write bool) (path []string, node *yamlNode, err error) {
	if n.parent == nil {
		return nil, nil, nil
	}
	if !write && n.memoValid && n.memoGen == n.seer.gen {
		return n.memoPath, n.memoNode, nil
	}
	ppath, pnode, err := n.parent.resolve(write)
	if err != nil {
		return nil, nil, err
	}
	// Handlers append to the path slice; clone the parent's so sibling branches
	// resolving off the same (memoized) parent path don't corrupt each other.
	ppath = append([]string(nil), ppath...)
	path, node, err = n.op.handler(n.op, n, write, ppath, pnode)
	if err != nil {
		return nil, nil, err
	}
	if !write {
		n.memoNode, n.memoPath, n.memoGen, n.memoValid = node, path, n.seer.gen, true
	}
	return path, node, nil
}

func (n *Query) Commit() error {
	n.seer.lock.Lock()
	defer n.seer.lock.Unlock()
	if len(n.errors) > 0 {
		return fmt.Errorf("%d errors preventing commit", len(n.errors))
	}
	// A write mutates yaml.Node trees / the documents map in place; bump the
	// generation before any of that so every read memo taken earlier invalidates,
	// even if a later op in this commit fails partway through.
	n.seer.gen++
	if _, _, err := n.resolve(true); err != nil {
		return fmt.Errorf("committing failed with %s", err.Error())
	}

	// A commit resolves into exactly one document; filePath is its map key.
	if n.filePath != "" {
		n.seer.dirty[n.filePath] = struct{}{}
	}

	// Op-based WAL: persist this commit's ops so a kill before the next Sync()
	// doesn't drop the change. No-op when WAL is disabled (walPath == "").
	if err := n.seer.appendCommitWAL(n.opChain()); err != nil {
		return fmt.Errorf("wal append failed: %w", err)
	}

	return nil
}

func (n *Query) Value(dst interface{}) error {
	n.seer.lock.Lock()
	defer n.seer.lock.Unlock()
	if len(n.errors) > 0 {
		return fmt.Errorf("%d errors preventing getting value", len(n.errors))
	}

	path, doc, err := n.resolve(false)
	if err != nil {
		return fmt.Errorf("Value failed with %s", err.Error())
	}

	if doc == nil {
		//let's see if we're looking at a folder
		_path := "/" + joinPath(path)
		if st, exist := n.seer.fs.Stat(_path); exist == nil && st.IsDir() {
			// it's a folder
			dirFiles, err := afero.ReadDir(n.seer.fs, _path)
			if err != nil {
				return fmt.Errorf("parsing folder `%s` failed with %w", path, err)
			}

			_dst := make([]string, 0)
			for _, f := range dirFiles {
				if f.IsDir() {
					_dst = append(_dst, f.Name())
				} else {
					fname := f.Name()
					item := strings.TrimSuffix(fname, ".yaml")
					if item+".yaml" == fname {
						_dst = append(_dst, item)
					}
				}
			}

			switch idst := dst.(type) {
			case *interface{}:
				*idst = _dst
			case *[]string:
				*idst = _dst
			default:
				return fmt.Errorf("value of a folder can only be mapped to `*[]string` or *interface{} not `%T`", dst)
			}

			return nil
		} else {
			return fmt.Errorf("no data found for %s", path)
		}
	}

	err = doc.this.Decode(dst)
	if err != nil {
		line, column := getNodeLocation(doc.this)
		if doc.filePath != "" {
			if line > 0 && column > 0 {
				return fmt.Errorf("decode(%T) failed in file '%s' at line %d, column %d: %w", dst, doc.filePath, line, column, err)
			} else if line > 0 {
				return fmt.Errorf("decode(%T) failed in file '%s' at line %d: %w", dst, doc.filePath, line, err)
			}
			return fmt.Errorf("decode(%T) failed in file '%s': %w", dst, doc.filePath, err)
		}
		return fmt.Errorf("decode(%T) failed with %w", dst, err)
	}

	return nil
}

func (n *Query) List() ([]string, error) {
	var val interface{}
	err := n.Value(&val)
	if err != nil {
		path := "/" + joinPath(n.logicalPath())
		st, statErr := n.seer.fs.Stat(path)
		if statErr == nil && st.IsDir() {
			dirFiles, err := afero.ReadDir(n.seer.fs, path)
			if err != nil {
				return nil, fmt.Errorf("listing directory `%s` failed with %w", path, err)
			}

			out := make([]string, 0)
			for _, f := range dirFiles {
				if f.IsDir() {
					out = append(out, f.Name())
				} else if item, ok := strings.CutSuffix(f.Name(), ".yaml"); ok {
					out = append(out, item)
				}
			}
			return out, nil
		}
		return nil, fmt.Errorf("listing keys failed with %s", err)
	}

	// Empty value should be returned as nil
	if val == nil {
		return nil, nil
	}

	switch ival := val.(type) {
	case []string:
		return ival, nil
	case map[string]interface{}:
		return mapKeys(ival), nil
	case map[interface{}]interface{}:
		return mapKeys(safeInterfaceToStringKeys(ival)), nil
	default:
		return nil, fmt.Errorf("listing keys failed with %v type(%T) is not a map or a slice", val, val)
	}
}

// Location reports where the resolved value lives. On a failed resolve the
// terminal node has no location, so walk up to the deepest resolved ancestor —
// matching the old replay model where the last successful op's location stuck.
func (n *Query) location() (string, int, int) {
	for q := n; q != nil; q = q.parent {
		if q.filePath != "" || q.line != 0 || q.column != 0 {
			return q.filePath, q.line, q.column
		}
	}
	return "", 0, 0
}

func (n *Query) FilePath() string {
	fp, _, _ := n.location()
	return fp
}

func (n *Query) Line() int {
	_, line, _ := n.location()
	return line
}

func (n *Query) Column() int {
	_, _, col := n.location()
	return col
}

func (n *Query) Location() (filePath string, line int, column int) {
	return n.location()
}
