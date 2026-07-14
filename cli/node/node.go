package node

import (
	"errors"
	"fmt"

	accountsService "github.com/taubyte/tau/services/accounts"
	authService "github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/gateway"
	hoarderService "github.com/taubyte/tau/services/hoarder"
	monkeyService "github.com/taubyte/tau/services/monkey"
	patrickService "github.com/taubyte/tau/services/patrick"
	seerService "github.com/taubyte/tau/services/seer"
	nodeService "github.com/taubyte/tau/services/substrate"
	tnsService "github.com/taubyte/tau/services/tns"

	"github.com/taubyte/tau/pkg/config"
)

var available = map[string]config.ProtoCommandIface{
	"auth":      authService.Package(),
	"accounts":  accountsService.Package(),
	"hoarder":   hoarderService.Package(),
	"monkey":    monkeyService.Package(),
	"substrate": nodeService.Package(),
	"patrick":   patrickService.Package(),
	"seer":      seerService.Package(),
	"tns":       tnsService.Package(),
	"gateway":   gateway.Package(),
}

// Register adds a service package to the node registry so it can run in a shape.
// It is the extension point for build-tag-gated services (e.g. ee) that register
// themselves from an init(). A name already present is refused rather than
// overwritten, so a registration cannot silently replace a built-in service.
func Register(name string, pkg config.ProtoCommandIface) error {
	if name == "" {
		return errors.New("service name required")
	}
	if _, exists := available[name]; exists {
		return fmt.Errorf("service %q already registered", name)
	}
	available[name] = pkg
	return nil
}
