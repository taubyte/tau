package test_utils

import (
	"context"
	"io"

	peer "github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm/backend/dfs"
	"github.com/taubyte/tau/pkg/vm/backend/file"
	"github.com/taubyte/tau/pkg/vm/backend/url"
)

type testBackend struct {
	vm.Backend
	simple peer.Node
	Cid    string
}

func DFSBackend() *testBackend {
	simpleNode := peer.MockNode(context.Background())

	return &testBackend{
		Backend: dfs.New(simpleNode),
		simple:  simpleNode,
	}
}

func (t *testBackend) Inject(r io.Reader) (*testBackend, error) {
	var err error
	t.Cid, err = t.simple.AddFile(r)
	if err != nil {
		return nil, err
	}

	return t, nil
}

func HTTPBackend() vm.Backend {
	return url.New()
}

func AllBackends(injectReader io.Reader) (cid string, simpleNode peer.Node, backends []vm.Backend, err error) {
	dfsBe := DFSBackend()
	if injectReader != nil {
		if dfsBe, err = dfsBe.Inject(injectReader); err != nil {
			return
		}
	}

	return dfsBe.Cid, dfsBe.simple, []vm.Backend{HTTPBackend(), dfsBe, file.New()}, nil
}
