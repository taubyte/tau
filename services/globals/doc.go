// Package globals holds what a cloud keeps once for every region: state that
// is not a project's and not one node's, read by any node that serves.
//
// Its first responsibility is the cloud's TLS certificates — issued from an
// ACME CA once for every node that presents them, renewed before they lapse,
// and kept where any node serving TLS reads them. See certificate.go.
//
// # Why not an existing service
//
// The dns service owns the zone and the gateway owns port 80, and each is the
// answering half of one challenge type; neither is where an order should
// live. auth is identity only. primitives is a project's coordination, and its
// consensus is one region's. What remains is cloud-wide and region-spanning,
// which is what this service is for.
//
// # It runs no consensus
//
// Everything here is replicated through kvdb, under the rules in AGENTS.md:
// one key per writer, conflicts made visible and resolved on read. Where that
// cannot give a guarantee, the file that relies on it says what it gives
// instead.
package globals
