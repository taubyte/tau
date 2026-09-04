// Package dns answers queries, and does nothing else.
//
// It is one half of v1's seer; stats is the other. v1's seer answered DNS AND
// accumulated where everything is, and the two have different shapes: this half
// is read-mostly and latency-bound, the other is write-heavy and
// staleness-tolerant. Splitting them lets each be sized, deployed and reasoned
// about on its own, and it is why this package has no swarm identity, no
// gossip, and one dependency — a read.
//
// # Answering is a projection of a stats read
//
// A query for `shop.example.com` is the question "who serves shop.example.com",
// which is exactly Route. Everything below is the translation of that answer
// into records: which addresses are A and which are AAAA, how many fit, and
// what to say when the answer is empty.
//
// # The three refusals, which are the whole design
//
// NOTHING SERVES THIS NAME IS NOT "THIS NAME DOES NOT EXIST". A name whose
// nodes are all restarting is a name that exists and is momentarily unserved,
// and NXDOMAIN is cached negatively by every resolver between here and the
// client — turning a ten-second gap into minutes of hard failure. SERVFAIL is
// the honest answer and it is the cheap one to recover from.
//
// A QUERY FOR A TYPE THIS SERVICE DOES NOT SERVE IS NOERROR WITH NO ANSWERS,
// never NXDOMAIN. The name exists; this server has no MX for it. Saying
// NXDOMAIN to an MX query would tell the resolver the name itself is absent,
// and a resolver is entitled to apply that to every other type.
//
// # SRV, and why routing needs it
//
// A workload's own port never leaves the node it runs on: the container engine
// allocates a host port and tells the container about it, and a PROXY on that
// node accepts the connection and forwards it over p2p. So what a client needs
// is the node's proxy port, and an A record structurally cannot carry a port.
// SRV can — `priority weight port target` — and its weight field is where the
// load ordering survives into the answer instead of being flattened away.
//
// An SRV target is a NAME and not an address (RFC 2782), so answering SRV means
// minting one name per node under Config.NodeZone and resolving it back to that
// node's addresses. Both halves are here, and the A records for the targets ride
// along in the ADDITIONAL section so a client does not pay a second round trip
// for something this server already knows.
//
// AN ADDRESS THAT IS NOT AN IP IS SKIPPED RATHER THAN GUESSED AT. stats carries
// what nodes report and parses nothing (deliberately — see its doc). A hostname
// in that list is a legitimate thing for a node to report and a thing this
// service cannot put in an A record, so it is passed over, and a node whose
// addresses are ALL unusable is simply not part of the answer.
package dns

import (
	"encoding/base32"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/taubyte/tau/pkg/stats"
)

// Service answers DNS queries from a stats view.
type Service struct {
	cfg  Config
	opts options

	mu      sync.Mutex
	servers []*dns.Server
	closed  bool
}

// New builds the service. It does not listen — Listen does.
//
// cfg is the operator's declared values; opts inject the collaborators. New
// defaults and validates cfg exactly once, then applies the options, then
// checks that everything required actually arrived.
func New(cfg Config, opts ...Option) (*Service, error) {
	c, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	var o options
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, err
		}
	}
	if o.router == nil {
		return nil, ErrNoRouter
	}
	return &Service{cfg: c, opts: o}, nil
}

// Answer builds the reply to one query. It is the whole of this service's
// behaviour, and it touches no socket, so every rule above is a unit test.
func (s *Service) Answer(req *dns.Msg) *dns.Msg {
	reply := new(dns.Msg)
	reply.SetReply(req)
	reply.Authoritative = true

	// A query with no question, or with more than one, is not something to
	// guess at. The protocol allows a count above one and no resolver in
	// practice sends it; answering the first and ignoring the rest would be a
	// reply that does not correspond to the request.
	if len(req.Question) != 1 {
		reply.Rcode = dns.RcodeFormatError
		return reply
	}

	q := req.Question[0]
	if q.Qclass != dns.ClassINET {
		reply.Rcode = dns.RcodeNotImplemented
		return reply
	}

	name := strings.ToLower(strings.TrimSuffix(q.Name, "."))
	if name == "" {
		reply.Rcode = dns.RcodeFormatError
		return reply
	}

	if q.Qtype == dns.TypeSRV {
		return s.answerSRV(reply, name, q.Name)
	}

	// A name under the node zone is one node's own name, minted for an SRV
	// target. It resolves to that node's addresses and nothing else.
	if peer, ok := s.peerOf(name); ok {
		node, found := s.opts.router.Get(peer)
		if !found || node.Age > s.cfg.MaxAge {
			reply.Rcode = dns.RcodeServerFailure
			return reply
		}
		switch q.Qtype {
		case dns.TypeA:
			reply.Answer = s.records([]stats.Node{node}, q.Name, false)
		case dns.TypeAAAA:
			reply.Answer = s.records([]stats.Node{node}, q.Name, true)
		}
		return reply
	}

	nodes := s.opts.router.Route(name, s.cfg.MaxAge)
	if len(nodes) == 0 {
		// See the package doc: this is SERVFAIL and not NXDOMAIN, because
		// "nothing is serving it right now" is not "it does not exist", and
		// the difference is how long a resolver remembers.
		reply.Rcode = dns.RcodeServerFailure
		return reply
	}

	switch q.Qtype {
	case dns.TypeA:
		reply.Answer = s.records(nodes, q.Name, false)
	case dns.TypeAAAA:
		reply.Answer = s.records(nodes, q.Name, true)
	default:
		// The name exists and this server has nothing of that type for it.
		// NOERROR with an empty answer says exactly that; NXDOMAIN would say
		// the name is absent, which a resolver may apply to every other type.
	}
	return reply
}

