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
	opTypeGet            = 1
	opTypeCreateDocument = 2
	opTypeCreateFolder   = 3 // TODO: Either implement or delete
	opTypeSet            = 16
	opTypeGetOrCreate    = 42
)

type op struct {
	opType  int
	name    string
	value   interface{}
	handler opHandler
}

type yamlNode struct {
	parent *yaml.Node // prant
	prev   *yaml.Node // previous -- genrally contains name
	this   *yaml.Node // node with data
}

type opHandler func(this op, node *Query, path []string /*returned by previous op*/, value *yamlNode /* value passed by parent*/) ( /*path*/ []string /*value*/, *yamlNode, error)

type Query struct {
	seer          *Seer
	write         bool     // set to true by Commit() --- set to false by Value()
	requestedPath []string // is built by the Gets
	ops           []op
	errors        []error
}

type Batch struct {
	queries []*Query
}
