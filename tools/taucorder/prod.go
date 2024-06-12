package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"

	"github.com/libp2p/go-libp2p/core/crypto"
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/taubyte/tau/tools/taucorder/helpers/p2p"
	"github.com/urfave/cli/v2"

	"github.com/libp2p/go-libp2p/core/sec"
	"github.com/libp2p/go-libp2p/p2p/net/swarm"

	"github.com/miekg/dns"

	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"

	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
)

func getDNSRecords(domain string, dnsServer string) ([]net.IP, error) {
	var ips []net.IP

	c := dns.Client{}
	m := dns.Msg{}
	m.SetQuestion(dns.Fqdn(domain), dns.TypeA)
	r, _, err := c.Exchange(&m, dnsServer+":53")
	if err != nil {
		return nil, err
	}

	for _, ans := range r.Answer {
		if aRecord, ok := ans.(*dns.A); ok {
			ips = append(ips, aRecord.A)
		}
	}

	return ips, nil
}

func generateNodeKeyAndID(pkey string) (string, string, error) {
	var (
		key     crypto.PrivKey
		keyData []byte
		err     error
	)
	if pkey == "" {
		key = keypair.New()
		keyData, err = crypto.MarshalPrivateKey(key)
		if err != nil {
			return "", "", fmt.Errorf("marshal private key failed with %w", err)
		}
	} else {
		keyData, err = base64.StdEncoding.DecodeString(pkey)
		if err != nil {
			return "", "", fmt.Errorf("decode private key failed with %w", err)
		}

		key, err = crypto.UnmarshalPrivateKey(keyData)
		if err != nil {
			return "", "", fmt.Errorf("read private key failed with %w", err)
		}
	}

	id, err := peerCore.IDFromPublicKey(key.GetPublic())
	if err != nil {
		return "", "", fmt.Errorf("id from private key failed with %w", err)
	}

	return id.String(), base64.StdEncoding.EncodeToString(keyData), nil
}

func AsErrPeerIDMismatch(err error) *sec.ErrPeerIDMismatch {
	var dialerr *swarm.DialError
	if !errors.As(err, &dialerr) {
		return nil
	}

	var mis sec.ErrPeerIDMismatch
	for _, te := range dialerr.DialErrors {
		if errors.As(te.Cause, &mis) {
			return &mis
		}
	}

	return nil
}

func getPeers(fqdn string, ports []uint64, swarmKey []byte) ([]peerCore.AddrInfo, error) {
	// Progress bar setup
	progress := mpb.New(mpb.WithWidth(60), mpb.WithRefreshRate(300*time.Millisecond))
	dnsBar, _ := progress.Add(1,
		mpb.SpinnerStyle(frames...).Build(),
		mpb.BarRemoveOnComplete(),
		mpb.BarFillerTrim(),
		mpb.PrependDecorators(
			decor.Name("Fetching DNS records"),
		),
	)

	ips := make(map[string]net.IP)
	for _, pr := range commonSpecs.Services {
		_ips, _ := getDNSRecords(pr+".tau."+fqdn, "8.8.8.8")
		for _, ip := range _ips {
			ips[ip.String()] = ip
		}
	}

	dnsBar.Increment()
	dnsBar.Wait()

	time.Sleep(time.Second)

	if len(ips) == 0 {
		return nil, errors.New("no peer were found")
	}

	tmpCtx, tmpCtxC := context.WithCancel(context.Background())
	defer tmpCtxC()

	node, err := p2p.New(tmpCtx, nil, swarmKey)
	if err != nil {
		return nil, fmt.Errorf("creating temporary node failed with %w", err)
	}

	defer node.Close()

	// Progress bar setup for connecting to peers
	total := len(ips)
	connectBar := progress.AddBar(int64(total),
		mpb.PrependDecorators(
			decor.Name("Discovering peers: "),
			decor.CountersNoUnit("%d / %d"),
		),
		mpb.AppendDecorators(decor.Percentage()),
		mpb.BarRemoveOnComplete(),
	)

	peers := make([]peerCore.AddrInfo, 0, len(ips))
	sem := make(chan struct{}, 4) // limit to 4 concurrent goroutines
	results := make(chan *peerCore.AddrInfo, len(ips))

	for _, ip := range ips {
		sem <- struct{}{}
		go func(ip net.IP) {
			defer func() { <-sem }()
			defer connectBar.Increment()

			pid, _, _ := generateNodeKeyAndID("")
			for _, port := range ports {
				peerAddrStr := fmt.Sprintf("/ip4/%s/tcp/%d/p2p/%s", ip.String(), port, pid)
				ma, err := multiaddr.NewMultiaddr(peerAddrStr)
				if err != nil {
					continue
				}

				addrInfo, err := peerCore.AddrInfoFromP2pAddr(ma)
				if err != nil {
					continue
				}

				err = node.Peer().Connect(context.Background(), *addrInfo)
				secerr := AsErrPeerIDMismatch(err)
				if secerr == nil {
					continue
				}

				addrInfo.ID = secerr.Actual

				results <- addrInfo
			}
		}(ip)
	}

	// Wait for all goroutines to finish
	go func() {
		for i := 0; i < total; i++ {
			result := <-results
			if result != nil {
				peers = append(peers, *result)
			}
		}
		close(results)
	}()

	progress.Wait()

	time.Sleep(time.Second)

	return peers, nil
}

var prodCmd = &cli.Command{
	Name:    "use",
	Aliases: []string{"u"},
	Usage:   "use a remote cloud",
	Flags: []cli.Flag{
		&cli.Uint64SliceFlag{
			Name: "port",
		},
		&cli.StringFlag{
			Name:    "swarm-key",
			Aliases: []string{"swarm", "key"},
		},
	},
	Action: func(c *cli.Context) error {
		fqdn := c.Args().First()
		if fqdn == "" {
			return errors.New("provide the fqdn of cloud to connect to")
		}

		swarmKey, err := os.ReadFile(c.String("swarm-key"))
		if err != nil {
			return fmt.Errorf("failed to open swarm file `%s` with %w", c.String("swarm-key"), err)
		}

		swarmKey, err = formatSwarmKey(swarmKey)
		if err != nil {
			return fmt.Errorf("failed to format swarm key with %w", err)
		}

		peers, err := getPeers(fqdn, c.Uint64Slice("port"), swarmKey)
		if err != nil {
			return err
		}

		scanner = func(ctx context.Context, n peer.Node) error {
			nodes, err := getPeers(fqdn, c.Uint64Slice("port"), swarmKey)
			if err != nil {
				return err
			}

			var wg sync.WaitGroup
			for _, pinfo := range nodes {
				wg.Add(1)
				go func(pinfo peerCore.AddrInfo) {
					defer wg.Done()
					err := n.Peer().Connect(ctx, pinfo)
					if err != nil {
						fmt.Printf("Failed to connect to `%s` with %s\n", pinfo.String(), err.Error())
					}
				}(pinfo)
			}

			wg.Wait()
			return nil
		}

		node, err = p2p.New(context.Background(), peers, swarmKey)
		if err != nil {
			return fmt.Errorf("failed new with bootstrap list with error: %v", err)
		}

		node.WaitForSwarm(10 * time.Second)

		return nil
	},
}
