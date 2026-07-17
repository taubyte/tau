package seer

import (
	"sync"

	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
)

type Document any

type Seer struct {
	fs        afero.Fs
	lock      sync.Mutex
	documents map[string]*yaml.Node
	// dirty is the set of document paths mutated since the last Sync.
	// Sync only re-encodes and rewrites these, leaving read-only cached
	// documents untouched (both a perf win and so Sync never reformats a
	// file the caller only read).
	dirty map[string]struct{}
	// gen is bumped on every write (and on loading a new document) to invalidate
	// stale read-resolution memos held by Query nodes.
	gen uint64
	// walPath is the file (relative to the configured FS root) where
	// Sync() stages a write-ahead log entry before touching data
	// files. Empty disables WAL entirely — the default.
	walPath string
}

const (
	opTypeGet = iota
	opTypeCreateDocument
	opTypeCreateFolder
	opTypeSet
	opTypeGetOrCreate
)

type op struct {
	opType  int
	name    string
	value   any
	handler opHandler
}

type yamlNode struct {
	parent   *yaml.Node // parent
	prev     *yaml.Node // previous -- genrally contains name
	this     *yaml.Node // node with data
	filePath string     // path to the YAML file this node came from
}

// write is threaded explicitly (not read off the Query) because a Query is now
// immutable and shared across branches — the terminal Value/Commit decides it.
type opHandler func(this op, node *Query, write bool, path []string /*returned by previous op*/, value *yamlNode /* value passed by parent*/) ( /*path*/ []string /*value*/, *yamlNode, error)

// Query is an immutable node in a resolution chain: it holds the op that derives
// it from its parent, never mutating either. Get/Set/Delete/Document return a new
// Query; Value/Commit resolve the chain root->leaf. Sharing a Query across
// branches is safe, which is why Fork() is a no-op.
type Query struct {
	seer   *Seer
	parent *Query // nil for the root query
	op     op     // op linking parent -> this (zero value when parent == nil)
	errors []error

	// memoized READ resolution, invalidated when memoGen != seer.gen.
	// Writes never read or set this. Guarded by seer.lock (via resolve).
	memoNode  *yamlNode
	memoPath  []string
	memoGen   uint64
	memoValid bool

	// location of the value this node resolved to (set during resolve)
	filePath string
	line     int
	column   int
}

type Batch struct {
	queries []*Query
}
