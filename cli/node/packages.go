package node

import (
	authService "github.com/taubyte/odo/protocols/auth"
	hoarderService "github.com/taubyte/odo/protocols/hoarder"
	monkeyService "github.com/taubyte/odo/protocols/monkey"
	patrickService "github.com/taubyte/odo/protocols/patrick"
	seerService "github.com/taubyte/odo/protocols/seer"
	nodeService "github.com/taubyte/odo/protocols/substrate"
	tnsService "github.com/taubyte/odo/protocols/tns"

	"github.com/taubyte/odo/config"
)

var available = map[string]config.Package{
	"auth":    authService.Package(),
	"hoarder": hoarderService.Package(),
	"monkey":  monkeyService.Package(),
	"node":    nodeService.Package(),
	"patrick": patrickService.Package(),
	"seer":    seerService.Package(),
	"tns":     tnsService.Package(),
}
