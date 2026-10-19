// Package secrets is the one store in tau that git does not write: the values a
// project's run-time variables and grants refer to.
//
// # How it works
//
// The nodes running this service generate a group key together (a Pedersen
// distributed key generation over Ristretto255; see pkg/secrets/tdh). No node
// ever holds the group's private scalar: each holds one share of it, on its own
// disk, sealed at rest. A secret is encrypted by whoever writes it, with a fresh
// data key wrapped to the group public key, before it reaches any member. What
// replicates between members is ciphertext, wrappings and the key's public half.
// Reading a secret takes partial decryptions from a threshold of members, each
// computed from one share against the stored wrapping the reader names, and the
// reader combines them itself.
//
// The threshold is a majority of the roster, capped at n-1 so the cluster can
// lose one member and still read. A changed roster, or a key older than its
// rotation age, starts a new epoch: a fresh key, every secret's data key
// re-wrapped under it, and the older wrappings retired once the new one has been
// opened for real. Rotation regenerates rather than reshares, so the old group
// secret does not live on into the new epoch.
//
// # What this defends against
//
//   - A copy of the replicated database — any member's, a backup, a replica on a
//     node that runs no secrets — reveals no plaintext. It holds ciphertexts and
//     wrappings only.
//   - The shares of fewer than threshold members, by disk theft or compromise,
//     open nothing and do not yield the group secret.
//   - A stolen data directory alone does not open that node's share when an
//     at-rest key is configured (Config.Key), because the key is not on the disk.
//   - A member dealing a malformed contribution: every contribution is checked
//     against its Feldman commitment, and a rebuilt share is checked against the
//     published verification vector before it is kept.
//   - A holder being used as a decryption oracle for arbitrary data: a partial is
//     only ever computed against a wrapping the store holds, named by secret, key
//     and writer, never against a ciphertext a caller supplies.
//   - A ciphertext or wrapping moved under another secret's name: both are bound
//     to their reference (and the wrapping to its key) as authenticated data.
//   - A share leaked after its epoch was rotated past: it opens nothing written
//     or re-wrapped since.
//
// # What it does not defend against
//
//   - A peer on the swarm. The service does not authorize callers: any peer that
//     can reach threshold members can ask each for a partial and read any stored
//     secret, and any peer can report running the service and join the next
//     roster. Membership of the swarm (its pre-shared key) is the boundary, and
//     the nodes inside it are trusted with each other's secrets.
//   - The node that reads. A worker resolving a variable, or a client opening a
//     secret, holds the plaintext in memory; a member re-wrapping during rotation
//     holds the data key.
//   - Threshold or more members compromised, or one compromised while it runs —
//     a live member can ask the others for partials like any peer.
//   - Small clusters. At two members the threshold is one, so either member
//     alone reads everything; at one member its share is the group secret.
//   - A data directory taken from a node with no at-rest key configured: the
//     fallback key is derived from the node's identity, which sits beside the
//     shares.
//   - Losing more than n minus threshold members at once. There is no escrow; the
//     secrets wrapped only under keys those members held are gone.
//   - A member that withholds its contribution or its partials: a round does not
//     complete and a read does not reach threshold. That is a denial of service,
//     never a wrong answer.
//   - Tampering with the audit trail on a compromised node; it is not chained.
package secrets
