package node

import (
	authService "github.com/taubyte/tau/protocols/auth"
	"github.com/taubyte/tau/protocols/gateway"
	hoarderService "github.com/taubyte/tau/protocols/hoarder"
	monkeyService "github.com/taubyte/tau/protocols/monkey"
	patrickService "github.com/taubyte/tau/protocols/patrick"
	seerService "github.com/taubyte/tau/protocols/seer"
	nodeService "github.com/taubyte/tau/protocols/substrate"
	tnsService "github.com/taubyte/tau/protocols/tns"

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
