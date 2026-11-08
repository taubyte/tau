package database

import "time"

// The replication floor is the fleet's (how many machines it can afford to lose),
// not an author's; the vocabulary's `size` bounds one database's growth and does not
// interact with it.

// How many nodes hold a kv database is the region's, read from cm (cm/settings),
// capped by the nodes discovery finds.

// DefaultSweep is how often a node reconsiders what it holds. Not a heartbeat:
// nothing waits on it, and membership settles over minutes.
const DefaultSweep = 30 * time.Second
