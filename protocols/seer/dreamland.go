package seer

import (
	"context"
	"fmt"

	"github.com/foxcpp/go-mockdns"
	iface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/go-interfaces/services/seer"
	tauConfig "github.com/taubyte/tau/config"
	"github.com/taubyte/tau/libdream"
	dreamlandCommon "github.com/taubyte/tau/libdream/common"
)

func init() {
	libdream.Registry.Seer.Service = createService
}

func createService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
	serviceConfig := &tauConfig.Node{}
	serviceConfig.Root = config.Root
	serviceConfig.P2PListen = []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)}
	serviceConfig.P2PAnnounce = []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)}
	serviceConfig.DevMode = true
	serviceConfig.Ports = make(map[string]int)
	serviceConfig.Ports["dns"] = config.Others["dns"]
	serviceConfig.Databases = config.Databases

	serviceConfig.SwarmKey = config.SwarmKey

	serviceConfig.HttpListen = fmt.Sprintf("%s:%d", dreamlandCommon.DefaultHost, config.Others["http"])

	if result, ok := config.Others["secure"]; ok {
		serviceConfig.EnableHTTPS = (result != 0)
	}

	var mockResolver seer.Resolver
	if config.Others["mock"] == 1 {
		// NOTE: have to keep entry lowercase since package searches through lowercase
		mockServer, err := mockdns.NewServer(map[string]mockdns.Zone{
			"testing_website_builder.com.": {
				CNAME: "nodes.taubyte.com.",
				A:     []string{"192.168.0.1", "10.0.0.1"},
				TXT:   []string{"eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCJ9.eyJhZGRyZXNzIjoiNWRydTFFR1Iza0hyWHJzTWI3TDNpTEpTQm51c01KIn0.jUcMqKyHb_IBvdjObb_sggv9mfrix18FJyZpAxWdkVIoqO9kEAcpQzU675jm7n5qZDbzfzS-dmmHsUOuA54OJQ"},
			},
		}, false)
		if err != nil {
			return nil, fmt.Errorf("starting mock dns failed with: %w", err)
		}
		mockResolver = mockServer.Resolver()
	}

	return New(ctx, serviceConfig, Resolver(mockResolver))
}
