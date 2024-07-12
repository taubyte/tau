package backend

import (
	"errors"

	goHttp "net/http"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/vm/backend/dfs"
	"github.com/taubyte/tau/pkg/vm/backend/file"
	"github.com/taubyte/tau/pkg/vm/backend/url"
)

// New returns all available backends
func New(node peer.Node, httpClient goHttp.Client) ([]vm.Backend, error) {
	if node == nil {
		return nil, errors.New("node is nil")
	}

	return []vm.Backend{dfs.New(node), url.New()}, nil
}

func NewDev(node peer.Node, httpClient goHttp.Client) ([]vm.Backend, error) {
	if node == nil {
		return nil, errors.New("node is nil")
	}

	return []vm.Backend{dfs.New(node), file.New(), url.New()}, nil
}
