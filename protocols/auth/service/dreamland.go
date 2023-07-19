package service

// import (
// 	"context"
// 	"fmt"

// 	"github.com/taubyte/dreamland/core/common"
// 	dreamlandCommon "github.com/taubyte/dreamland/core/common"
// 	dreamlandRegistry "github.com/taubyte/dreamland/core/registry"
// 	iface "github.com/taubyte/go-interfaces/common"
// 	commonIface "github.com/taubyte/go-interfaces/services/common"
// )

// func init() {
// 	dreamlandRegistry.Registry.Auth.Service = createAuthService
// }

// func createAuthService(ctx context.Context, config *iface.ServiceConfig) (iface.Service, error) {
// 	serviceConfig := &commonIface.GenericConfig{}
// 	serviceConfig.Root = config.Root
// 	serviceConfig.P2PListen = []string{fmt.Sprintf(common.DefaultP2PListenFormat, config.Port)}
// 	serviceConfig.P2PAnnounce = []string{fmt.Sprintf(common.DefaultP2PListenFormat, config.Port)}
// 	serviceConfig.Bootstrap = false
// 	serviceConfig.DevMode = true
// 	serviceConfig.SwarmKey = config.SwarmKey

// 	if config.Others["http"] != 443 {
// 		serviceConfig.HttpListen = fmt.Sprintf("%s:%d", dreamlandCommon.DefaultURL, config.Others["http"])
// 	}

// 	if result, ok := config.Others["secure"]; ok {
// 		serviceConfig.HttpSecure = (result != 0)
// 	}

// 	serviceConfig.DVPrivateKey = config.PrivateKey
// 	serviceConfig.DVPublicKey = config.PublicKey

// 	return New(ctx, serviceConfig)
// }
