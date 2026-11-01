package proxy

import (
	"fmt"
	"net"
	"slices"

	"github.com/taubyte/tau/pkg/specs"
)

// An address must be routed to the machine before it can be bound, so a proxy
// listens on all of them and dns answers with the ones the node reports. The only
// port an author writes is one their container binds inside itself.
//
// The client-facing port is the kernel's, published by SRV, so there is no
// collision domain or table to replicate. An empty host binds v4 and v6 alike.

// Transport is what every proxy carries. The vocabulary has no `protocol` field: a
// tunnel here is a reliable ordered stream, so a datagram protocol was never
// servable.
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
func (s *Service) listen(proj *specs.Project, r specs.Resource, id, name string) (net.Listener, Socket, error) {
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

// socketOf is how a client looks this proxy up. An omitted service is the resource's
// own name, not the name it was looked up by through a parent scope.
func (s *Service) socketOf(proj *specs.Project, r specs.Resource, id, name string) Socket {
	declared := specs.ReadProxy(r)
	sock := Socket{
		Host:      s.hostOf(proj, id),
		Protocol:  declared.Service,
		Transport: Transport,
	}
	if sock.Protocol == "" {
		sock.Protocol = declared.Name
	}
	if sock.Protocol == "" {
		sock.Protocol = name
	}
	return sock
}

// hostOf is the complete name this proxy answers on: project.Hosts plus the cloud's
// zone. Empty when a generated host meets a node told no zone, rather than publishing
// a partial name.
func (s *Service) hostOf(proj *specs.Project, id string) string {
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
