package dfs

import (
	"io"

	"github.com/taubyte/tau/core/vm"
	peer "github.com/taubyte/tau/p2p/peer"
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
