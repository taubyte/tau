package seer

import (
	"sync"

	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
)

type Document interface{}

type Seer struct {
	fs        afero.Fs
	lock      sync.Mutex
	documents map[string]*yaml.Node
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
	value   interface{}
	handler opHandler
}

type yamlNode struct {
	parent   *yaml.Node // prant
	prev     *yaml.Node // previous -- genrally contains name
	this     *yaml.Node // node with data (Line and Column fields accessible via this.Line and this.Column)
	filePath string     // path to the YAML file this node came from
}

type opHandler func(this op, node *Query, path []string /*returned by previous op*/, value *yamlNode /* value passed by parent*/) ( /*path*/ []string /*value*/, *yamlNode, error)

type Query struct {
	seer          *Seer
	write         bool     // set to true by Commit() --- set to false by Value()
	requestedPath []string // is built by the Gets
	ops           []op
	errors        []error
	filePath      string // path to the current YAML file (empty if not in a document)
	line          int    // line number in the current YAML file (0 if not available)
	column        int    // column number in the current YAML file (0 if not available)
}

type Batch struct {
	queries []*Query
}
