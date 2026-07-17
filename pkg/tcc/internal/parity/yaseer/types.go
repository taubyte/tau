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

type opHandler func(this op, node *Query, path []string /*returned by previous op*/, value *yamlNode /* value passed by parent*/) ( /*path*/ []string /*value*/, *yamlNode, error)

type Query struct {
	seer          *Seer
	write         bool     // set to true by Commit() --- set to false by Value()
	requestedPath []string // is built by the Gets
	ops           []op
	errors        []error
	filePath      string
	line          int
	column        int
}

type Batch struct {
	queries []*Query
}
