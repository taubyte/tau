package database

import "time"

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

// DefaultReplication is how many nodes hold a kv database when the cloud has
// published no fleet document.
//
// EVERY NODE REACHES IT THE SAME WAY, which is what keeps a cloud that has
// declared nothing in agreement with itself: a definitive absence read by two
// nodes is the same absence.
//
// TWO, BECAUSE ONE IS NOT REPLICATION. A single copy is lost with the node that
// holds it, and the whole reason a database service exists rather than a
// directory on a worker is that the data outlives the machine. Two survives one
// node going, which is the failure an operator actually plans for; three is the
// number a corp will set on a fleet large enough to lose two at once, and that
// is why this is a knob rather than a constant.
const DefaultReplication = 2

// DefaultSweep is how often a node reconsiders what it holds.
//
// IT IS NOT A HEARTBEAT. Nothing waits on a sweep: a database that moved is
// served by its old holder until the new one has it, and a sweep that ran every
// second would spend the fleet's bandwidth asking questions whose answers
// change on the timescale membership settles on — which is minutes. This is the
// rate at which a fleet finishes reacting to a change, not the rate at which it
// notices one.
const DefaultSweep = 30 * time.Second

// The floor itself is NOT here. It is a property of the fleet, and a fleet
// property configured per node is two nodes disagreeing forever — see
// pkg/fleet and fleet.go.
