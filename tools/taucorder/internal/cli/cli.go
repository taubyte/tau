// Package cli is taucorder's command line: urfave/cli, as cmd/tau uses it.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/pkg/stats"
	"github.com/taubyte/tau/services/discovery"
	"github.com/taubyte/tau/tools/taucorder/internal/bridge"
	"github.com/taubyte/tau/tools/taucorder/internal/policy"
	"github.com/taubyte/tau/tools/taucorder/internal/query"
	"github.com/taubyte/tau/tools/taucorder/internal/route"
	"github.com/taubyte/tau/tools/taucorder/internal/server"
	"github.com/taubyte/tau/tools/taucorder/internal/tui"
	"github.com/taubyte/tau/utils"
)

// Seat is a joined bridge, as the commands use it.
type Seat interface {
	route.Picker
	Host() host.Host
	Nodes() []stats.Node
	Close()
}

// Env is what the commands are wired to. Tests replace its parts.
type Env struct {
	Out io.Writer
	// Join joins a cloud.
	Join func(ctx context.Context, cfg bridge.Config) (Seat, error)
	// RunUI runs the TUI program.
	RunUI func(m tea.Model) error
	// Listening is told the local listener's address once it accepts.
	Listening func(addr string)
}

// Default is the real wiring.
func Default() Env {
	return Env{
		Out: os.Stdout,
		Join: func(ctx context.Context, cfg bridge.Config) (Seat, error) {
			return bridge.Join(ctx, cfg)
		},
		RunUI: func(m tea.Model) error {
			_, err := tea.NewProgram(m, tui.Options()...).Run()
			return err
		},
	}
}

// DefaultListen is v1 taucorder's port, on loopback.
const DefaultListen = "127.0.0.1:4201"

// Profile is a cloud as a file:
//
//	cloud: example
//	swarm_key: /etc/tau/swarm.key
//	bootstrap: [/ip4/203.0.113.7/tcp/4242/p2p/12D3Koo...]
//	listen: 127.0.0.1:4201
//	origins: [http://localhost:5173]
type Profile struct {
	Cloud     string   `yaml:"cloud"`
	SwarmKey  string   `yaml:"swarm_key"`
	Bootstrap []string `yaml:"bootstrap"`
	Listen    string   `yaml:"listen"`
	Origins   []string `yaml:"origins"`
}

func loadProfile(path string) (Profile, error) {
	var p Profile
	if path == "" {
		return p, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("profile %s: %w", path, err)
	}
	return p, nil
}

func pick(flag string, profile string) string {
	if flag != "" {
		return flag
	}
	return profile
}

func pickList(flag []string, profile []string) []string {
	if len(flag) > 0 {
		return flag
	}
	return profile
}

// config is the bridge.Config the flags and profile describe.
func config(c *cli.Context) (bridge.Config, Profile, error) {
	p, err := loadProfile(c.String("profile"))
	if err != nil {
		return bridge.Config{}, p, err
	}
	keyPath := pick(c.String("swarm-key"), p.SwarmKey)
	if keyPath == "" {
		return bridge.Config{}, p, errors.New("a swarm key is required (--swarm-key or the profile's swarm_key)")
	}
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return bridge.Config{}, p, err
	}
	// A swarm.key file is three lines; FormatSwarmKey wants slash-separated
	// segments, so line breaks become separators.
	key, err := utils.FormatSwarmKey(strings.Join(strings.Fields(string(raw)), "/"))
	if err != nil {
		return bridge.Config{}, p, fmt.Errorf("swarm key %s: %w", keyPath, err)
	}
	var boot []peer.AddrInfo
	for _, s := range pickList(c.StringSlice("bootstrap"), p.Bootstrap) {
		a, err := ma.NewMultiaddr(s)
		if err != nil {
			return bridge.Config{}, p, fmt.Errorf("bootstrap %q: %w", s, err)
		}
		info, err := peer.AddrInfoFromP2pAddr(a)
		if err != nil {
			return bridge.Config{}, p, fmt.Errorf("bootstrap %q: %w", s, err)
		}
		boot = append(boot, *info)
	}
	return bridge.Config{
		SwarmKey:  key,
		Bootstrap: boot,
		Cloud:     pick(c.String("cloud"), p.Cloud),
		Listen:    c.StringSlice("p2p-listen"),
	}, p, nil
}

