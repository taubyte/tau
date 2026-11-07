package app

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/multiformats/go-multiaddr"
	authP2P "github.com/taubyte/tau/clients/p2p/auth"
	"github.com/taubyte/tau/core/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/pkg/raft"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/utils"
	"github.com/urfave/cli/v2"

	libp2pPeer "github.com/libp2p/go-libp2p/core/peer"
)

// Operator commands read a node's config like `tau start` and talk to the
// running node over p2p as a client. They start no service; the only socket
// they open is the client's own ephemeral loopback port.

// certAuth is the part of the auth client the cert commands use.
type certAuth interface {
	ListCertificates() ([]string, error)
	GetRawCertificate(domain string) ([]byte, error)
	DeleteCertificate(domain string) error
}

type shapeConf struct {
	shape string
	pid   string
	conf  config.Config
}

// operator holds what the commands reach outside the process; tests fake it.
type operator struct {
	auth    func(ctx context.Context, sc shapeConf) (certAuth, func(), error)
	raft    func(ctx context.Context, sc shapeConf, namespace string) (*raft.ClusterInfoResponse, error)
	journal func(shape string) ([]string, error)
	portUp  func(port int) bool
}

var ops = operator{
	auth: func(ctx context.Context, sc shapeConf) (certAuth, func(), error) {
		n, done, err := dialOwnNode(ctx, sc)
		if err != nil {
			return nil, nil, err
		}
		c, err := authP2P.New(ctx, n)
		if err != nil {
			done()
			return nil, nil, err
		}
		return c.(certAuth), done, nil
	},
	raft: func(ctx context.Context, sc shapeConf, namespace string) (*raft.ClusterInfoResponse, error) {
		n, done, err := dialOwnNode(ctx, sc)
		if err != nil {
			return nil, err
		}
		defer done()
		pid, err := libp2pPeer.Decode(sc.pid)
		if err != nil {
			return nil, err
		}
		return raft.ClusterInfoOf(n, namespace, pid)
	},
	journal: func(shape string) ([]string, error) {
		out, err := exec.Command("journalctl", "-u", "tau@"+shape, "-n", "5000", "--no-pager", "-o", "cat").Output()
		if err != nil {
			return nil, err
		}
		return strings.Split(string(out), "\n"), nil
	},
	portUp: func(port int) bool {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 500*time.Millisecond)
		if err == nil {
			c.Close()
		}
		return err == nil
	},
}

// dialOwnNode starts a p2p client bootstrapped to this node only, as the
// node's own lite client does, in a temp repo removed by done.
func dialOwnNode(ctx context.Context, sc shapeConf) (peer.Node, func(), error) {
	announce := sc.conf.P2PAnnounce()
	if len(announce) == 0 {
		return nil, nil, errors.New("p2p announce is empty in the config")
	}
	ma, err := multiaddr.NewMultiaddr(announce[0])
	if err != nil {
		return nil, nil, err
	}
	port, err := ma.ValueForProtocol(multiaddr.P_TCP)
	if err != nil {
		return nil, nil, err
	}
	self, err := utils.ConvertToAddrInfo([]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%s/p2p/%s", port, sc.pid)})
	if err != nil {
		return nil, nil, err
	}
	dir, err := os.MkdirTemp("", "tau-operator-*")
	if err != nil {
		return nil, nil, err
	}
	n, err := peer.NewClientNode(ctx, dir, keypair.NewRaw(), sc.conf.SwarmKey(), []string{"/ip4/127.0.0.1/tcp/0"}, nil, true, self)
	if err != nil {
		os.RemoveAll(dir)
		return nil, nil, err
	}
	done := func() { n.Close(); os.RemoveAll(dir) }
	if err = n.WaitForSwarm(10 * time.Second); err != nil {
		done()
		return nil, nil, fmt.Errorf("this node does not answer on p2p port %s: %w", port, err)
	}
	return n, done, nil
}

var operatorFlags = []cli.Flag{
	&cli.PathFlag{Name: "root", Value: config.DefaultRoot, Usage: "Folder where tau is installed"},
	&cli.StringFlag{Name: "shape", Aliases: []string{"s"}, Value: "all", Usage: "shape to read, or all"},
	&cli.BoolFlag{Name: "json", Usage: "print JSON"},
}

// loadShapes reads the config of shape, or of every shape in <root>/config.
func loadShapes(ctx *cli.Context) (string, []shapeConf, error) {
	root := ctx.Path("root")
	if !filepath.IsAbs(root) {
		return "", nil, fmt.Errorf("root folder `%s` is not absolute", root)
	}
	shapes := []string{ctx.String("shape")}
	if shapes[0] == "all" {
		files, _ := filepath.Glob(filepath.Join(root, "config", "*.yaml"))
		shapes = shapes[:0]
		for _, f := range files {
			shapes = append(shapes, strings.TrimSuffix(filepath.Base(f), ".yaml"))
		}
		if len(shapes) == 0 {
			return "", nil, fmt.Errorf("no shape config in %s/config", root)
		}
	}
	out := make([]shapeConf, 0, len(shapes))
	for _, s := range shapes {
		pid, conf, _, err := loadSourceConfig(root, s, path.Join(root, "config", s+".yaml"), false)
		if err != nil {
			return "", nil, fmt.Errorf("shape %s: %w", s, err)
		}
		out = append(out, shapeConf{shape: s, pid: pid, conf: conf})
	}
	return root, out, nil
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ---- tau certs ----

// certFiles are autocert's non-certificate entries (keys, tokens).
var certFiles = regexp.MustCompile(`(\+token|\+rsa|\+key|\.key)$`)

type certRow struct {
	Name     string    `json:"name"`
	Where    string    `json:"where"`
	Issuer   string    `json:"issuer,omitempty"`
	NotAfter time.Time `json:"not_after,omitzero"`
	Error    string    `json:"error,omitempty"`
}

func certInfo(r *certRow, data []byte) {
	for {
		var b *pem.Block
		if b, data = pem.Decode(data); b == nil {
			r.Error = "no certificate in it"
			return
		}
		if b.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			r.Error = err.Error()
			return
		}
		r.Issuer, r.NotAfter = c.Issuer.CommonName, c.NotAfter
		if r.Issuer == "" && len(c.Issuer.Organization) > 0 {
			r.Issuer = c.Issuer.Organization[0]
		}
		return
	}
}

// acmeDirs are a shape's local acme copies: its node's and its client node's.
func acmeDirs(root, shape string) []string {
	return []string{
		filepath.Join(root, "storage", shape, "local", "acme"),
		filepath.Join(root, "storage", shape+"_client", "local", "acme"),
	}
}

func listCerts(ctx context.Context, root string, shapes []shapeConf) []certRow {
	rows := []certRow{}
	a, done, err := ops.auth(ctx, shapes[0])
	if err == nil {
		var names []string
		if names, err = a.ListCertificates(); err == nil {
			sort.Strings(names)
			for _, n := range names {
				r := certRow{Name: n, Where: "auth"}
				if data, err := a.GetRawCertificate(n); err != nil {
					r.Error = err.Error()
				} else {
					certInfo(&r, data)
				}
				rows = append(rows, r)
			}
		}
		done()
	}
	if err != nil {
		rows = append(rows, certRow{Where: "auth", Error: err.Error()})
	}
	for _, sc := range shapes {
		for _, dir := range acmeDirs(root, sc.shape) {
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if e.IsDir() || certFiles.MatchString(e.Name()) {
					continue
				}
				r := certRow{Name: e.Name(), Where: dir}
				if data, err := os.ReadFile(filepath.Join(dir, e.Name())); err != nil {
					r.Error = err.Error()
				} else {
					certInfo(&r, data)
				}
				rows = append(rows, r)
			}
		}
	}
	return rows
}

