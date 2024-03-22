package service

import (
	iface "github.com/taubyte/go-interfaces/common"
	commonSpecs "github.com/taubyte/go-specs/common"
	"github.com/taubyte/tau/libdream"
	"github.com/taubyte/tau/libdream/common"
	protocolsCommon "github.com/taubyte/tau/protocols/common"
)

func init() {
	if err := libdream.Registry.Set(commonSpecs.Patrick, createPatrickService, nil); err != nil {
		panic(err)
	}
}

func createPatrickService(u *libdream.Universe, config *iface.ServiceConfig) (iface.Service, error) {
	// Used to test cancel job on go-patrick-http
	if result, ok := config.Others["delay"]; ok {
		if result == 1 {
			protocolsCommon.DelayJob = true
		}
	}

	// Used to test retry job on go-patrick-http
	if result, ok := config.Others["retry"]; ok {
		if result == 1 {
			protocolsCommon.RetryJob = true
		}
	}

	return New(u.Context(), common.NewDreamlandConfig(u, config))
}
