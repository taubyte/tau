package service

// import (
// 	"context"
// 	"fmt"

// 	"github.com/foxcpp/go-mockdns"
// 	dreamlandCommon "github.com/taubyte/dreamland/core/common"
// 	dreamlandRegistry "github.com/taubyte/dreamland/core/registry"
// 	iface "github.com/taubyte/go-interfaces/common"
// 	commonIface "github.com/taubyte/go-interfaces/services/common"
// 	"github.com/taubyte/go-interfaces/services/seer"
// )

// func init() {
// 	dreamlandRegistry.Registry.Seer.Service = createService
// }

// func createService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
// 	serviceConfig := &commonIface.GenericConfig{}
// 	serviceConfig.Root = config.Root
// 	serviceConfig.P2PListen = []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)}
// 	serviceConfig.P2PAnnounce = []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)}
// 	serviceConfig.Bootstrap = false
// 	serviceConfig.DnsPort = config.Others["dns"]
// 	serviceConfig.DevMode = true

// 	serviceConfig.SwarmKey = config.SwarmKey

// 	if config.Others["http"] != 443 {
// 		serviceConfig.HttpListen = fmt.Sprintf("%s:%d", dreamlandCommon.DefaultURL, config.Others["http"])
// 	}

// 	if result, ok := config.Others["secure"]; ok {
// 		serviceConfig.HttpSecure = (result != 0)
// 	}

// 	var mockResolver seer.Resolver
// 	if config.Others["mock"] == 1 {
// 		// NOTE: have to keep entry lowercase since package searches through lowercase
// 		mockServer, _ := mockdns.NewServer(map[string]mockdns.Zone{
// 			"testing_website_builder.com.": {
// 				CNAME: "nodes.taubyte.com.",
// 				A:     []string{"192.168.0.1", "10.0.0.1"},
// 				TXT:   []string{"eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCJ9.eyJhZGRyZXNzIjoiNWRydTFFR1Iza0hyWHJzTWI3TDNpTEpTQm51c01KIn0.jUcMqKyHb_IBvdjObb_sggv9mfrix18FJyZpAxWdkVIoqO9kEAcpQzU675jm7n5qZDbzfzS-dmmHsUOuA54OJQ"},
// 			},
// 		}, false)
// 		mockResolver = mockServer.Resolver()
// 	}

// 	return New(ctx, serviceConfig, Resolver(mockResolver))
// }
