package test_utils

import (
	"context"
	"io"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/p2p/peer"
	loaders "github.com/taubyte/tau/pkg/vm/loaders/wazero"
	tns "github.com/taubyte/tau/services/tns/mocks"
)

func Loader(ctx context.Context, injectReader io.Reader) (cid string, loader vm.Loader, resolver vm.Resolver, tns tns.MockedTns, simple peer.Node, err error) {
	var backends []vm.Backend
	cid, simple, backends, err = AllBackends(ctx, injectReader)
	if err != nil {
		return
	}

	MockConfig.Cid = cid

	tns, resolver, err = Resolver(false)
	if err != nil {
		return
	}

	loader = loaders.New(resolver, backends...)

	return
}
