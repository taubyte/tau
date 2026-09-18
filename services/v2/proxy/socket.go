package proxy

import (
	"fmt"
	"net"
	"slices"

	"github.com/taubyte/tau/pkg/project"
)

// socket.go decides where a proxy answers, which is: on every address this node
// has, on a port the kernel picked.
//
// # There are no addresses here, and there is nowhere to put one
//
// An address has to be ROUTED to this machine and present on an interface
// before anything can bind it — an operational fact no config can invent. So
// tau listens on all of them, dns answers with the ones the node reports, and
// nothing in between ever names one. The vocabulary agrees: `proxy` declares a
// host, a service and a protocol, and not one address or client-facing port.
// The only port an author writes is the one their container binds INSIDE
// itself, which never leaves the machine.
//
// AN EARLIER VERSION DERIVED AN ADDRESS PER PROXY inside an operator's prefix.
// It passed its tests because those asserted the derived socket as a STRING and
// bound 127.0.0.1 separately — one level too shallow to ever attempt the bind
// that would have failed with "cannot assign requested address".
//
// # The port is the kernel's, and SRV carries it
//
// At L4 the port IS the address, so an authored one would be the first value in
// this vocabulary with a global collision domain. Asking the kernel for a free
// one leaves no table to replicate, reconcile or leak, and what it hands back
// is a fact about this node — which is the granularity an SRV record publishes
// at, and why the schema derives `_<service>._<protocol>` for every proxy.
//
// # Both families, for free
//
// An empty host binds every address, v4 and v6 both, on every platform this
// runs on. Ports are not scarce the way addresses are — there are 65535 on each
// — so neither family needs an allocator, and neither needs saying twice.

// Transport is what every proxy carries, and it is a constant because the kind
// is.
//
// THE VOCABULARY HAS NO `protocol` FIELD. It had one, as a set of three, and
// two of them were never servable — a tunnel here is a reliable ordered stream,
// and carrying a datagram protocol over one turns packet loss into a latency
// spike. With one value left it was a field nobody could get wrong, so the
// transport moved to where the kind is implemented: here, once, rather than in
// every resource that declares a proxy.
const Transport = "tcp"

// Socket is where a proxy ended up listening, and how a client finds it: an
// SRV lookup of `_<Protocol>._<Transport>.<Host>` answers with Port.
type Socket struct {
	// Host is the name a client resolves, e.g. "db.shop.example.com".
	Host string

	// Protocol is the IANA service label, e.g. "postgres".
	Protocol string

	// Transport is what carries it, e.g. "tcp".
	Transport string

	// Port is what the kernel gave this proxy on this node.
	Port uint16
}

// listen binds this proxy on every address this node has.
func (s *Service) listen(proj *project.Project, r project.Resource, id, name string) (net.Listener, Socket, error) {
	// THE KERNEL IS THE ALLOCATOR: an empty host is every address, and port
	// zero is "whichever is free".
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, Socket{}, fmt.Errorf("proxy: listening for %s: %w", name, err)
	}

	sock := s.socketOf(proj, r, id, name)
	if a, ok := l.Addr().(*net.TCPAddr); ok {
		sock.Port = uint16(a.Port)
	}
	if sock.Host == "" {
		// It listens, and nothing can find it: a generated name needs the
		// cloud's own zone appended and this node was not told one.
		s.opts.warn("proxy: %s answers on no name this cloud can spell, so nothing will resolve it", name)
	}
	return l, sock, nil
}

// socketOf is how a client looks this proxy up.
//
// AN OMITTED SERVICE IS THE RESOURCE'S OWN NAME, which is what the vocabulary
// says — not the name the caller looked it up by, which may have resolved
// through a parent scope and be neither the resource's nor a service anybody
// has heard of.
func (s *Service) socketOf(proj *project.Project, r project.Resource, id, name string) Socket {
	sock := Socket{
		Host:      s.hostOf(proj, id),
		Protocol:  r.String("service"),
		Transport: Transport,
	}
	if sock.Protocol == "" {
		sock.Protocol = r.String("name")
	}
	if sock.Protocol == "" {
		sock.Protocol = name
	}
	return sock
}

// hostOf is the complete name this proxy answers on.
//
// THE NAME IS ASSEMBLED ONCE, BY THE VOCABULARY. project.Hosts puts the
// resource's label, its zone and the project's own label together; this adds
// the one part a compiled project cannot know — the cloud's own zone, which is
// operator config for exactly the reason the gateway's CloudFqdn is.
//
// An empty answer means nothing here can spell the name: a generated host on a
// node that was told no zone. Publishing a partial one would put a name in DNS
// that no client will ever ask for.
func (s *Service) hostOf(proj *project.Project, id string) string {
	kind, scope, name, ok := proj.Locate(id)
	if !ok {
		return ""
	}
	for _, h := range proj.Hosts() {
		if h.Kind != kind || h.Name != name || !slices.Equal(h.Scope, scope) {
			continue
		}
		if !h.Generated {
			return h.Fqdn
		}
		if s.cfg.CloudFqdn == "" {
			return ""
		}
		return h.Fqdn + "." + s.cfg.CloudFqdn
	}
	return ""
}
