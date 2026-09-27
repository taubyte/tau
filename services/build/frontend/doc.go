// Package frontend is build's public half: the one process in this service a
// stranger can reach.
//
// It takes GitHub webhooks off the internet, establishes that the caller may
// cause the job they are asking for, and calls the backend
// (services/build/backend) over p2p to make it. It answers the
// GitHub-token-authenticated job API a human or the CLI calls. It holds no
// clone credentials, no container runtime, no build images and no publish
// rights, and it serves no RPC of its own — service-topology.md §2's capability
// list, and build.proto's header at length.
//
// # Trust runs in exactly one direction
//
// This role may ASK the backend and may never be asked. There is no
// rpc.Listen in this package and no host.Host on its Service, and
// services/build/boundary_test.go reads the source to hold both. The reason is
// build.proto's: a p2p listener here would have every node in the swarm
// dialling, and believing, a process whose input is an unauthenticated POST
// body — and the peer identity on the far side of that stream would say "a tau
// node" while meaning "whatever reached the webhook handler".
//
// # What this role establishes before the backend sees anything
//
// The backend's own authorization cannot help here: the identity on a p2p
// stream is the calling NODE's, and this role is one node relaying many
// humans (service-conventions.md §5b, and build.proto says so where it explains
// why JobService carries no caller field). So the question "may this caller
// cause this job" is answered HERE, on the resource, or it is not answered at
// all.
//
//   - On the webhook: the body verifies under the secret of the hook named in
//     the PATH, and the repository named in the BODY is the repository that hook
//     is registered for. v1 checks the first and not the second, which is how a
//     signature made under any registered hook comes to authorize a job for any
//     registered repository.
//   - On the four job routes: the caller holds WRITE access, at the provider, to
//     a repository the job or project belongs to. v1 checks only that the bearer
//     holds a token GitHub still answers for, and no handler on any of those
//     routes ever reads the resulting client — so any GitHub account can read,
//     cancel or retry any project's jobs.
//
// # What it costs before it establishes anything
//
// Both of those checks COST THIS CLOUD SOMETHING before the caller has proven
// anything, so both are behind an admission gate: one global limit per public
// surface, which cannot be keyed and is therefore the bound that holds, and one
// per-key limit for fairness (ratelimit.go). The webhook's proof is a lookup at
// auth. The job routes' proof is a read of the job itself — leader-only,
// through a raft barrier, which hashicorp/raft implements as a log entry
// replicated to quorum — so an un-admitted job route hands a stranger holding
// nothing but a well-shaped Authorization header one replicated entry per HTTP
// request. v1 has no version of this on either surface.
//
// # Its dependencies are five interfaces, and they are why it is testable
//
// Hooks, Projects, Backend, Identity and Deliveries (config.go) are declared
// here and satisfied elsewhere: services/build/clients holds the p2p adapters
// for the first three, and forgeauth answers the last two through pkg/forge,
// by host — so this package names no forge. Nothing in
// this package constructs a client, so every route runs against fakes on
// httptest with no libp2p host, no listener and no port — which is what makes
// service-conventions.md §6's bar reachable without a universe underneath.
//
// # It binds net/http, not pkg/http
//
// A deliberate, stated break with service-conventions.md §5a. See ingress.go:
// pkg/http resolves a route variable from the query string before the path, and
// installs no body cap. Both would be reintroduced by inheriting it, and both
// are defects this package exists to remove.
package frontend
