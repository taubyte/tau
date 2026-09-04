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
// AN ADDRESS THAT IS NOT AN IP IS SKIPPED RATHER THAN GUESSED AT. stats carries
// what nodes report and parses nothing (deliberately — see its doc). A hostname
// in that list is a legitimate thing for a node to report and a thing this
// service cannot put in an A record, so it is passed over, and a node whose
// addresses are ALL unusable is simply not part of the answer.
package dns

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/taubyte/tau/pkg/v2/stats"
)

// Service answers DNS queries from a stats view.
type Service struct {
	cfg Config

	mu      sync.Mutex
	servers []*dns.Server
	closed  bool
}

// New builds the service. It does not listen — Listen does.
func New(cfg Config) (*Service, error) {
	c, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	return &Service{cfg: c}, nil
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

	nodes := s.cfg.Router.Route(name, s.cfg.MaxAge)
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
