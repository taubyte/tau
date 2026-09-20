package database

import "fmt"

// config.go is what an operator declares about a database node.
//
// ONE NUMBER, AND IT IS A FLOOR. How many nodes must hold a kv database before
// the cloud considers it kept. It is an operator's rather than an author's
// because it is a property of the FLEET — how many machines this cloud can
// afford to lose — and an author writing it per database would be spending
// somebody else's hardware.
//
// The vocabulary's `size` is the author's half of the same conversation, and
// the two do not interact: size bounds what one database may grow to, this
// bounds how many copies of it exist.

// DefaultReplication is how many nodes hold a kv database when an operator says
// nothing.
//
// TWO, BECAUSE ONE IS NOT REPLICATION. A single copy is lost with the node that
// holds it, and the whole reason a database service exists rather than a
// directory on a worker is that the data outlives the machine. Two survives one
// node going, which is the failure an operator actually plans for; three is the
// number a corp will set on a fleet large enough to lose two at once, and that
// is why this is a knob rather than a constant.
const DefaultReplication = 2

// Config is what an operator declared about this node's databases.
type Config struct {
	// Replication is the FLOOR on how many nodes hold each kv database. Zero
	// means DefaultReplication.
	//
	// IT IS A FLOOR AND NOT A TARGET. Placement puts a database on exactly this
	// many, and the maintenance loop brings a new replica online when one of
	// them goes — so the number is what the cloud restores to, not a ceiling it
	// drifts under.
	Replication int
}

// withDefaults fills what an operator left out and refuses what cannot be met.
func (c Config) withDefaults() (Config, error) {
	if c.Replication == 0 {
		c.Replication = DefaultReplication
	}
	if c.Replication < 1 {
		return Config{}, fmt.Errorf("database: replication %d: a database nobody holds is not kept", c.Replication)
	}
	return c, nil
}
