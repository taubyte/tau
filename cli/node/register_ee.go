//go:build ee

package node

import (
	eenode "github.com/taubyte/tau/ee/node"
	"github.com/taubyte/tau/pkg/specs/common"
)

// Registers ee services into the node registry in enterprise (-tags ee) builds.
// The service set + implementations live in the private ee/ submodule (ee/node);
// this seam only wires them into the shared registry. Structural only — no EE
// logic or layout here.
func init() {
	for _, r := range eenode.Services() {
		if err := Register(r.Name, r.Package); err != nil {
			panic(err)
		}
		common.RegisterService(r.Name, r.Caps)
	}
}
