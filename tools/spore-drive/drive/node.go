package drive

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"maps"
	"net"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"time"

	"github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/crypto"

	"github.com/taubyte/tau/instance"
	"github.com/taubyte/tau/instance/boot"
	"github.com/taubyte/tau/p2p/sectls"
	"github.com/taubyte/tau/pkg/tenancy"
	"github.com/taubyte/tau/tools/spore-drive/config"
)

// node.go turns a spore-drive cloud config, written for v1 (one tau process per
// shape), into the v2 node files a host runs. The input format does not change.
//
//   - One node per host: every shape's services are unioned, except that build and
//     runner cannot share a node (instance.ErrBuildAndRunner), so build gets its own
//     (frontendInstance).
//   - v1 service names map onto the v2 services doing their work (v1Services).
//   - What has no v2 home is read, not emitted, and logged per host
//     (ignoredFields).

var logger = log.Logger("tau.spore-drive")

// v1Services maps each v1 service name onto the v2 services doing its work.
// accounts is absent on purpose: it has no v2 counterpart in this tree, and is
// reported as ignored.
var v1Services = map[string][]string{
	"auth":      {instance.Auth, instance.Secrets},
	"tns":       {instance.Register},
	"seer":      {instance.Discovery, instance.DNS},
	"substrate": {instance.Worker, instance.Gateway},
	"gateway":   {instance.Gateway},
	"hoarder":   {instance.Database, instance.Primitives},
	"patrick":   {instance.Build},
	"monkey":    {instance.Runner},
}

// dropped are v1 service names that are read and run nothing in v2.
var dropped = []string{"accounts"}

// unit is one tau instance a host runs. The main one is named after the host's
// primary shape, as its key and port already are: its systemd unit is
// tau@<shape>, its file <root>/config/<shape>.yaml and its data
// <root>/storage/<shape>. A split-off build node is named after that
// service.
type unit struct {
	name string
	port int
	file boot.File
}

// keepsSecrets is whether this node runs the secrets service, and so is
// initialized with the cloud's secrets before it starts.
func (i unit) keepsSecrets() bool { return slices.Contains(i.file.Node.Services, instance.Secrets) }

// safeName is what an instance or path may be spelled with: it names a unit, a
// file and a directory on the host, and reaches a remote shell unquoted.
var safeName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// hostNodes is what a host runs, and which of its config's fields have no v2
// home.
type hostNodes struct {
	instances []unit
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
				if !slices.Contains(instance.Everything(), svc) {
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

// derivedKey is the build node's identity when it has to be split off
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

// certificateValidity is a node certificate's life. Rotation is a redeploy,
// which issues every node a new one.
const certificateValidity = 365 * 24 * time.Hour

// issue certifies a node's key, base64 as the host config holds it.
func issue(ca *sectls.CA, key, cloud string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return nil, fmt.Errorf("decode private key failed with %w", err)
	}
	private, err := crypto.UnmarshalPrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("private key: %w", err)
	}
	return ca.Issue(private.GetPublic(), cloud, certificateValidity)
}

