package dfs

import (
	"io"

	peer "github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/core/vm"
)

var _ vm.Backend = &backend{}

type backend struct {
	node peer.Node
}

type zWasmReadCloser struct {
	dag        io.ReadCloser
	unCompress io.ReadCloser
}

type zipReadCloser struct {
	io.ReadCloser
	parent io.Closer
}
