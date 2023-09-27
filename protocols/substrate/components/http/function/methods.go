package function

import (
	"github.com/taubyte/go-interfaces/services/substrate/components"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/tau/clients/p2p/seer/usage"
)

func (f *Function) Service() components.ServiceComponent {
	return f.srv
}

func (f *Function) Config() *structureSpec.Function {
	return &f.config
}

// TODO: move to file
type Metrics struct {
	Cached     float32 `cbor:"0,keyasint"`
	ClostStart int64   `cbor:"1,keyasint"`
	Memory     float64 `cbor:"2,keyasint"`
	AvgRunTime int64   `cbor:"3,keyasint"`
}

func (f *Function) Metrics() Metrics {
	m := f.metrics
	if mem, err := usage.GetMemoryUsage(); err == nil {
		m.Memory = float64(mem.Free) / float64(f.config.Memory)
	}
	return m
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
