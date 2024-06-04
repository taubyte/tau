package substrate

import (
	"github.com/taubyte/tau/core/vm"
	dfs "github.com/taubyte/tau/pkg/vm/backend/dfs"
	"github.com/taubyte/tau/pkg/vm/backend/file"
	httpBe "github.com/taubyte/tau/pkg/vm/backend/url"
	loader "github.com/taubyte/tau/pkg/vm/loaders/wazero"
	resolver "github.com/taubyte/tau/pkg/vm/resolvers/taubyte"
	vmWaz "github.com/taubyte/tau/pkg/vm/service/wazero"
	source "github.com/taubyte/tau/pkg/vm/sources/taubyte"
)

func (srv *Service) startVm() (err error) {
	resolv := resolver.New(srv.tns)
	backends := []vm.Backend{dfs.New(srv.node), file.New(), httpBe.New()}
	lder := loader.New(resolv, backends...)
	src := source.New(lder)
	srv.vm = vmWaz.New(srv.ctx, src)

	return nil
}
