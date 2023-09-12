package gateway

// import (
// 	"context"
// 	"fmt"

// 	iface "github.com/taubyte/go-interfaces/common"
// 	tauConfig "github.com/taubyte/tau/config"
// 	dreamlandCommon "github.com/taubyte/tau/libdream/common"
// 	dreamlandRegistry "github.com/taubyte/tau/libdream/registry"
// )

// func init() {
// 	dreamlandRegistry.Registry.Gateway.Service = createGateway
// }

// func createGateway(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
// 	serviceConfig := &tauConfig.Node{
// 		Ports:       make(map[string]int),
// 		Root:        config.Root,
// 		P2PListen:   []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)},
// 		P2PAnnounce: []string{fmt.Sprintf(dreamlandCommon.DefaultP2PListenFormat, config.Port)},
// 		DevMode:     true,
// 		SwarmKey:    config.SwarmKey,
// 		HttpListen:  fmt.Sprintf("%s:%d", dreamlandCommon.DefaultHost, config.Others["http"]),
// 	}

// 	if result, ok := config.Others["secure"]; ok {
// 		serviceConfig.EnableHTTPS = result != 0
// 	}

// 	return New(ctx, serviceConfig)
// }
