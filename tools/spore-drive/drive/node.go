package drive

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"slices"
	"sort"

	"github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/crypto"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/node/boot"
	"github.com/taubyte/tau/pkg/tenancy"
	"github.com/taubyte/tau/tools/spore-drive/config"
	"github.com/taubyte/tau/utils"
)

// node.go turns a spore-drive cloud config — written for v1, where a host ran
// one tau process per shape — into the v2 node files a host runs.
//
// THE INPUT FORMAT DOES NOT CHANGE. Operators' cloud configs are authoritative
// and keep parsing as they are; what changes is what is emitted from them:
//
//   - ONE NODE PER HOST. A v2 node runs any set of services in one process, so
//     the services of every shape on a host are unioned into one node. The
//     exception is the one combination a node refuses (node.ErrBothHalves):
//     when the union holds both build halves, build-frontend runs as a second
//     node of its own (see frontendInstance).
//   - v1 SERVICE NAMES ARE MAPPED onto the v2 services that do their work
//     (v1Services); a v2 name passes through as written.
//   - WHAT HAS NO v2 HOME IS READ AND NOT EMITTED, and each deploy logs which
//     of those fields a host's config set (ignoredFields).

var logger = log.Logger("tau.spore-drive")

// mainInstance names a host's node. The systemd unit is tau@<instance>, its
// file /tb/config/<instance>.yaml; a split-off build-frontend node is named
// after that service.
const mainInstance = "node"

// v1Services maps each v1 service name onto the v2 services doing its work.
// accounts is absent on purpose: it has no v2 counterpart in this tree, and is
// reported as ignored.
var v1Services = map[string][]string{
	"auth":      {node.Auth, node.Secrets},
	"tns":       {node.Register},
	"seer":      {node.Discovery, node.DNS},
	"substrate": {node.Worker, node.Gateway},
	"gateway":   {node.Gateway},
	"hoarder":   {node.Database, node.Primitives},
	"patrick":   {node.BuildFrontend},
	"monkey":    {node.BuildBackend},
}

// dropped are v1 service names that are read and run nothing in v2.
var dropped = []string{"accounts"}

// instance is one v2 node a host runs.
type instance struct {
	name string
	port int
	file boot.File
}

// hostNodes is what a host runs, and which of its config's fields have no v2
// home.
type hostNodes struct {
	instances []instance
	ignored   []string
}

// primaryShape is the shape whose key and main port a host's node uses. The
// first by name, so every host computes the same answer for every other —
// bootstrap addresses depend on it.
func primaryShape(p config.Parser, hostName string) (string, error) {
	shapes := slices.Clone(p.Hosts().Host(hostName).Shapes().List())
	if len(shapes) == 0 {
		return "", fmt.Errorf("host %s runs no shape", hostName)
	}
	sort.Strings(shapes)
	return shapes[0], nil
}

// v2Services unions and maps the services of a host's shapes.
func v2Services(p config.Parser, hostName string) ([]string, []string, error) {
	var out, droppedHere []string
	shapes := slices.Clone(p.Hosts().Host(hostName).Shapes().List())
	sort.Strings(shapes)
	for _, shape := range shapes {
		for _, svc := range p.Shapes().Shape(shape).Services().List() {
			if slices.Contains(dropped, svc) {
				droppedHere = append(droppedHere, svc)
				continue
			}
			mapped, ok := v1Services[svc]
			if !ok {
				if !slices.Contains(node.All, svc) && svc != node.BuildFrontend {
					return nil, nil, fmt.Errorf("shape %s: %q is neither a v1 service nor a v2 one", shape, svc)
				}
				mapped = []string{svc}
			}
			for _, m := range mapped {
				if !slices.Contains(out, m) {
					out = append(out, m)
				}
			}
		}
	}
	return out, droppedHere, nil
}