// App is the command line.
func App(env Env) *cli.App {
	output := &cli.StringFlag{Name: "output", Aliases: []string{"o"}, Value: "table", Usage: "table or json"}
	wait := &cli.DurationFlag{Name: "wait", Value: discovery.DefaultInterval + time.Second, Usage: "how long to listen to the fleet's gossip before answering"}
	return &cli.App{
		Name:  "taucorder",
		Usage: "bridge a tau cloud's v2 services to local clients, and watch the fleet",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "profile", Aliases: []string{"p"}, EnvVars: []string{"TAUCORDER_PROFILE"}, Usage: "profile file (yaml)"},
			&cli.StringFlag{Name: "swarm-key", Usage: "the cloud's swarm.key file"},
			&cli.StringSliceFlag{Name: "bootstrap", Aliases: []string{"b"}, Usage: "bootstrap peer multiaddr (repeatable)"},
			&cli.StringFlag{Name: "cloud", Usage: "the cloud's name, which scopes its discovery gossip"},
			&cli.StringSliceFlag{Name: "p2p-listen", Usage: "swarm listen multiaddr (default an ephemeral loopback port)"},
		},
		Commands: []*cli.Command{
			{
				Name:  "serve",
				Usage: "forward the v2 services' ConnectRPC on a local listener",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "listen", Aliases: []string{"l"}, Usage: "local address (default " + DefaultListen + ")"},
					&cli.BoolFlag{Name: "allow-writes", Usage: "forward calls that mutate state or run code"},
					&cli.BoolFlag{Name: "allow-sensitive", Usage: "forward reads of secrets, certificates, deploy keys and tenant data"},
					&cli.StringSliceFlag{Name: "origin", Usage: "browser origin allowed to call (repeatable)"},
					&cli.StringSliceFlag{Name: "host", Usage: "Host header accepted besides loopback (repeatable)"},
				},
				Action: func(c *cli.Context) error { return serve(c, env) },
			},
			{
				Name:  "ui",
				Usage: "the operator TUI",
				Flags: []cli.Flag{&cli.DurationFlag{Name: "refresh", Value: 5 * time.Second, Usage: "refresh interval, 0 for manual"}},
				Action: func(c *cli.Context) error {
					return withSeat(c, env, func(s Seat, q *query.Client) error {
						return env.RunUI(tui.New(q, c.Duration("refresh")))
					})
				},
			},
			{
				Name: "nodes", Usage: "the fleet, from discovery", Flags: []cli.Flag{output, wait},
				Action: func(c *cli.Context) error {
					return withSeat(c, env, func(s Seat, q *query.Client) error {
						listen(c)
						ns := q.Nodes()
						return query.Write(env.Out, c.String("output"), query.NodesTable(ns), ns)
					})
				},
			},
			{
				Name: "services", Usage: "what the fleet serves, from discovery", Flags: []cli.Flag{output, wait},
				Action: func(c *cli.Context) error {
					return withSeat(c, env, func(s Seat, q *query.Client) error {
						listen(c)
						ss := q.Services()
						return query.Write(env.Out, c.String("output"), query.ServicesTable(ss), ss)
					})
				},
			},
			{
				Name: "projects", Usage: "projects and their deployments", Flags: []cli.Flag{output},
				Action: func(c *cli.Context) error {
					return withSeat(c, env, func(s Seat, q *query.Client) error {
						ps, err := q.Projects(c.Context)
						if err != nil {
							return err
						}
						return query.Write(env.Out, c.String("output"), query.ProjectsTable(ps), ps)
					})
				},
			},
			{
				Name: "jobs", Usage: "build jobs", Flags: []cli.Flag{output, &cli.StringFlag{Name: "project", Usage: "only this project's"}},
				Action: func(c *cli.Context) error {
					return withSeat(c, env, func(s Seat, q *query.Client) error {
						js, err := q.Jobs(c.Context, c.String("project"))
						if err != nil {
							return err
						}
						return query.Write(env.Out, c.String("output"), query.JobsTable(js), js)
					})
				},
			},
		},
	}
}

// listen waits out --wait, so the fleet's reports have arrived.
func listen(c *cli.Context) {
	select {
	case <-c.Context.Done():
	case <-time.After(c.Duration("wait")):
	}
}

func withSeat(c *cli.Context, env Env, do func(Seat, *query.Client) error) error {
	cfg, _, err := config(c)
	if err != nil {
		return err
	}
	s, err := env.Join(c.Context, cfg)
	if err != nil {
		return err
	}
	defer s.Close()
	t := route.New(s.Host(), s)
	defer t.Close()
	return do(s, query.New(s, t.Client(), route.BaseURL))
}

func serve(c *cli.Context, env Env) error {
	cfg, p, err := config(c)
	if err != nil {
		return err
	}
	addr := pick(c.String("listen"), p.Listen)
	if addr == "" {
		addr = DefaultListen
	}
	opts := server.Options{
		Allow:   policy.Allow{Writes: c.Bool("allow-writes"), Sensitive: c.Bool("allow-sensitive")},
		Origins: pickList(c.StringSlice("origin"), p.Origins),
		Hosts:   c.StringSlice("host"),
	}
	s, err := env.Join(c.Context, cfg)
	if err != nil {
		return err
	}
	defer s.Close()
	t := route.New(s.Host(), s)
	defer t.Close()
	h, err := server.New(t, opts)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: h, Protocols: server.Protocols(), ReadHeaderTimeout: 10 * time.Second}
	fmt.Fprintf(env.Out, "taucorder: forwarding tau v2 ConnectRPC on http://%s (writes=%v sensitive=%v, origins=%s)\n",
		ln.Addr(), opts.Allow.Writes, opts.Allow.Sensitive, strings.Join(opts.Origins, ","))
	if env.Listening != nil {
		env.Listening(ln.Addr().String())
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case <-c.Context.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		<-done
		return nil
	case err := <-done:
		return err
	}
}
