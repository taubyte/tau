// Package cm is the cloud manager of one region: what that region keeps once and
// that belongs to no project, no single node and no other service. Certificates
// are its first concern; proving a project's custom domains is its second
// (domain.go).
//
// Where the cloud names its zone, cm hosts the certificate store: any node that
// needs a certificate orders it from the ACME CA itself (Client), keeps it here,
// and reads the newest one back. See certificate.go.
//
// # It runs no consensus
//
// The certificate store and the domain proofs are replicated through kvdb, under
// the rules in AGENTS.md: one key per writer, conflicts made visible and resolved
// on read. The services that need raft open their own groups, through their
// instance.
package cm