func writeCerts(w io.Writer, rows []certRow) {
	for _, r := range rows {
		if r.Name == "" {
			fmt.Fprintf(w, "%s: %s\n", r.Where, r.Error)
			continue
		}
		info := r.Error
		if info == "" {
			info = fmt.Sprintf("%-30s not after %s", r.Issuer, r.NotAfter.UTC().Format(time.RFC3339))
		}
		fmt.Fprintf(w, "%-40s %s  [%s]\n", r.Name, info, r.Where)
	}
}

// checkCertName refuses what is not one domain name: no dot, or a path.
func checkCertName(name string) error {
	if !strings.Contains(name, ".") || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("`%s` is not a certificate name: give a domain such as *.gen.example.com", name)
	}
	return nil
}

type deleteResult struct {
	Name    string   `json:"name"`
	Removed []string `json:"removed_local"`
	Others  string   `json:"other_nodes"`
}

// deleteCert removes name from auth, then this node's local copies of
// exactly that name (os.Remove takes no glob, so `*.x` is that one file).
func deleteCert(ctx context.Context, root string, shapes []shapeConf, name string) (deleteResult, error) {
	res := deleteResult{Name: name, Removed: []string{}}
	if err := checkCertName(name); err != nil {
		return res, err
	}
	a, done, err := ops.auth(ctx, shapes[0])
	if err != nil {
		return res, err
	}
	err = a.DeleteCertificate(name)
	done()
	if err != nil {
		return res, err
	}
	for _, sc := range shapes {
		for _, dir := range acmeDirs(root, sc.shape) {
			p := filepath.Join(dir, name)
			if err := os.Remove(p); err == nil {
				res.Removed = append(res.Removed, p)
			} else if !os.IsNotExist(err) {
				return res, err
			}
		}
	}
	// the leading * is a glob in a shell: [*] matches only a literal *
	glob := name
	if strings.HasPrefix(glob, "*") {
		glob = "[*]" + glob[1:]
	}
	res.Others = fmt.Sprintf("other nodes keep their local copy: on each, run rm -f %s/storage/*/local/acme/%s (or restart tau there)", root, glob)
	return res, nil
}

