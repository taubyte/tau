// Package cm is the cloud manager: the home of what a cloud keeps once for
// every region and that belongs to no project, no single node and no other
// service. The consensus the services of an instance order their writes with is
// its first concern (consensus.go); certificates are its second; proving a
// project's custom domains is its third (domain.go).
//
// A cm node opens the raft groups the services beside it ask for, and, where the
// cloud names its zone, hosts the certificate store: any node that needs a
// certificate orders it from the ACME CA itself (Client), keeps it here, and reads
// the newest one back. See certificate.go.
//
// # Its own records run no consensus
//
// The certificate store and the domain proofs are replicated through kvdb, under
// the rules in AGENTS.md: one key per writer, conflicts made visible and resolved
// on read. cm decides who is in a group and where it lives, and is never on a
// group's write path.
package cm
