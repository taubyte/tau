// Package clients is the one place either of build's roles dials another v2
// service, and the one place either dials the other.
//
// It exists because p2p/rpc deliberately stops at the pipe. It dials a peer
// you have already named — `NewClient(h, pid, protocol)` — and p2p/discovery
// answers which peers serve a protocol, but nothing joins the two, retries the
// next peer when one refuses, or holds a connection open between calls.
// p2p/discovery's own doc says so and says why a pool does not belong there
// ("a pool here would be keyed by peer within one protocol… nor does it belong
// in a service: eight of them would hold eight copies of the same map").
// Until p2p/rpc grows one, build writes it once, here, bounded — rather than
// twice, once per role.
//
// # The fan-out is a loop, and it is visible
//
// Pool.Do walks peers one at a time. That is service-conventions.md §3c's rule
// with teeth: "there is no Threshold, no implicit N-way send, and no method
// whose fan-out you have to read the transport to discover". v1's
// patrickClient.Peers(allPeers...).Dequeue() reads as one call and runs N
// DESTRUCTIVE handlers — each popping a job and writing an /assigned/ row —
// then keeps one response and strands the other N-1. The shape of that API is
// what made it invisible, so this one does not have that shape.
//
// # It imports neither role
//
// The adapters below return their own concrete types rather than the interface
// each consumer declared. Returning a role's interface would make every role
// that dials auth depend on the other role's package, which is the dependency
// services/build/boundary_test.go exists to keep out. The interface
// satisfaction is asserted in this package's TEST files, where importing a
// role costs nothing.
//
// It does not import services/auth either. Dialling auth needs auth's
// SCHEMA — the generated connect client — never auth's service package, which
// would drag its store and its whole dependency set into a process that only
// wanted to ask a question.
package clients
