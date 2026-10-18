package dns

import (
	"errors"
	"fmt"
	"time"

	"github.com/taubyte/tau/pkg/stats"
)

// config.go declares what this service is built from. Every interface is
// declared here, by the consumer, and is as narrow as the use.
//
// THERE IS NO peer.Node AND NO host.Host IN THIS PACKAGE. dns does not join a
// swarm, does not gossip and has no identity of its own on the network — it
// answers queries from a view somebody else maintains. Taking a host to get at
// discovery would make this service impossible to test without one, for no gain.

// Router is the slice of discovery this service needs: one read.
//
// *services/discovery.Service satisfies it. The interface exists so that dns can be
// tested against a table of nodes, and so that the day gateway wants the same
// read there is one name for it rather than two.
type Router interface {
	Route(name string, maxAge time.Duration) []stats.Node

	// Get is one node by peer id, however old its row. SRV answers point at
	// per-node names, and something has to resolve those names back to
	// addresses — this is that lookup.
	Get(peer string) (stats.Node, bool)
}

// Config is what an operator declared, and ONLY that: every field below is a
// value somebody writes in yaml, so it decodes, marshals and round-trips.
//
// THE INJECTED DEPENDENCY IS NOT HERE. Where the answers come from is handed
// over at construction with WithRouter, because a live object cannot be
// decoded from a config file and a struct that mixes the two has no honest
// serialized form. That split was put to codex@xhigh and agreed:
// "operator configuration is data, not constructor customization".
type Config struct {
	// TTL is what this server tells resolvers about how long to cache an
	// answer. Default 30s.
	//
	// IT IS A PROMISE ABOUT HOW FAST A CHANGE PROPAGATES, and it is the reason
	// it is not larger. A node that stops serving a name drops out of discovery
	// within a couple of report intervals, but a resolver holding a 300s
	// answer keeps sending clients at it for five minutes regardless. The
	// whole point of routing from live measurements is lost at the cache.
	TTL time.Duration

	// MaxAge is how stale a discovery row may be and still be answered from.
	// Default 30s.
	//
	// dns states its own tolerance because pkg/stats deliberately has no
	// default: a DNS answer that will be cached for TTL is a different
	// question from a gateway picking a backend for one request. The two are
	// related — an answer cached for TTL, built from a row already MaxAge old,
	// can be acted on up to TTL+MaxAge after the last thing the node said.
	MaxAge time.Duration

	// NodeZone is the suffix under which each node gets a name, e.g.
	// "nodes.example.com" — a node's own name is "<peer-id>.<NodeZone>".
	//
	// SRV EXISTS BECAUSE A RECORDS CANNOT CARRY A PORT, and an SRV record's
	// target is a NAME, not an address (RFC 2782). So answering SRV means
	// minting a name per node and being able to resolve it, which is what this
	// zone is for. A peer id is alphanumeric and around fifty characters, so it
	// fits a DNS label without encoding.
	//
	// Empty disables SRV: the server answers NOERROR with no records rather
	// than inventing a zone, because a target in a zone nobody delegated is an
	// answer that resolves to nothing.
	NodeZone string

	// MaxRecords caps how many addresses go into one answer. Default 8.
	//
	// A fleet of two hundred nodes serving one popular name would otherwise
	// produce an answer that does not fit a UDP datagram, and truncation sends
	// every resolver back over TCP — a cost paid on every query to punish a
	// name for being popular.
	MaxRecords int
}

// Defaults.
const (
	DefaultTTL        = 30 * time.Second
	DefaultMaxAge     = 30 * time.Second
	DefaultMaxRecords = 8
)

var (
	ErrNoRouter = errors.New("dns: a server with no router has nothing to answer from")

	// ErrNoResolvers is a nil source of resolved answers. Same reasoning as
	// ErrNoZones: a server told to ask something must be given something to ask.
	ErrNoResolvers = errors.New("dns: a nil resolver answers no question")

	// ErrNoZones is a nil source of authored records, which is not the same as
	// no source at all — see WithZones.
	ErrNoZones = errors.New("dns: a nil source declares no records")
)

// Option injects what this service is built from. Operator values go in Config;
// live collaborators come through here.
type Option func(*options) error

type options struct {
	router    Router
	zones     Zones
	resolvers Resolvers
}

// WithRouter is where the answers come from. Required.
func WithRouter(r Router) Option {
	return func(o *options) error {
		if r == nil {
			return ErrNoRouter
		}
		o.router = r
		return nil
	}
}

func (c Config) withDefaults() (Config, error) {
	if c.TTL == 0 {
		c.TTL = DefaultTTL
	}
	if c.MaxAge == 0 {
		c.MaxAge = DefaultMaxAge
	}
	if c.MaxRecords == 0 {
		c.MaxRecords = DefaultMaxRecords
	}
	if c.TTL < 0 || c.MaxAge <= 0 || c.MaxRecords < 1 {
		return Config{}, fmt.Errorf("dns: ttl %s, max age %s and %d records: an answer needs a positive budget for all three",
			c.TTL, c.MaxAge, c.MaxRecords)
	}
	return c, nil
}
