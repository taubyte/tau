package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/exp/maps"

	dreamApi "github.com/taubyte/tau/clients/http/dream"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"

	"github.com/libp2p/go-libp2p/core/crypto"
	peer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/pnet"
	"github.com/multiformats/go-multiaddr"
	"github.com/taubyte/tau/tools/taucorder/common"
	"github.com/taubyte/tau/tools/taucorder/helpers/p2p"
	"github.com/urfave/cli/v2"

	"github.com/libp2p/go-libp2p/core/sec"
	"github.com/libp2p/go-libp2p/p2p/net/swarm"

	"github.com/miekg/dns"

	"github.com/taubyte/p2p/keypair"

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

	id, err := peer.IDFromPublicKey(key.GetPublic())
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

var (
	frames = []string{"∙∙∙", "●∙∙", "∙●∙", "∙∙●", "∙∙∙"}
)

func formatSwarmKey(key []byte) (pnet.PSK, error) {
	_key := strings.Split(string(key), "/")
	_key = deleteEmpty(_key)

	if len(_key) != expectedKeyLength {
		return nil, errors.New("swarm key is not correctly formatted")
	}

	format := fmt.Sprintf(`/%s/%s/%s/%s/
/%s/
%s`, _key[0], _key[1], _key[2], _key[3], _key[4], _key[5])

	return []byte(format), nil
}

func newCLI() *(cli.App) {
	app := &cli.App{
		UseShortOptionHandling: true,
		EnableBashCompletion:   true,
		Action:                 func(ctx *cli.Context) error { return nil },
	}
	defineCLI(app)
	return app
}

func ParseCommandLine() error {
	return newCLI().RunContext(common.GlobalContext, os.Args)
}

func defineCLI(app *cli.App) {
	app.Commands = []*cli.Command{
		{
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

						info, err := client.Universes()
						if err != nil {
							return fmt.Errorf("failed client status with error: %v", err)
						}

						if _, ok := stats[universe]; !ok {
							return fmt.Errorf("universe %s does not exist", universe)
						}

						if _, ok := info[universe]; !ok {
							return fmt.Errorf("failed to fetch info for universe %s", universe)
						}

						// List for bootstrapping
						_nodes := make([]peer.AddrInfo, 0, len(stats[universe].Nodes))

						for id, addr := range stats[universe].Nodes {
							node_addrs := make([]multiaddr.Multiaddr, 0)
							for _, _addr := range addr {
								node_addrs = append(node_addrs, multiaddr.StringCast(_addr))
							}
							_pid, err := peer.Decode(id)
							if err != nil {
								return fmt.Errorf("failed peer id decode with error: %v", err)
							}
							node := peer.AddrInfo{ID: _pid, Addrs: node_addrs}
							_nodes = append(_nodes, node)
						}

						node, err = p2p.New(common.GlobalContext, _nodes, info[universe].SwarmKey)
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
		},
		{
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

				// Progress bar setup
				progress := mpb.New(mpb.WithWidth(60), mpb.WithRefreshRate(300*time.Millisecond))
				name := "Fetching DNS records"
				dnsBar, _ := progress.Add(1,
					mpb.SpinnerStyle(frames...).Build(),
					mpb.BarRemoveOnComplete(),
					mpb.BarFillerTrim(),
					mpb.PrependDecorators(
						decor.Name(name),
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
					return errors.New("no peer were found")
				}

				tmpCtx, tmpCtxC := context.WithCancel(context.Background())
				node, err = p2p.New(tmpCtx, nil, swarmKey)
				if err != nil {
					tmpCtxC()
					return fmt.Errorf("creating temporary node failed with %w", err)
				}

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

				peers := make([]peer.AddrInfo, 0, len(ips))
				sem := make(chan struct{}, 4) // limit to 4 concurrent goroutines
				results := make(chan *peer.AddrInfo, len(ips))

				for _, ip := range ips {
					sem <- struct{}{}
					go func(ip net.IP) {
						defer func() { <-sem }()
						defer connectBar.Increment()

						pid, _, _ := generateNodeKeyAndID("")
						for _, port := range c.Uint64Slice("port") {
							peerAddrStr := fmt.Sprintf("/ip4/%s/tcp/%d/p2p/%s", ip.String(), port, pid)
							ma, err := multiaddr.NewMultiaddr(peerAddrStr)
							if err != nil {
								continue
							}

							addrInfo, err := peer.AddrInfoFromP2pAddr(ma)
							if err != nil {
								continue
							}

							err = node.Peer().Connect(context.Background(), *addrInfo)
							secerr := AsErrPeerIDMismatch(err)
							if secerr == nil {
								continue
							}

							addrInfo.ID = secerr.Actual

							//fmt.Printf("Found %s\n", ip.)
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

				node.Close()
				tmpCtxC()

				node, err = p2p.New(context.Background(), peers, swarmKey)
				if err != nil {
					return fmt.Errorf("failed new with bootstrap list with error: %v", err)
				}

				node.WaitForSwarm(10 * time.Second)

				return nil
			},
		},
	}
}
