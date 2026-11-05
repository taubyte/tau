// Package clients is the one place plm or the builder dials another v2
// service or the other role. p2p/rpc dials a named peer and p2p/discovery names
// peers; this joins them with a bounded pool that walks to the next peer on
// refusal.
//
// Pool.Do walks peers one at a time, never an implicit N-way send. Adapters return
// their own concrete types so no role depends on another (boundary_test.go);
// interface satisfaction is asserted in tests. Auth is dialled through its
// generated client, never services/auth.
package clients
