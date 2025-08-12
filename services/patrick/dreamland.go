package service

import (
	iface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/common"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	servicesCommon "github.com/taubyte/tau/services/common"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Patrick, createPatrickService, nil); err != nil {
		panic(err)
	}
}

func createPatrickService(u *dream.Universe, config *iface.ServiceConfig) (iface.Service, error) {
	// Used to test cancel job on go-patrick-http
	if result, ok := config.Others["delay"]; ok {
		if result == 1 {
			servicesCommon.DelayJob = true
		}
	}

	// Used to test retry job on go-patrick-http
	if result, ok := config.Others["retry"]; ok {
		if result == 1 {
			servicesCommon.RetryJob = true
		}
	}

	return New(u.Context(), common.NewDreamConfig(u, config))
}