// nodesFor computes the v2 node files a host runs.
func (d *sporedrive) nodesFor(hostName string) (*hostNodes, error) {
	p := d.parser
	hc := p.Hosts().Host(hostName)

	primary, err := primaryShape(p, hostName)
	if err != nil {
		return nil, err
	}
	for _, shape := range hc.Shapes().List() {
		if shape == instance.Build {
			return nil, fmt.Errorf("host %s: a shape cannot be named %s, the name of the node split off for it", hostName, instance.Build)
		}
		if !safeName.MatchString(shape) {
			return nil, fmt.Errorf("host %s: shape %q names a unit and a directory, so it is letters, digits, '.', '_' and '-'", hostName, shape)
		}
	}
	services, droppedHere, err := v2Services(p, hostName)
	if err != nil {
		return nil, err
	}

	// EVERY NODE RUNS STATS. It is how a v2 node is seen by the fleet at all:
	// a node that does not gossip its services is never routed to.
	if !slices.Contains(services, instance.Discovery) {
		services = append(services, instance.Discovery)
	}

	ca, err := p.Cloud().P2P().CA().Load()
	if err != nil {
		return nil, fmt.Errorf("the cloud's CA: %w", err)
	}
	cloud := p.Cloud().Domain().Root()

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
	cloudServices, err := servicesOf(p)
	if err != nil {
		return nil, err
	}
	sections, err := withBuild(added, cloudServices)
	if err != nil {
		return nil, err
	}
	mainPort := int(p.Shapes().Shape(primary).Ports().Get("main"))
	key := hc.Shapes().Instance(primary).Key()

	file := func(services []string, port int, key string) (boot.File, error) {
		certificate, err := issue(ca, key, cloud)
		if err != nil {
			return boot.File{}, err
		}
		announce := make([]string, len(ips))
		for i, ip := range ips {
			announce[i] = fmt.Sprintf("/ip4/%s/tcp/%d", ip, port)
		}
		f := boot.File{
			Node: instance.Config{
				Services: services,
				Cloud:    cloud,
				// ADDED SERVICES' CONFIGURATION IS COPIED, NEVER READ. Each
				// service decodes its own entry; a node built without one
				// refuses the entry by name.
				Sections: sections,
				// The tenancy is cloud-wide: every node carries it, and the
				// ones that register, link or build a repository apply it.
				Tenancy: served.Namespaces(),
				// So is the zone: a build completes generated names with it, as a
				// gateway serves and a cm certifies them.
				CloudFqdn: p.Cloud().Domain().Generated(),
				Forges:    cloudServices.forges,
				Auth:      cloudServices.auth,
				CM:        cloudServices.cm,
			},
			Listen:      []string{fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port)},
			Announce:    announce,
			Bootstrap:   peers,
			PrivateKey:  key,
			CA:          string(ca.CertificatePEM()),
			Certificate: string(certificate),
		}
		if slices.Contains(services, instance.Gateway) {
			f.HTTP = ":80"
			f.HTTPS = ":443"
		}
		return f, nil
	}

	out := &hostNodes{ignored: d.ignoredFields(hostName, droppedHere)}

	if slices.Contains(services, instance.Build) && slices.Contains(services, instance.Runner) {
		buildKey, err := derivedKey(key, instance.Build)
		if err != nil {
			return nil, err
		}
		services = slices.DeleteFunc(services, func(s string) bool { return s == instance.Build })
		split, err := file([]string{instance.Build, instance.Discovery}, mainPort+1, buildKey)
		if err != nil {
			return nil, err
		}
		out.instances = append(out.instances, unit{name: instance.Build, port: mainPort + 1, file: split})
	}
	main, err := file(services, mainPort, key)
	if err != nil {
		return nil, err
	}
	out.instances = append([]unit{{name: primary, port: mainPort, file: main}}, out.instances...)
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

// withBuild is the added services' sections and the two spore-drive writes for the
// build services: build's origins, the consoles auth sends people back to, which may
// call it; and the runner's word on whether declared zones are trusted unproven.
func withBuild(added map[string]any, cloud *cloudServices) (map[string]any, error) {
	own := map[string]any{}
	if origins := consoleOrigins(cloud.auth.Redirects); len(origins) > 0 {
		own[instance.Build] = map[string]any{"origins": origins}
	}
	if cloud.trusted {
		own[instance.Runner] = map[string]any{"domains": map[string]any{"trusted": true}}
	}
	if len(own) == 0 {
		return added, nil
	}
	sections := maps.Clone(added)
	if sections == nil {
		sections = map[string]any{}
	}
	for name, section := range own {
		if _, twice := added[name]; twice {
			return nil, fmt.Errorf("added: %s is configured by spore-drive, not as an added service", name)
		}
		sections[name] = section
	}
	return sections, nil
}

// consoleOrigins are the origins of the console callbacks auth returns to.
func consoleOrigins(redirects []string) []string {
	var out []string
	for _, r := range redirects {
		if u, err := url.Parse(r); err == nil && u.Scheme != "" && u.Host != "" {
			o := u.Scheme + "://" + u.Host
			if !slices.Contains(out, o) {
				out = append(out, o)
			}
		}
	}
	return out
}
