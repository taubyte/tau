// Package dns answers queries, and does nothing else.
//
// It is one half of v1's seer; discovery is the other. v1's seer answered DNS AND
// accumulated where everything is, and the two have different shapes: this half
// is read-mostly and latency-bound, the other is write-heavy and
// staleness-tolerant. Splitting them lets each be sized, deployed and reasoned
// about on its own, and it is why this package has no swarm identity, no
// gossip, and one dependency — a read.
//
// # Answering is a projection of a discovery read
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
// node accepts the connection and forwards it over p2p. Nobody authors the port
// a client dials either — the kernel gives it to whichever listener asked for
// one, per proxy and per node — so an SRV lookup is the only way a client ever
// learns it, and an A record structurally cannot carry a port.
//
// SRV can — `priority weight port target` — and its weight field is where the
// load ordering survives into the answer instead of being flattened away. The
// two underscore labels are part of the question and not decoration: one name
// can have several listeners on one node, and the service and transport are all
// that tell them apart.
//
// An SRV target is a NAME and not an address (RFC 2782), so answering SRV means
// minting one name per node under Config.NodeZone and resolving it back to that
// node's addresses. Both halves are here, and the A records for the targets ride
// along in the ADDITIONAL section so a client does not pay a second round trip
// for something this server already knows.
//
// AN ADDRESS THAT IS NOT AN IP IS SKIPPED RATHER THAN GUESSED AT. discovery carries
// what nodes report and parses nothing (deliberately — see its doc). A hostname
// in that list is a legitimate thing for a node to report and a thing this
// service cannot put in an A record, so it is passed over, and a node whose
// addresses are ALL unusable is simply not part of the answer.
package dns

import (
	"context"
	"encoding/base32"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/mr-tron/base58"

	"github.com/taubyte/tau/pkg/stats"
)

// Service answers DNS queries from a discovery view.
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
	return s.AnswerContext(context.Background(), req)
}

