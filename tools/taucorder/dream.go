package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"golang.org/x/exp/maps"

	dreamApi "github.com/taubyte/tau/clients/http/dream"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/taubyte/tau/tools/taucorder/common"
	"github.com/taubyte/tau/tools/taucorder/helpers/p2p"
	"github.com/urfave/cli/v2"

	"github.com/taubyte/tau/p2p/peer"
)

func getDreamlandPeers(universe string) ([]peerCore.AddrInfo, []byte, error) {
	client, err := dreamApi.New(
		common.GlobalContext,
		dreamApi.Unsecure(),
		dreamApi.URL("http://127.0.0.1:1421"),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed creating dreamland http client with error: %v", err)
	}

	stats, err := client.Status()
	if err != nil {
		return nil, nil, fmt.Errorf("failed client status with error: %v", err)
	}

	info, err := client.Universes()
	if err != nil {
		return nil, nil, fmt.Errorf("failed client status with error: %v", err)
	}

	if _, ok := stats[universe]; !ok {
		return nil, nil, fmt.Errorf("universe %s does not exist", universe)
	}

	if _, ok := info[universe]; !ok {
		return nil, nil, fmt.Errorf("failed to fetch info for universe %s", universe)
	}

	// List for bootstrapping
	nodes := make([]peerCore.AddrInfo, 0, len(stats[universe].Nodes))

	for id, addr := range stats[universe].Nodes {
		node_addrs := make([]multiaddr.Multiaddr, 0)
		for _, _addr := range addr {
			node_addrs = append(node_addrs, multiaddr.StringCast(_addr))
		}
		_pid, err := peerCore.Decode(id)
		if err != nil {
			return nil, nil, fmt.Errorf("failed peer id decode with error: %v", err)
		}
		nodes = append(nodes, peerCore.AddrInfo{ID: _pid, Addrs: node_addrs})
	}

	return nodes, info[universe].SwarmKey, nil
}

var dreamCmd = &cli.Command{
	Name:    "dream",
	Aliases: []string{"local"},
	Usage:   "Run using local dreamland",
	Subcommands: []*cli.Command{
		{
			Name:    "with",
			Aliases: []string{"use"},
			Action: func(c *cli.Context) error {
				universe := c.Args().First()
				if universe == "" {
					return errors.New("provide the name of universe to connect to")
				}

				nodes, swarmKey, err := getDreamlandPeers(universe)
				if err != nil {
					return err
				}

				scanner = func(ctx context.Context, n peer.Node) error {
					nodes, _, err := getDreamlandPeers(universe)
					if err != nil {
						return err
					}

					var wg sync.WaitGroup
					var done atomic.Uint64

					for _, pinfo := range nodes {
						wg.Add(1)
						go func(pinfo peerCore.AddrInfo) {
							defer wg.Done()
							err := n.Peer().Connect(ctx, pinfo)
							if err != nil {
								fmt.Printf("Failed to connect to `%s` with %s\n", pinfo.String(), err.Error())
							}
							done.Add(1)
						}(pinfo)
					}

					wg.Wait()

					fmt.Printf("Found %d nodes. Connected to %d.\n", len(nodes), done.Load())

					return nil
				}

				node, err = p2p.New(common.GlobalContext, nodes, swarmKey)
				if err != nil {
					return fmt.Errorf("failed new with bootstrap list with error: %v", err)
				}

				return nil
			},
		},
		{
			Name:    "list",
			Aliases: []string{"l"},
			Action: func(c *cli.Context) error {
				client, err := dreamApi.New(
					common.GlobalContext,
					dreamApi.Unsecure(),
					dreamApi.URL("http://127.0.0.1:1421"),
				)
				if err != nil {
					return fmt.Errorf("failed creating dreamland http client with error: %v", err)
				}

				stats, err := client.Status()
				if err != nil {
					return fmt.Errorf("failed client status with error: %v", err)
				}

				for _, universe := range maps.Keys(stats) {
					fmt.Println(universe)
				}

				return nil
			},
		},
	},
}
