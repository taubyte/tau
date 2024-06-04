package node

import (
	authService "github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/gateway"
	hoarderService "github.com/taubyte/tau/services/hoarder"
	monkeyService "github.com/taubyte/tau/services/monkey"
	patrickService "github.com/taubyte/tau/services/patrick"
	seerService "github.com/taubyte/tau/services/seer"
	nodeService "github.com/taubyte/tau/services/substrate"
	tnsService "github.com/taubyte/tau/services/tns"

	"github.com/taubyte/tau/config"
)

var available = map[string]config.ProtoCommandIface{
	"auth":      authService.Package(),
	"hoarder":   hoarderService.Package(),
	"monkey":    monkeyService.Package(),
	"substrate": nodeService.Package(),
	"patrick":   patrickService.Package(),
	"seer":      seerService.Package(),
	"tns":       tnsService.Package(),
	"gateway":   gateway.Package(),
}
