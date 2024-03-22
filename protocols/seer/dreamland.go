package seer

import (
	"fmt"

	"github.com/foxcpp/go-mockdns"
	iface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/go-interfaces/services/seer"
	commonSpecs "github.com/taubyte/go-specs/common"
	"github.com/taubyte/tau/libdream"
	"github.com/taubyte/tau/libdream/common"
)

func init() {
	if err := libdream.Registry.Set(commonSpecs.Seer, createService, nil); err != nil {
		panic(err)
	}
}

func createService(u *libdream.Universe, config *iface.ServiceConfig) (iface.Service, error) {

	var mockResolver seer.Resolver
	if config.Others["mock"] == 1 {
		// NOTE: have to keep entry lowercase since package searches through lowercase
		mockServer, err := mockdns.NewServer(map[string]mockdns.Zone{
			"testing_website_builder.com.": {
				CNAME: "substrate.tau.cloud.",
				A:     []string{"192.168.0.1", "10.0.0.1"},
				TXT:   []string{"eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCJ9.eyJhZGRyZXNzIjoiNWRydTFFR1Iza0hyWHJzTWI3TDNpTEpTQm51c01KIn0.jUcMqKyHb_IBvdjObb_sggv9mfrix18FJyZpAxWdkVIoqO9kEAcpQzU675jm7n5qZDbzfzS-dmmHsUOuA54OJQ"},
			},
		}, false)
		if err != nil {
			return nil, fmt.Errorf("starting mock dns failed with: %w", err)
		}
		mockResolver = mockServer.Resolver()
	}

	return New(u.Context(), common.NewDreamlandConfig(u, config), Resolver(mockResolver))
}
