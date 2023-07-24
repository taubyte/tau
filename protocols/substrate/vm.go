package substrate

import (
	"github.com/taubyte/go-interfaces/vm"
	dfs "github.com/taubyte/vm/backend/dfs"
	"github.com/taubyte/vm/backend/file"
	httpBe "github.com/taubyte/vm/backend/url"
	loader "github.com/taubyte/vm/loaders/wazero"
	resolver "github.com/taubyte/vm/resolvers/taubyte"
	vmWaz "github.com/taubyte/vm/service/wazero"
	source "github.com/taubyte/vm/sources/taubyte"
)

func (srv *Service) startVm() (err error) {
	resolv := resolver.New(srv.tns)
	backends := []vm.Backend{dfs.New(srv.node), file.New(), httpBe.New()}
	lder := loader.New(resolv, backends...)
	src := source.New(lder)
	srv.vm = vmWaz.New(srv.ctx, src)

	return nil
}
