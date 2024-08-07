package function

import (
	"github.com/taubyte/tau/core/services/substrate/components"
	"github.com/taubyte/tau/core/vm"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

const WasmMemorySizeLimit = uint64(vm.MemoryPageSize) * uint64(vm.MemoryLimitPages)

func (f *Function) Service() components.ServiceComponent {
	return f.srv
}

func (f *Function) Config() *structureSpec.Function {
	return &f.config
}

func (f *Function) Commit() string {
	return f.commit
}

func (f *Function) Branch() string {
	return f.branch
}

func (f *Function) Matcher() components.MatchDefinition {
	return f.matcher
}

func (f *Function) Id() string {
	return f.config.Id
}

func (f *Function) Ready() error {
	if !f.readyDone {
		<-f.readyCtx.Done()
	}

	return f.readyError
}

func (f *Function) CachePrefix() string {
	return f.matcher.Host
}

func (f *Function) Application() string {
	return f.application
}

func (f *Function) AssetId() string {
	return f.assetId
}

func (f *Function) Project() string {
	return f.project
}

func (f *Function) IsProvisioned() bool {
	return f.provisioned
}
