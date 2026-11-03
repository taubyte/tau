package discovery

type descriptor struct {
	Name string
}

// Descriptor is the role a node runs this as. It serves no RPC: what it knows
// arrives over the bus.
var Descriptor = descriptor{Name: "discovery"}
