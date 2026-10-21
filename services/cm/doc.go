// Package cm is the cloud manager: the home of what a cloud keeps once for
// every region and that belongs to no project, no single node and no other
// service. Certificates are its first concern; proving a project's custom
// domains is its second (domain.go).
//
// A cm node hosts the certificate store and nothing more: any node that
// needs a certificate orders it from the ACME CA itself (Client), keeps it
// here, and reads the newest one back. See certificate.go.
//
// # It runs no consensus
//
// Everything here is replicated through kvdb, under the rules in AGENTS.md:
// one key per writer, conflicts made visible and resolved on read.
package cm
