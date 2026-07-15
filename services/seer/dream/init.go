package dream

import (
	"fmt"
	"strings"

	"github.com/foxcpp/go-mockdns"
	iface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/common"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	seerSvr "github.com/taubyte/tau/services/seer"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Seer, createService, nil); err != nil {
		panic(err)
	}
}

func createService(u *dream.Universe, config *iface.ServiceConfig) (iface.Service, error) {
	cfg, err := common.NewConfig(u, config)
	if err != nil {
		return nil, err
	}
	var srv iface.Service
	if config.Others["mock"] == 1 {
		// NOTE: have to keep entry lowercase since package searches through lowercase
		mockServer, mockErr := mockdns.NewServer(map[string]mockdns.Zone{
			"testing_website_builder.com.": {
				CNAME: "substrate.tau." + strings.ToLower(u.Name()) + ".localtau.",
				A:     []string{"192.168.0.1", "10.0.0.1"},
				TXT:   []string{"eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCJ9.eyJhZGRyZXNzIjoiNWRydTFFR1Iza0hyWHJzTWI3TDNpTEpTQm51c01KIn0.jUcMqKyHb_IBvdjObb_sggv9mfrix18FJyZpAxWdkVIoqO9kEAcpQzU675jm7n5qZDbzfzS-dmmHsUOuA54OJQ"},
			},
		}, false)
		if mockErr != nil {
			return nil, fmt.Errorf("starting mock dns failed with: %w", mockErr)
		}

		srv, err = seerSvr.New(u.Context(), cfg, seerSvr.Resolver(mockServer.Resolver()))
	} else {
		srv, err = seerSvr.New(u.Context(), cfg)
	}
	if err != nil {
		return nil, err
	}

	if err := common.StartBeacon(u.Context(), cfg, srv.Node(), commonSpecs.Seer); err != nil {
		return nil, err
	}
	return srv, nil
}
