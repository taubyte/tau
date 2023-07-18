package node

import (
	authService "github.com/taubyte/odo/protocols/auth/service"
	hoarderService "github.com/taubyte/odo/protocols/hoarder/service"
	monkeyService "github.com/taubyte/odo/protocols/monkey/service"
	nodeService "github.com/taubyte/odo/protocols/node/service"
	patrickService "github.com/taubyte/odo/protocols/patrick/service"
	seerService "github.com/taubyte/odo/protocols/seer/service"
	tnsService "github.com/taubyte/odo/protocols/tns/service"

	commonIface "github.com/taubyte/go-interfaces/services/common"
)

var available = map[string]commonIface.Package{
	"auth":    authService.Package(),
	"hoarder": hoarderService.Package(),
	"monkey":  monkeyService.Package(),
	"node":    nodeService.Package(),
	"patrick": patrickService.Package(),
	"seer":    seerService.Package(),
	"tns":     tnsService.Package(),
}
