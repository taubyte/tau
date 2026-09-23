package common

import (
	"fmt"
	"regexp"
	"strings"

	iface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/p2p/keypair"
	"github.com/taubyte/tau/dream"
	tauConfig "github.com/taubyte/tau/pkg/config"
)

func NewConfig(u *dream.Universe, config *iface.ServiceConfig) (tauConfig.Config, error) {
	privKey, _, err := keypair.GenerateDeterministicKey(config.Root)
	if err != nil {
		return nil, err
	}

	// PORT 0, ALWAYS: THE BIND IS THE ALLOCATION. dream picks no port numbers —
	// it asks the kernel for one by listening on 0, and reads back what it got
	// once the host is up (dream.createService). Predicting a number first and
	// binding it later is a window another process can step into, and four
	// dreaming packages in parallel step into it.
	//
	// config.Port is not consulted on the way in: a universe assigns its own
	// p2p ports, and the field carries the real one back out after the bind.
	//
	// The announce is the listen address for the same reason it always was —
	// dream is one process on loopback — and a zero port in an announce reads
	// as "the port I bound" (peer.node.SimpleAddrsFactory).
	p2pListen := []string{fmt.Sprintf(dream.DefaultP2PListenFormat, 0)}
	ports := make(map[string]int)
	for _, k := range []string{"http", "p2p", "dns", "ipfs"} {
		ports[k] = config.Others[k]
	}

	upeers := u.Peers()
	bpeers := make([]string, 0, len(upeers))
	for _, n := range upeers {
		bpeers = append(bpeers, n.Peer().Addrs()[0].String()+"/p2p/"+n.ID().String())
	}

	cluster := config.Cluster
	if cluster == "" {
		cluster = "main"
	}

	serviceConfig, err := tauConfig.New(
		tauConfig.WithRoot(config.Root),
		tauConfig.WithP2PListen(p2pListen),
		tauConfig.WithP2PAnnounce(p2pListen),
		tauConfig.WithSwarmKey(config.SwarmKey),
		tauConfig.WithPrivateKey(privKey),
		tauConfig.WithPorts(ports),
		// Unset is 0, which is the kernel picking; a universe started with an
		// explicit `--bind <service>@<port>/http` pins it instead, because that
		// is a port somebody asked for rather than one dream guessed.
		tauConfig.WithHttpListen(fmt.Sprintf("%s:%d", dream.DefaultHost, config.Others["http"])),
		tauConfig.WithVerbose(config.Others["verbose"] != 0),
		tauConfig.WithEnableHTTPS(config.Others["secure"] != 0),
		tauConfig.WithDomainValidation(tauConfig.DomainValidation{PrivateKey: config.PrivateKey, PublicKey: config.PublicKey}),
		tauConfig.WithNetworkFqdn(strings.ToLower(u.Name())+".localtau"),
		tauConfig.WithGeneratedDomainRegExp(regexp.MustCompile(`^[^.]+\.g\.`+strings.ToLower(u.Name())+`.localtau$`)),
		tauConfig.WithServicesDomainRegExp(regexp.MustCompile(`^([^.]+\.)?tau\.`+strings.ToLower(u.Name())+`.localtau$`)),
		tauConfig.WithAliasDomainsRegExp(make([]*regexp.Regexp, 0)),
		tauConfig.WithHosts(config.Hosts),
		tauConfig.WithCluster(cluster),
		tauConfig.WithPeers(bpeers),
		tauConfig.WithLocation(&config.Location),
	)
	if err != nil {
		return nil, err
	}

	serviceConfig.SetDatabases(config.Databases)
	return serviceConfig, nil
}
