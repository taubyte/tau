package domains

import (
	"strings"

	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

// Custom accessors with value transforms the generator can't derive.
// tcc-gen deliberately skips these fields (skipBoth in tools/tcc-gen); do not
// move them back into the generated getter.go/set.go.

func (g getter) FQDN() string {
	return strings.ToLower(basic.Get[string](g, "fqdn"))
}

func (g getter) UseCertificate() bool {
	var val struct{}
	return g.Config().Get("certificate").Value(&val) == nil
}

func UseCertificate(value bool) basic.Op {
	return func(ci basic.ConfigIface) []*seer.Query {
		var val struct{}
		if value && ci.Config().Get("certificate").Value(val) != nil {
			return basic.Set("certificate", nil)(ci)
		}

		return []*seer.Query{ci.Config().Get("certificate").Delete()}
	}
}