// AnswerContext is Answer with a deadline on whatever it has to look up.
//
// THE CONTEXT IS FOR THE AUTHORED RECORDS and nothing else: routing reads a
// local view and cannot block, where finding which project owns a name is a
// lookup somebody else answers. A resolver gives up in seconds, so a query that
// outlives its client is work nobody will read.
func (s *Service) AnswerContext(ctx context.Context, req *dns.Msg) *dns.Msg {
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

	// A DNS-01 CHALLENGE, before anything an author wrote: the cloud's zone
	// is the cloud's, and only a cm node proves names in it. See
	// challenges.go.
	if s.answerChallenge(ctx, reply, name, q) {
		return reply
	}

	// AN AUTHORED RECORD WINS, before anything is routed. It is the more
	// specific statement — somebody wrote it down, about that exact name, on
	// purpose — and it is the only way this server answers a type it cannot
	// route at all.
	if records := s.authored(ctx, q.Name, q.Qtype); len(records) > 0 {
		reply.Answer = records
		return reply
	}

	if q.Qtype == dns.TypeCAA {
		if records := s.caa(name, q.Name); records != nil {
			reply.Answer = records
			return reply
		}
	}

	// THEN THE ZONE'S RESOLVER, for the names its file does not carry. Second
	// because the file is the more specific statement and first would put a
	// component start on every query the lines already answer.
	//
	// `absent` is NOT answered here. It is a positive statement that the name
	// does not exist IN THAT ZONE, and a project may serve a name from a gateway
	// while declaring the zone above it -- so the query carries on, and the
	// verdict is spent at the bottom where nothing else answered either.
	absent := false
	if answer := s.resolved(ctx, q.Name, q.Qtype); answer != nil {
		switch {
		case !answer.Exists:
			absent = true
		case len(answer.Records) == 0:
			// The name is there and holds nothing of this type: NOERROR with no
			// records, which is what stops a client looking elsewhere.
			return reply
		default:
			if records := s.resolvedRecords(s.apexOf(ctx, q.Name), q.Name, answer.Records); len(records) > 0 {
				reply.Answer = records
				return reply
			}
			// Answered lines that parse to nothing at this name is the same
			// shape as `empty`: it said something, and none of it was about this.
			return reply
		}
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

	// A SERVICE OF THE CLOUD, by name. Before the generated names because it
	// sits where a project label does, and a project label is never a service's
	// name. See services.go.
	if s.answerService(reply, name, q) {
		return reply
	}

	// A NAME UNDER THE CLOUD'S OWN ZONE is a generated one: published or not,
	// and answered by the gateways. See generated.go.
	if s.answerGenerated(ctx, reply, name, q) {
		return reply
	}

	nodes := s.opts.router.Route(name, s.cfg.MaxAge)
	if len(nodes) == 0 {
		// A NAME A ZONE DECLARES BUT NOT AT THIS TYPE EXISTS. Asked for an A
		// where only an MX was written, the answer is NOERROR with no records —
		// SERVFAIL would tell a resolver this server is broken and send it to
		// ask somebody else.
		if s.answersFor(ctx, q.Name) {
			return reply
		}

		// A ZONE'S OWN RESOLVER SAID NO, and nothing else served it either. That
		// is the one thing this server hears that is a statement about
		// EXISTENCE rather than about what happens to be running, so it is the
		// one case that earns NXDOMAIN -- which a resolver caches as an absence.
		if absent {
			reply.Rcode = dns.RcodeNameError
			return reply
		}

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
// name, targeting that node's own name and the port that node's listener for
// this service actually got.
//
// THE UNDERSCORE LABELS ARE THE QUESTION, not decoration to be stripped. A node
// can serve one name at several ports — a postgres proxy and an https gateway
// on `db.example.com` are two listeners — and only the service and transport
// tell them apart, because a raw stream carries nothing else that could.
func (s *Service) answerSRV(reply *dns.Msg, name, qname string) *dns.Msg {
	if s.cfg.NodeZone == "" {
		// No zone means no name can be minted that resolves, and a target
		// pointing into a zone nobody delegated is an answer to nowhere.
		return reply
	}

	// _postgres._tcp.db.example.com -> postgres, tcp, db.example.com.
	protocol, transport, svc, ok := splitSRV(name)
	if !ok {
		reply.Rcode = dns.RcodeFormatError
		return reply
	}

	nodes := s.opts.router.Route(svc, s.cfg.MaxAge)
	if len(nodes) == 0 {
		reply.Rcode = dns.RcodeServerFailure
		return reply
	}

	ttl := s.ttlSeconds()
	for _, n := range nodes {
		label := nodeLabel(n.Peer)
		if label == "" || len(label) > maxLabel {
			// A name that cannot be spelled is not a name to hand out: a
			// truncated label would resolve to somebody else, or to nothing.
			continue
		}
		port := portOf(n, svc, protocol, transport)
		if port == 0 {
			// This node serves the name but has no listener for THIS service:
			// a proxy that has not come up, or a name whose postgres lives on
			// another node entirely. Routing to it would be routing to a
			// refusal.
			continue
		}
		target := dns.Fqdn(label + "." + s.cfg.NodeZone)
		reply.Answer = append(reply.Answer, &dns.SRV{
			Hdr:      dns.RR_Header{Name: qname, Rrtype: dns.TypeSRV, Class: dns.ClassINET, Ttl: ttl},
			Priority: 0,
			Weight:   weightOf(n.Load),
			Port:     port,
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
// subdomain gateways do with CIDs and for exactly this reason: base32 survives
// any case folding.
//
// IT ENCODES THE ID'S BYTES AND NOT ITS SPELLING, which is the whole of the
// difference between working and not. Base32 costs 8 bits per 5, so re-encoding
// the 46-character base58 TEXT produced a 74-character label — past DNS's limit
// of 63, on every real peer id there is. The guard below then dropped every SRV
// target, and the tests did not notice because their peers were `p` and
// `peer00`. Encoding the decoded multihash instead — 34 to 36 bytes — gives 55
// to 58 characters, which fits with room.
//
// The guard stays anyway. It is now unreachable for anything libp2p mints, and
// that is the point: it is there for the id nobody has thought of, and a name
// that cannot be spelled must produce NO target rather than a truncated one
// that resolves to somebody else.
const maxLabel = 63

var nodeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func nodeLabel(peer string) string {
	raw, err := base58.Decode(peer)
	if err != nil {
		// Not an id this server can spell. Answering with something derived
		// from it anyway would mint a name that decodes to a different peer.
		return ""
	}
	return strings.ToLower(nodeEncoding.EncodeToString(raw))
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
	return base58.Encode(raw), true
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

// records turns nodes into A or AAAA records, in the order discovery gave them —
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
	ctx, cancel := context.WithTimeout(context.Background(), lookupBudget)
	defer cancel()

	w.WriteMsg(s.AnswerContext(ctx, req))
}

// lookupBudget bounds what one query may spend finding which project owns a
// name. A resolver gives up in a few seconds; work past that is work nobody
// will read.
const lookupBudget = 2 * time.Second

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

// splitSRV takes the two underscore labels off an SRV name and hands back all
// three parts. A name with fewer than two is not an SRV question.
func splitSRV(name string) (protocol, transport, rest string, ok bool) {
	protocol, after, found := strings.Cut(name, ".")
	if !found || !strings.HasPrefix(protocol, "_") {
		return "", "", "", false
	}
	transport, rest, found = strings.Cut(after, ".")
	if !found || !strings.HasPrefix(transport, "_") || rest == "" {
		return "", "", "", false
	}
	return protocol[1:], transport[1:], rest, true
}

// portOf is where this node answers that exact question, or zero.
//
// EVERY PART IS MATCHED, host included: a node reports one socket per proxy it
// runs, across every project it holds, so matching on the service alone would
// hand out another project's postgres port under this name.
func portOf(n stats.Node, host, protocol, transport string) uint16 {
	for _, sock := range n.Sockets {
		if strings.EqualFold(sock.Host, host) &&
			strings.EqualFold(sock.Protocol, protocol) &&
			strings.EqualFold(sock.Transport, transport) {
			return sock.Port
		}
	}
	return 0
}

// caa is the operator's CAA records for a name at or under one of its zones,
// or nil when the name is in none.
func (s *Service) caa(name, qname string) []dns.RR {
	var out []dns.RR
	for _, c := range s.cfg.CAA {
		if name != c.Zone && !strings.HasSuffix(name, "."+c.Zone) {
			continue
		}
		out = append(out, &dns.CAA{
			Hdr:   dns.RR_Header{Name: qname, Rrtype: dns.TypeCAA, Class: dns.ClassINET, Ttl: uint32(s.cfg.TTL / time.Second)},
			Tag:   "issue",
			Value: c.Issuer,
		})
	}
	return out
}