func hostIPs(p config.Parser, hostName string) ([]net.IP, error) {
	addrs := p.Hosts().Host(hostName).Addresses().List()
	out := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr)
		if err != nil {
			ip = net.ParseIP(addr)
			if ip == nil {
				return nil, fmt.Errorf("`%s` is not valid IP or CIDR", addr)
			}
		}
		out = append(out, ip)
	}
	return out, nil
}

// bootstrap lists the main node of every bootstrap host but this one.
func bootstrap(p config.Parser, self string) ([]string, error) {
	out := make([]string, 0)
	b := p.Cloud().P2P().Bootstrap()
	for _, sh := range b.List() {
		for _, ht := range b.Shape(sh).List() {
			if ht == self {
				continue
			}
			primary, err := primaryShape(p, ht)
			if err != nil {
				return nil, err
			}
			id := p.Hosts().Host(ht).Shapes().Instance(primary).Id()
			port := int(p.Shapes().Shape(primary).Ports().Get("main"))
			ips, err := hostIPs(p, ht)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				addr := fmt.Sprintf("/ip4/%s/tcp/%d/p2p/%s", ip, port, id)
				if !slices.Contains(out, addr) {
					out = append(out, addr)
				}
			}
		}
	}
	return out, nil
}

func readAll(open func() (io.ReadCloser, error)) (string, error) {
	r, err := open()
	if err != nil {
		return "", err
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	return string(data), err
}

// derivedKey is the build-frontend node's identity when it has to be split off
// a host's node: a second peer needs a second key, and the cloud config — which
// spore-drive must not rewrite — holds one per shape. Derived from the primary
// key so it is the same on every deploy without being stored anywhere.
func derivedKey(primary, purpose string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(primary)
	if err != nil {
		return "", fmt.Errorf("decode private key failed with %w", err)
	}
	seed := sha256.Sum256(append([]byte(purpose+"\x00"), raw...))
	priv, _, err := crypto.GenerateEd25519Key(bytes.NewReader(seed[:]))
	if err != nil {
		return "", err
	}
	out, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

// nodesFor computes the v2 node files a host runs.
func (d *sporedrive) nodesFor(hostName string) (*hostNodes, error) {
	p := d.parser
	hc := p.Hosts().Host(hostName)

	primary, err := primaryShape(p, hostName)
	if err != nil {
		return nil, err
	}
	services, droppedHere, err := v2Services(p, hostName)
	if err != nil {
		return nil, err
	}

	// EVERY NODE RUNS STATS. It is how a v2 node is seen by the fleet at all:
	// a node that does not gossip its services is never routed to.
	if !slices.Contains(services, node.Discovery) {
		services = append(services, node.Discovery)
	}

	swarmFile, err := readAll(p.Cloud().P2P().Swarm().Open)
	if err != nil {
		return nil, fmt.Errorf("failed to open swarm key: %w", err)
	}
	// The file is written on one line; libp2p reads the three-line form.
	swarm, err := utils.FormatSwarmKey(swarmFile)
	if err != nil {
		return nil, fmt.Errorf("swarm key: %w", err)
	}

	peers, err := bootstrap(p, hostName)
	if err != nil {
		return nil, err
	}
	ips, err := hostIPs(p, hostName)
	if err != nil {
		return nil, err
	}

	added := p.Added()
	served, err := tenancy.Parse(p.Tenancy())
	if err != nil {
		return nil, fmt.Errorf("tenancy.yaml: %w", err)
	}
	mainPort := int(p.Shapes().Shape(primary).Ports().Get("main"))
	key := hc.Shapes().Instance(primary).Key()

	file := func(services []string, port int, key string) boot.File {
		announce := make([]string, len(ips))
		for i, ip := range ips {
			announce[i] = fmt.Sprintf("/ip4/%s/tcp/%d", ip, port)
		}
		f := boot.File{
			Node: node.Config{
				Services: services,
				Cloud:    p.Cloud().Domain().Root(),
				// ADDED SERVICES' CONFIGURATION IS COPIED, NEVER READ. Each
				// service decodes its own entry; a node built without one
				// refuses the entry by name.
				Added: added,
				// The tenancy is cloud-wide: every node carries it, and the
				// ones that register, link or build a repository apply it.
				Tenancy: served.Namespaces(),
			},
			Listen:     []string{fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port)},
			Announce:   announce,
			Bootstrap:  peers,
			PrivateKey: key,
			SwarmKey:   base64.StdEncoding.EncodeToString(swarm),
		}
		if slices.Contains(services, node.Gateway) {
			f.Node.Gateway.CloudFqdn = p.Cloud().Domain().Generated()
			f.HTTP = ":80"
			f.HTTPS = ":443"
		}
		return f
	}

	out := &hostNodes{ignored: d.ignoredFields(hostName, droppedHere)}

	if slices.Contains(services, node.BuildFrontend) && slices.Contains(services, node.BuildBackend) {
		frontendKey, err := derivedKey(key, node.BuildFrontend)
		if err != nil {
			return nil, err
		}
		services = slices.DeleteFunc(services, func(s string) bool { return s == node.BuildFrontend })
		out.instances = append(out.instances, instance{
			name: node.BuildFrontend,
			port: mainPort + 1,
			file: file([]string{node.BuildFrontend, node.Discovery}, mainPort+1, frontendKey),
		})
	}
	out.instances = append([]instance{{name: mainInstance, port: mainPort, file: file(services, mainPort, key)}}, out.instances...)
	return out, nil
}

// ignoredFields names what this host's config sets that no v2 node reads.
func (d *sporedrive) ignoredFields(hostName string, droppedServices []string) []string {
	p := d.parser
	var out []string
	add := func(set bool, what string) {
		if set {
			out = append(out, what)
		}
	}

	if lat, lng := p.Hosts().Host(hostName).Location(); lat != 0 || lng != 0 {
		out = append(out, "hosts."+hostName+".location (a v2 node reads no location)")
	}
	for _, shape := range p.Hosts().Host(hostName).Shapes().List() {
		for _, port := range p.Shapes().Shape(shape).Ports().List() {
			add(port != "main", "shapes."+shape+".ports."+port)
		}
		add(len(p.Shapes().Shape(shape).Plugins().List()) > 0, "shapes."+shape+".plugins")
	}
	for _, svc := range droppedServices {
		out = append(out, "service "+svc)
	}
	add(len(p.Cloud().Domain().Hosts()) > 0, "cloud.domain.hosts")
	add(domainKeys(p), "cloud.domain.validation (a v2 node signs no domain claims)")
	add(p.TenancyApp(), "tenancy.app (a v2 node checks the namespace, not membership)")
	return out
}

// Node is one tau process a host runs, as a plan shows it.
type Node struct {
	Name     string
	Port     int
	Services []string
}

// Nodes is what a host runs, and what of its config no v2 node reads.
type Nodes struct {
	Nodes   []Node
	Ignored []string
}

// Nodes computes what a host runs without touching it: the plan of one host.
func (d *sporedrive) Nodes(hostName string) (*Nodes, error) {
	hn, err := d.nodesFor(hostName)
	if err != nil {
		return nil, err
	}
	out := &Nodes{Ignored: hn.ignored}
	for _, inst := range hn.instances {
		out.Nodes = append(out.Nodes, Node{Name: inst.name, Port: inst.port, Services: inst.file.Node.Services})
	}
	return out, nil
}

// domainKeys reports whether the config names a domain-validation key pair.
func domainKeys(p config.Parser) bool {
	for _, open := range []func() (io.ReadCloser, error){
		p.Cloud().Domain().Validation().OpenPrivateKey,
		p.Cloud().Domain().Validation().OpenPublicKey,
	} {
		if f, err := open(); err == nil {
			f.Close()
			return true
		}
	}
	return false
}