// answerSRV answers `_service._proto.name` with one record per node serving
// name, targeting that node's own name and its proxy port.
func (s *Service) answerSRV(reply *dns.Msg, name, qname string) *dns.Msg {
	if s.cfg.NodeZone == "" {
		// No zone means no name can be minted that resolves, and a target
		// pointing into a zone nobody delegated is an answer to nowhere.
		return reply
	}

	// _https._tcp.shop.example.com -> shop.example.com. The two underscore
	// labels are the service and protocol, and what stats knows is the rest.
	svc := name
	for range 2 {
		if !strings.HasPrefix(svc, "_") {
			break
		}
		_, rest, ok := strings.Cut(svc, ".")
		if !ok {
			reply.Rcode = dns.RcodeFormatError
			return reply
		}
		svc = rest
	}

	nodes := s.opts.router.Route(svc, s.cfg.MaxAge)
	if len(nodes) == 0 {
		reply.Rcode = dns.RcodeServerFailure
		return reply
	}

	ttl := s.ttlSeconds()
	for _, n := range nodes {
		label := nodeLabel(n.Peer)
		if len(label) > maxLabel {
			// A name that cannot be spelled is not a name to hand out: a
			// truncated label would resolve to somebody else, or to nothing.
			continue
		}
		if n.Port == 0 {
			// A node that has not got its proxy up says so by reporting no
			// port. Routing to it would be routing to a refusal.
			continue
		}
		target := dns.Fqdn(label + "." + s.cfg.NodeZone)
		reply.Answer = append(reply.Answer, &dns.SRV{
			Hdr:      dns.RR_Header{Name: qname, Rrtype: dns.TypeSRV, Class: dns.ClassINET, Ttl: ttl},
			Priority: 0,
			Weight:   weightOf(n.Load),
			Port:     n.Port,
			Target:   target,
		})
		// The target's addresses ride along, so a client does not pay a second
		// round trip for something this server already has in hand.
		reply.Extra = append(reply.Extra, s.records([]stats.Node{n}, target, false)...)
		reply.Extra = append(reply.Extra, s.records([]stats.Node{n}, target, true)...)

		if len(reply.Answer) >= s.cfg.MaxRecords {
			break
		}
	}
	if len(reply.Answer) == 0 {
		reply.Rcode = dns.RcodeServerFailure
	}
	return reply
}

// nodeLabel encodes a peer id into something a DNS label can carry, and
// peerOf decodes it.
//
// A PEER ID CANNOT BE A DNS LABEL AS IT STANDS. It is base58, which is
// case-SENSITIVE, and DNS labels are case-INSENSITIVE — a resolver is entitled
// to change the case of any name it forwards, and this server lowercases what
// it receives because every other name it serves is case-insensitive. Used raw,
// "12D3KooWtwo" comes back as "12d3koowtwo" and matches no node at all. That is
// not a hypothetical: it is what the test for this found.
//
// So the label is lowercase base32 without padding, which is exactly what IPFS
// subdomain gateways do with CIDs and for exactly this reason. Base32 survives
// any case folding, and the encoded form of a ~52-character peer id is about 85
// characters — inside the 63-character label limit only for shorter ids, so
// this is checked rather than assumed: an id that will not fit produces no SRV
// target rather than a truncated one that resolves to somebody else.
const maxLabel = 63

var nodeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func nodeLabel(peer string) string {
	return strings.ToLower(nodeEncoding.EncodeToString([]byte(peer)))
}

// peerOf reports whether a name is one node's own name under the node zone.
func (s *Service) peerOf(name string) (string, bool) {
	if s.cfg.NodeZone == "" {
		return "", false
	}
	suffix := "." + strings.ToLower(s.cfg.NodeZone)
	if !strings.HasSuffix(name, suffix) {
		return "", false
	}
	label := strings.TrimSuffix(name, suffix)
	if label == "" || strings.Contains(label, ".") {
		return "", false
	}
	raw, err := nodeEncoding.DecodeString(strings.ToUpper(label))
	if err != nil {
		return "", false
	}
	return string(raw), true
}

// weightOf turns a load into an SRV weight.
//
// SRV WEIGHT IS PROPORTIONAL SHARE AMONG EQUAL PRIORITIES, so an idle node
// should get a bigger number than a busy one — the inverse of load. A saturated
// node keeps weight 1 rather than 0, because RFC 2782 treats zero as "use only
// if nothing else is available", and this server already refuses to return a
// node it does not want traffic on: not-ready nodes are filtered out entirely,
// so anything still in the list IS an acceptable answer.
func weightOf(load float64) uint16 {
	w := int((1 - load) * 100)
	if w < 1 {
		return 1
	}
	return uint16(w)
}

// records turns nodes into A or AAAA records, in the order stats gave them —
// least loaded first — capped at MaxRecords.
//
// THE ORDER IS THE ROUTING DECISION. Many clients take the first address, and
// resolvers that shuffle will at least be shuffling a set drawn from the least
// loaded nodes rather than from the whole fleet.
func (s *Service) records(nodes []stats.Node, qname string, wantV6 bool) []dns.RR {
	var out []dns.RR
	for _, n := range nodes {
		for _, a := range n.Addresses {
			ip := net.ParseIP(a)
			if ip == nil {
				continue // a hostname is a legitimate report and not an A record
			}
			v4 := ip.To4()
			if wantV6 {
				if v4 != nil {
					continue
				}
				out = append(out, &dns.AAAA{
					Hdr:  dns.RR_Header{Name: qname, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: s.ttlSeconds()},
					AAAA: ip,
				})
			} else {
				if v4 == nil {
					continue
				}
				out = append(out, &dns.A{
					Hdr: dns.RR_Header{Name: qname, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: s.ttlSeconds()},
					A:   v4,
				})
			}
			if len(out) >= s.cfg.MaxRecords {
				return out
			}
		}
	}
	return out
}

func (s *Service) ttlSeconds() uint32 { return uint32(s.cfg.TTL / time.Second) }

// ServeDNS makes the service a dns.Handler.
func (s *Service) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	w.WriteMsg(s.Answer(req))
}

// Listen serves on addr over both UDP and TCP.
//
// BOTH, ALWAYS, AND THAT IS NOT BELT AND BRACES. A UDP answer that does not fit
// is truncated, and every resolver's response to truncation is to retry the
// same query over TCP. A server listening only on UDP fails exactly the queries
// whose answers are largest, which for this service means the most popular
// names on the biggest fleets.
func (s *Service) Listen(addr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("dns: this server has been closed")
	}
	if len(s.servers) > 0 {
		return errors.New("dns: already listening")
	}

	started := make(chan error, 2)
	for _, net_ := range []string{"udp", "tcp"} {
		srv := &dns.Server{
			Addr:              addr,
			Net:               net_,
			Handler:           s,
			NotifyStartedFunc: func() { started <- nil },
		}
		s.servers = append(s.servers, srv)
		go func(srv *dns.Server) {
			if err := srv.ListenAndServe(); err != nil {
				started <- fmt.Errorf("dns: %s: %w", srv.Net, err)
			}
		}(srv)
	}

	var errs []error
	for range s.servers {
		if err := <-started; err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// Addrs is where the servers actually bound, which is how a caller that asked
// for :0 finds out. UDP first, then TCP.
func (s *Service) Addrs() []net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []net.Addr
	for _, srv := range s.servers {
		if srv.PacketConn != nil {
			out = append(out, srv.PacketConn.LocalAddr())
		}
		if srv.Listener != nil {
			out = append(out, srv.Listener.Addr())
		}
	}
	return out
}

// Close stops both servers. It is safe to call twice.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	var errs []error
	for _, srv := range s.servers {
		if err := srv.Shutdown(); err != nil {
			errs = append(errs, err)
		}
	}
	s.servers = nil
	return errors.Join(errs...)
}
