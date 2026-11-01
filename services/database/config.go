package database

import "time"

// The replication floor is the fleet's (how many machines it can afford to lose),
// not an author's; the vocabulary's `size` bounds one database's growth and does not
// interact with it.

// DefaultReplication is how many nodes hold a kv database when the cloud has
// published no fleet document; every node reaches it identically. Two, because one
// copy dies with its node.
const DefaultReplication = 2

// DefaultSweep is how often a node reconsiders what it holds. Not a heartbeat:
// nothing waits on it, and membership settles over minutes.
const DefaultSweep = 30 * time.Second

// The floor itself is NOT here. It is a property of the fleet, and a fleet
// property configured per node is two nodes disagreeing forever — see
// pkg/fleet and fleet.go.
