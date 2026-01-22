package seer

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/utils/maps"
	pathUtils "github.com/taubyte/tau/utils/path"
)

// Helper
func Fork(n *Query) *Query {
	return n.Fork()
}

// Copy a query ... the conly way to reuse a query.
func (n *Query) Fork() *Query {
	nq := &Query{
		seer:          n.seer,
		write:         n.write,
		requestedPath: make([]string, len(n.requestedPath)),
		ops:           make([]op, len(n.ops)),
		errors:        make([]error, 0),
	}

	copy(nq.requestedPath, n.requestedPath)
	copy(nq.ops, n.ops)

	return nq
}

func (n *Query) Set(value interface{}) *Query {
	n.ops = append(n.ops,
		op{
			opType:  opTypeSet,
			value:   value,
			handler: opSetInYaml,
		},
	)
	return n
}

func (n *Query) Delete() *Query {
	n.ops = append(n.ops,
		op{
			opType:  opTypeSet,
			handler: opDelete,
		},
	)
	return n
}

func (n *Query) Get(name string) *Query {
	n.requestedPath = append(n.requestedPath, name)
	n.ops = append(n.ops,
		op{
			opType:  opTypeGetOrCreate,
			name:    name,
			handler: opGetOrCreate,
		},
	)
	return n
}

func (n *Query) Document() *Query {
	if len(n.ops) == 0 {
		// should never happen actually, as you need to call get or set before
		n.errors = append(n.errors, errors.New("can't convert root to a document"))
		return n
	}

	n.write = true

	// grab path from previous
	// and delete last op
	last_op_index := len(n.ops) - 1
	name := n.ops[last_op_index].name
	n.ops = n.ops[:last_op_index]

	n.ops = append(n.ops,
		op{
			opType:  opTypeCreateDocument,
			name:    name,
			handler: opCreateDocument,
		},
	)
	return n
}

// return a copy of the Stack Error
func (n *Query) Errors() []error {
	ret := make([]error, len(n.errors))
	copy(ret, n.errors)
	return ret
}

func (n *Query) Clear() *Query {
	n.write = false
	n.ops = n.ops[:0]
	n.errors = n.errors[:0]
	return n
}

func (n *Query) Commit() error {
	n.seer.lock.Lock()
	defer n.seer.lock.Unlock()
	n.write = true
	if len(n.errors) > 0 {
		return fmt.Errorf("%d errors preventing commit", len(n.errors))
	}

	var (
		path []string  = make([]string, 0)
		doc  *yamlNode // nil when created here
		err  error
	)
	for _, op := range n.ops {
		path, doc, err = op.handler(op, n, path, doc)
		if err != nil {
			return fmt.Errorf("committing failed with %s", err.Error())
		}
	}

	return nil
}

func (n *Query) Value(dst interface{}) error {
	n.seer.lock.Lock()
	defer n.seer.lock.Unlock()
	n.write = false
	if len(n.errors) > 0 {
		return fmt.Errorf("%d errors preventing getting value", len(n.errors))
	}

	var (
		path []string  = make([]string, 0)
		doc  *yamlNode // nil when created here
		err  error
	)
	for _, op := range n.ops {
		path, doc, err = op.handler(op, n, path, doc)
		if err != nil {
			return fmt.Errorf("Value failed with %s", err.Error())
		}
	}

	if doc == nil {
		//let's see if we're looking at a folder
		_path := "/" + pathUtils.Join(path)
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
		path := "/" + pathUtils.Join(n.requestedPath)
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
		return maps.Keys(ival), nil
	case map[interface{}]interface{}:
		return maps.Keys(maps.SafeInterfaceToStringKeys(ival)), nil
	default:
		return nil, fmt.Errorf("listing keys failed with %v type(%T) is not a map or a slice", val, val)
	}
}

func (n *Query) FilePath() string {
	return n.filePath
}

func (n *Query) Line() int {
	return n.line
}

func (n *Query) Column() int {
	return n.column
}

func (n *Query) Location() (filePath string, line int, column int) {
	return n.filePath, n.line, n.column
}
