// Package ingress is build's public side, the part of a cloud a stranger can
// reach. It takes webhooks and the job API off the internet, establishes the
// caller may cause what they ask, and hands the rest to plm's jobs
// (services/plm/jobs), on this node or another plm node over p2p. It holds
// no clone credentials, runtime or publish rights and serves no RPC.
//
// Trust runs one way: this side asks jobs and is never asked; there is no
// rpc.Listen here (boundary_test.go). Jobs sees only the calling node's identity,
// so authorization happens here: a webhook verifies under the secret of the hook
// named in the path, for that hook's own repository; job routes check the
// caller's role on the job's project (authz.go). Both checks cost this cloud
// something, so both sit behind an admission gate (ratelimit.go).
//
// Dependencies are interfaces (config.go), so every route runs on httptest with
// fakes. It binds net/http, not pkg/http (ingress.go).
package ingress