func certsCommand() *cli.Command {
	return &cli.Command{
		Name:  "certs",
		Usage: "certificates in auth's ACME store and this node's local copies",
		Subcommands: []*cli.Command{
			{
				Name:  "list",
				Usage: "list certificates: name, issuer, not-after (read-only)",
				Flags: operatorFlags,
				Action: func(ctx *cli.Context) error {
					root, shapes, err := loadShapes(ctx)
					if err != nil {
						return err
					}
					rows := listCerts(ctx.Context, root, shapes)
					if ctx.Bool("json") {
						return printJSON(ctx.App.Writer, rows)
					}
					writeCerts(ctx.App.Writer, rows)
					return nil
				},
			},
			{
				Name:      "delete",
				Usage:     "delete a certificate from auth and this node's local copy of it",
				ArgsUsage: "<name>",
				Flags:     operatorFlags,
				Action: func(ctx *cli.Context) error {
					if ctx.NArg() != 1 {
						return errors.New("give one certificate name, e.g. tau certs delete '*.gen.example.com'")
					}
					root, shapes, err := loadShapes(ctx)
					if err != nil {
						return err
					}
					res, err := deleteCert(ctx.Context, root, shapes, ctx.Args().First())
					if err != nil {
						return err
					}
					if ctx.Bool("json") {
						return printJSON(ctx.App.Writer, res)
					}
					fmt.Fprintf(ctx.App.Writer, "deleted %s from auth\n", res.Name)
					for _, p := range res.Removed {
						fmt.Fprintf(ctx.App.Writer, "removed %s\n", p)
					}
					fmt.Fprintln(ctx.App.Writer, res.Others)
					return nil
				},
			},
		},
	}
}

// ---- tau node health ----

type shapeHealth struct {
	Shape    string          `json:"shape"`
	Services []string        `json:"services"`
	Ports    map[string]bool `json:"ports_up"`
	Raft     string          `json:"raft,omitempty"`
	Members  int             `json:"raft_members,omitempty"`
	Errors   []string        `json:"last_errors"`
	LogNote  string          `json:"log,omitempty"`
}

type health struct {
	Version string        `json:"version"`
	Commit  string        `json:"commit"`
	Shapes  []shapeHealth `json:"shapes"`
}

func nodeHealth(ctx context.Context, shapes []shapeConf) health {
	h := health{Version: Version, Commit: Commit}
	for _, sc := range shapes {
		s := shapeHealth{Shape: sc.shape, Services: sc.conf.Services(), Ports: map[string]bool{}, Errors: []string{}}
		for name, port := range sc.conf.Ports() {
			s.Ports[fmt.Sprintf("%s:%d", name, port)] = ops.portUp(port)
		}
		if commonSpecs.RequiresRaftCluster(s.Services) {
			ns := sc.conf.Cluster()
			if ns == "" {
				ns = "main"
			}
			info, err := ops.raft(ctx, sc, ns)
			switch {
			case err != nil:
				s.Raft = "unknown: " + err.Error()
			case info.LeaderID == "":
				s.Raft, s.Members = "no leader", info.MemberCount
			case info.LeaderID == sc.pid:
				s.Raft, s.Members = "leader", info.MemberCount
			default:
				s.Raft, s.Members = "follower (leader "+info.LeaderID+")", info.MemberCount
			}
		}
		if lines, err := ops.journal(sc.shape); err != nil {
			s.LogNote = "log not reachable: " + err.Error()
		} else {
			for _, l := range lines {
				if strings.Contains(l, "ERROR") {
					s.Errors = append(s.Errors, l)
				}
			}
			s.Errors = s.Errors[max(0, len(s.Errors)-5):]
		}
		h.Shapes = append(h.Shapes, s)
	}
	return h
}

func writeHealth(w io.Writer, h health) {
	fmt.Fprintf(w, "tau %s (commit %s)\n", h.Version, h.Commit)
	for _, s := range h.Shapes {
		fmt.Fprintf(w, "\nshape %s: services %s\n", s.Shape, strings.Join(s.Services, ", "))
		ports := make([]string, 0, len(s.Ports))
		for p := range s.Ports {
			ports = append(ports, p)
		}
		sort.Strings(ports)
		for _, p := range ports {
			state := "DOWN"
			if s.Ports[p] {
				state = "up"
			}
			fmt.Fprintf(w, "  port %-16s %s\n", p, state)
		}
		if s.Raft != "" {
			fmt.Fprintf(w, "  raft %s, %d members\n", s.Raft, s.Members)
		}
		if s.LogNote != "" {
			fmt.Fprintf(w, "  %s\n", s.LogNote)
		}
		for _, e := range s.Errors {
			fmt.Fprintf(w, "  error: %s\n", e)
		}
	}
}

func nodeCommand() *cli.Command {
	return &cli.Command{
		Name:  "node",
		Usage: "this node's state",
		Subcommands: []*cli.Command{
			{
				Name:  "health",
				Usage: "ports up, raft role and members, last errors, version (read-only)",
				Flags: operatorFlags,
				Action: func(ctx *cli.Context) error {
					_, shapes, err := loadShapes(ctx)
					if err != nil {
						return err
					}
					h := nodeHealth(ctx.Context, shapes)
					if ctx.Bool("json") {
						return printJSON(ctx.App.Writer, h)
					}
					writeHealth(ctx.App.Writer, h)
					return nil
				},
			},
		},
	}
}
