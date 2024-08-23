package runtime

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"net"

	"github.com/tetratelabs/wazero/experimental/sock"

	gvntypes "github.com/containers/gvisor-tap-vsock/pkg/types"
	gvnvirtualnetwork "github.com/containers/gvisor-tap-vsock/pkg/virtualnetwork"
)

var (
	DefaultNetwork             *net.IPNet
	DefaultIPAddress           net.IP
	DefaultGatewayMacAddress   = "5a:94:ef:e4:0c:dd"
	DefaultContainerMacAddress net.HardwareAddr
	NetworkMTU                 = 1500
	NetworkDebug               = false
	LowPort                    = 10010
	HighPort                   = 64064
)

func init() {
	_, DefaultNetwork, _ = net.ParseCIDR("192.168.127.0/24")
	DefaultIPAddress = net.ParseIP("192.168.127.3")
	DefaultContainerMacAddress, _ = net.ParseMAC("02:00:00:00:00:01")
}

type NetworkConfig struct {
	network  *net.IPNet
	ip       net.IP
	mac      net.HardwareAddr
	forwards map[string]string
}

func firstLastIP(ipNet *net.IPNet) (net.IP, net.IP) {
	ip := ipNet.IP.Mask(ipNet.Mask) // First IP is the network address
	lastIP := make(net.IP, len(ip))

	// Copy the network address to lastIP and set host bits to 1
	for i := range ip {
		lastIP[i] = ip[i] | ^ipNet.Mask[i]
	}

	return ip, lastIP
}

func getRandomPort() int {
	portRange := HighPort - LowPort + 1
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(portRange)))
	return int(n.Int64()) + LowPort
}

func findFirstAvailablePort(ctx context.Context) (int, error) {
	for {
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("context cancelled: no available ports found")
		default:
			port := getRandomPort()
			ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
			if err == nil {
				ln.Close()
				return port, nil
			}
		}
	}
}

func (c *container) initNetwork(ctx context.Context) (err error) {
	gwIP, gwVirtIP := firstLastIP(c.networking.network)
	config := &gvntypes.Configuration{
		Debug:             NetworkDebug,
		MTU:               NetworkMTU,
		Subnet:            c.networking.network.String(),
		GatewayIP:         gwIP.String(),
		GatewayMacAddress: DefaultGatewayMacAddress,
		DHCPStaticLeases: map[string]string{
			c.networking.ip.String(): c.networking.mac.String(),
		},
		Forwards: c.networking.forwards,
		NAT: map[string]string{
			gwVirtIP.String(): "127.0.0.1",
		},
		GatewayVirtualIPs: []string{gwVirtIP.String()},
		Protocol:          gvntypes.QemuProtocol,
	}

	if c.vn, err = gvnvirtualnetwork.New(config); err != nil {
		return
	}

	c.port, err = findFirstAvailablePort(ctx)
	if err != nil {
		return
	}

	c.sockCfg = sock.NewConfig().WithTCPListener("127.0.0.1", c.port)

	return
}
