package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	peer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/pnet"
	"github.com/taubyte/tau/p2p/keypair"
	p2p "github.com/taubyte/tau/p2p/peer"

	"connectrpc.com/connect"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"

	dream "github.com/taubyte/tau/clients/http/dream"
	dreamCore "github.com/taubyte/tau/dream"
)

func deleteEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}

	r := make([]string, 0, len(s))
	for _, str := range s {
		if str != "" {
			r = append(r, str)
		}
	}
	return r
}

var (
	expectedKeyLength = 6
)

func formatSwarmKey(key []byte) (pnet.PSK, error) {
	_key := strings.Split(strings.ReplaceAll(string(key), "\n", ""), "/")
	_key = deleteEmpty(_key)

	if len(_key) != expectedKeyLength {
		return nil, errors.New("swarm key is not correctly formatted")
	}

	format := fmt.Sprintf(`/%s/%s/%s/%s/
/%s/
%s`, _key[0], _key[1], _key[2], _key[3], _key[4], _key[5])

	return []byte(format), nil
}

func (ns *nodeService) New(ctx context.Context, req *connect.Request[pb.Config]) (*connect.Response[pb.Node], error) {
	ni := &instance{}
	ni.ctx, ni.ctxC = context.WithCancel(ns.ctx)

	var (
		swarmKey []byte
		err      error
		nodes    []peer.AddrInfo
	)

	if source := req.Msg.GetCloud(); source != nil {
		if source.GetConnect() == nil {
			cid := source.GetConfigId()
			if cid == "" {
				return nil, errors.New("config id can not be empty")
			}

			if ns.resolver == nil {
				return nil, errors.New("failed to lookup config: no resolver")
			}

			if ni.config, err = ns.resolver.Lookup(cid); err != nil {
				return nil, fmt.Errorf("look up configuration id `%s`: %w", cid, err)
			}

			fqdn := ni.config.Cloud().Domain().Root()

			skr, err := ni.config.Cloud().P2P().Swarm().Open()
			if err != nil {
				return nil, fmt.Errorf("opening swarm key of `%s`: %w", fqdn, err)
			}
			defer skr.Close()

			if swarmKey, err = io.ReadAll(skr); err != nil && err != io.EOF {
				return nil, fmt.Errorf("reading swarm key of `%s`: %w", fqdn, err)
			}

			for _, sh := range ni.config.Cloud().P2P().Bootstrap().List() {
				for _, ht := range ni.config.Cloud().P2P().Bootstrap().Shape(sh).List() {
					htc := ni.config.Hosts().Host(ht)
					bt := htc.Shapes().Instance(sh)
					shid := bt.Id()
					shMainPort := int(ni.config.Shapes().Shape(sh).Ports().Get("main"))
					for _, addr := range htc.Addresses().List() {
						ip, _, err := net.ParseCIDR(addr)
						if err != nil {
							ip = net.ParseIP(addr)
							if ip == nil {
								return nil, fmt.Errorf("parsing peer `%s`: `%s` is not valid IP or CIDR", ht, addr)
							}
						}

						addrinfo, err := peer.AddrInfoFromString(fmt.Sprintf("/ip4/%s/tcp/%d/p2p/%s", ip.String(), shMainPort, shid))
						if err != nil {
							return nil, fmt.Errorf("parsing peer address `%s`: %w", ht, err)
						}

						nodes = append(nodes, *addrinfo)
					}
				}
			}
		} else {
			return nil, errors.New("remote spore drive server not implemented")
		}
	} else if source := req.Msg.GetUniverse(); source != nil {
		if con := source.GetConnect(); con != nil && con.GetUrl() != "" {
			ni.dream, err = dream.New(ns.ctx, dream.URL(con.GetUrl()))
		} else {
			ni.dream, err = dream.New(ns.ctx, dream.URL("http://"+dreamCore.DreamlandApiListen))
		}
		if err != nil {
			return nil, fmt.Errorf("connecting to dream: %w", err)
		}

		ni.universe = source.GetUniverse()

		u := ni.dream.Universe(ni.universe)
		info, err := u.Status()
		if err != nil {
			return nil, fmt.Errorf("connecting to universe `%s`: %w", ni.universe, err)
		}

		if !source.GetDisable() {
			if bpeers := source.GetAddresses(); bpeers != nil && len(bpeers.GetMultiaddr()) > 0 {
				for _, addr := range bpeers.GetMultiaddr() {
					addrinfo, err := peer.AddrInfoFromString(addr)
					if err != nil {
						return nil, fmt.Errorf("adding bootstrap address `%s`: %w", addr, err)
					}
					nodes = append(nodes, *addrinfo)
				}
			} else {

				maxPeers := len(info.Nodes)
				if sp := source.GetSubsetPercentage(); sp != 0 {
					if sp < 0 || sp > 1 {
						return nil, errors.New("subset percentage out of range")
					}
					maxPeers = int(float32(len(info.Nodes)) * sp)
				} else if sc := source.GetSubsetCount(); sc != 0 {
					if sc < 0 || sc > int32(len(info.Nodes)) {
						return nil, errors.New("subset count out of range")
					}
					maxPeers = int(sc)
				}

				for pid, p := range info.Nodes {
					if len(nodes) >= maxPeers {
						break
					}
					addrinfo, err := peer.AddrInfoFromString(fmt.Sprintf("%s/p2p/%s", p[0], pid)) // we pick first addr
					if err != nil {
						return nil, fmt.Errorf("adding node `%s` from universe `%s`: %w", pid, ni.universe, err)
					}
					nodes = append(nodes, *addrinfo)
				}
			}
		}

		if source.GetSwarmKey() != nil {
			swarmKey = source.GetSwarmKey()
		} else {
			swarmKey = info.SwarmKey
		}
	} else if source := req.Msg.GetRaw(); source != nil {
		swarmKey = source.GetSwarmKey()
		for _, paddr := range source.Peers {
			addrinfo, err := peer.AddrInfoFromString(paddr)
			if err != nil {
				return nil, fmt.Errorf("adding `%s` as peer: %w", paddr, err)
			}

			nodes = append(nodes, *addrinfo)
		}
	} else {
		return nil, errors.New("unexpected source")
	}

	privKey := req.Msg.GetPrivateKey()
	if privKey == nil {
		privKey = keypair.NewRaw()
	}

	if swarmKey != nil {
		swarmKey, err = formatSwarmKey(swarmKey)
		if err != nil {
			return nil, fmt.Errorf("format srwarmkey: %w", err)
		}
	}

	ni.Node, err = p2p.NewClientNode(
		ni.ctx,
		nil,
		privKey,
		swarmKey,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		nodes,
	)
	if err != nil {
		return nil, fmt.Errorf("creating node: %w", err)
	}

	if err = ni.post(); err != nil {
		return nil, fmt.Errorf("node post failed: %w", err)
	}

	ns.lock.Lock()
	defer ns.lock.Unlock()

	nid := ni.ID().String()
	ns.nodes[nid] = ni

	return connect.NewResponse(&pb.Node{Id: nid}), nil
}

func (ns *nodeService) Free(ctx context.Context, req *connect.Request[pb.Node]) (*connect.Response[pb.Empty], error) {
	nid := req.Msg.GetId()
	if nid == "" {
		return nil, errors.New("empty node id")
	}

	ns.lock.Lock()
	defer ns.lock.Unlock()

	if ni, ok := ns.nodes[nid]; ok {
		ni.ctxC()
		delete(ns.nodes, nid)
		return nil, nil
	}

	return nil, fmt.Errorf("node `%s` not found", nid)
}
