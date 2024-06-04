package domains

import (
	"strings"

	"github.com/taubyte/tau/pkg/schema/basic"
)

type getter struct {
	*domain
}

func (d *domain) Get() Getter {
	return getter{d}
}

func (g getter) Name() string {
	return g.domain.name
}

func (g getter) Application() string {
	return g.domain.application
}

func (g getter) Id() string {
	return basic.Get[string](g, "id")
}

func (g getter) Description() string {
	return basic.Get[string](g, "description")
}

func (g getter) Tags() []string {
	return basic.Get[[]string](g, "tags")
}

func (g getter) FQDN() string {
	return strings.ToLower(basic.Get[string](g, "fqdn"))
}

func (g getter) UseCertificate() bool {
	var val struct{}
	return g.Config().Get("certificate").Value(&val) == nil
}

func (g getter) Cert() string {
	return basic.Get[string](g, "certificate", "cert")
}

func (g getter) Key() string {
	return basic.Get[string](g, "certificate", "key")
}

func (g getter) Type() string {
	return basic.Get[string](g, "certificate", "type")
}

func (g getter) SmartOps() []string {
	return basic.Get[[]string](g, "smartops")
}
