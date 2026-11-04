// Package frontend is build's public half, the one process in this service a
// stranger can reach. It takes webhooks and the job API off the internet,
// establishes the caller may cause what they ask, and calls the backend over
// p2p. It holds no clone credentials, runtime or publish rights and serves no
// RPC.
//
// Trust runs one way: this role asks the backend and is never asked; there is no
// rpc.Listen here (boundary_test.go). The backend sees only this node's identity,
// so authorization happens here: a webhook verifies under the secret of the hook
// named in the path, for that hook's own repository; job routes check the
// caller's role on the job's project (authz.go). Both checks cost this cloud
// something, so both sit behind an admission gate (ratelimit.go).
//
// Dependencies are interfaces (config.go), so every route runs on httptest with
// fakes. It binds net/http, not pkg/http (ingress.go).
package ingress
