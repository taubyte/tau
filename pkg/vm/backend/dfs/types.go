package dfs

import (
	"io"

	"golang.org/x/sync/singleflight"

	"github.com/taubyte/tau/core/vm"
	peer "github.com/taubyte/tau/p2p/peer"
)

var _ vm.Backend = &backend{}

type backend struct {
	node  peer.Node
	cache *moduleCache
	group singleflight.Group
}

type zWasmReadCloser struct {
	dag        io.ReadCloser
	unCompress io.ReadCloser
}

type zipReadCloser struct {
	io.ReadCloser
	parent io.Closer
}
