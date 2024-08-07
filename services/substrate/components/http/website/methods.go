package website

import (
	commonIface "github.com/taubyte/tau/core/services/substrate/components"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (w *Website) Service() commonIface.ServiceComponent {
	return w.srv
}

func (w *Website) Config() *structureSpec.Website {
	return &w.config
}

func (w *Website) Commit() string {
	return w.commit
}

func (w *Website) Branch() string {
	return w.branch
}

func (w *Website) Matcher() commonIface.MatchDefinition {
	return w.matcher
}

func (w *Website) AssetId() string {
	return w.assetId
}

func (w *Website) Id() string {
	return w.config.Id
}

func (w *Website) CachePrefix() string {
	return w.matcher.Host
}

func (w *Website) Ready() error {
	if !w.readyDone {
		<-w.readyCtx.Done()
	}

	return w.readyError
}

func (w *Website) Close() {
	w.instanceCtxC()
}

func (w *Website) Project() string {
	return w.project
}

// Fulfill Serviceable interface, used to ensure TVM.New() fails if using a website
func (w *Website) Structure() *structureSpec.Function {
	return nil
}

func (w *Website) IsProvisioned() bool {
	return w.provisioned
}
