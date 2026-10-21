// Package globals holds what a cloud keeps once for every region: state that
// is not a project's and not one node's, read by any node that serves.
//
// Its first responsibility is the cloud's TLS certificates. A globals node
// hosts their store and nothing more: any node that needs a certificate
// orders it from the ACME CA itself (Client), keeps it here, and reads the
// newest one back. See certificate.go.
//
// # It runs no consensus
//
// Everything here is replicated through kvdb, under the rules in AGENTS.md:
// one key per writer, conflicts made visible and resolved on read.
package globals
