package test_utils

import (
	"context"
	"fmt"
	"io"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/p2p/keypair"
	peer "github.com/taubyte/tau/p2p/peer"

	"github.com/taubyte/tau/services/common"

	"github.com/taubyte/tau/pkg/vm/backend/dfs"
	"github.com/taubyte/tau/pkg/vm/backend/file"
	"github.com/taubyte/tau/pkg/vm/backend/url"
)

type testBackend struct {
	vm.Backend
	simple peer.Node
	Cid    string
}

func DFSBackend(ctx context.Context) *testBackend {
	simpleNode := peer.MockNode(ctx)

	return &testBackend{
		Backend: dfs.New(simpleNode),
		simple:  simpleNode,
	}
}

func DFSBackendWithNode(ctx context.Context) *testBackend {
	simpleNode, err := peer.New( // consumer
		ctx,
		nil,
		keypair.NewRaw(),
		common.SwarmKey(),
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11912)},
		nil,
		true,
		false,
	)
	if err != nil {
		panic(err)
	}

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

func AllBackends(ctx context.Context, injectReader io.Reader) (cid string, simpleNode peer.Node, backends []vm.Backend, err error) {
	dfsBe := DFSBackendWithNode(ctx)
	if injectReader != nil {
		if dfsBe, err = dfsBe.Inject(injectReader); err != nil {
			return
		}
	}

	return dfsBe.Cid, dfsBe.simple, []vm.Backend{HTTPBackend(), dfsBe, file.New()}, nil
}
