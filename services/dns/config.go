package dns

import (
	"errors"
	"fmt"
	"time"

	"github.com/taubyte/tau/pkg/v2/stats"
)

// config.go declares what this service is built from. Every interface is
// declared here, by the consumer, and is as narrow as the use.
//
// THERE IS NO peer.Node AND NO host.Host IN THIS PACKAGE. dns does not join a
// swarm, does not gossip and has no identity of its own on the network — it
// answers queries from a view somebody else maintains. Taking a host to get at
// stats would make this service impossible to test without one, for no gain.

// Router is the slice of stats this service needs: one read.
//
// *services/stats.Service satisfies it. The interface exists so that dns can be
// tested against a table of nodes, and so that the day gateway wants the same
// read there is one name for it rather than two.
type Router interface {
	Route(name string, maxAge time.Duration) []stats.Node
}

// Config is what an operator declared, plus the one thing injected.
type Config struct {
	// Router is where the answers come from. Required.
	Router Router

	// TTL is what this server tells resolvers about how long to cache an
	// answer. Default 30s.
	//
	// IT IS A PROMISE ABOUT HOW FAST A CHANGE PROPAGATES, and it is the reason
	// it is not larger. A node that stops serving a name drops out of stats
	// within a couple of report intervals, but a resolver holding a 300s
	// answer keeps sending clients at it for five minutes regardless. The
	// whole point of routing from live measurements is lost at the cache.
	TTL time.Duration

	// MaxAge is how stale a stats row may be and still be answered from.
	// Default 30s.
	//
	// dns states its own tolerance because pkg/v2/stats deliberately has no
	// default: a DNS answer that will be cached for TTL is a different
	// question from a gateway picking a backend for one request. The two are
	// related — an answer cached for TTL, built from a row already MaxAge old,
	// can be acted on up to TTL+MaxAge after the last thing the node said.
	MaxAge time.Duration

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

var ErrNoRouter = errors.New("dns: a server with no router has nothing to answer from")

func (c Config) withDefaults() (Config, error) {
	if c.Router == nil {
		return Config{}, ErrNoRouter
	}
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
