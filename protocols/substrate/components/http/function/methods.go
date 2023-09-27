package function

import (
	"fmt"

	"github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/go-interfaces/vm"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/tau/clients/p2p/seer/usage"
	"github.com/taubyte/tau/protocols/substrate/components/common"
)

const WasmMemorySizeLimit = uint64(vm.MemoryPageSize) * uint64(vm.MemoryLimitPages)

func (f *Function) Service() components.ServiceComponent {
	return f.srv
}

func (f *Function) Config() *structureSpec.Function {
	return &f.config
}

func (f *Function) Metrics() (common.Metrics, error) {
	m := f.metrics
	mem, err := usage.GetMemoryUsage()
	if err != nil {
		return common.Metrics{}, fmt.Errorf("getting memory stats failed with: %w", err)
	}

	maxMemory := f.config.Memory
	if f.provisioned {
		m.AvgRunTime = f.CallTime().Nanoseconds()
		m.ColdStart = f.ColdStart().Nanoseconds()
		maxMemory = f.MemoryMax()
	}

	// Memory == 0 no memory limit
	if maxMemory <= 0 {
		maxMemory = WasmMemorySizeLimit
	}

	m.Memory = float64(mem.Free) / float64(maxMemory)

	return m, nil
}

func (f *Function) Commit() string {
	return f.commit
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
