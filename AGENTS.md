# AGENTS.md

Working notes for anyone — human or agent — changing this codebase. Rules here exist
because getting them wrong produced a real bug, not because they sound tidy.

## Designing around kvdb

`core/kvdb` is a **CRDT key-value store** (go-ds-crdt), not a database. It replicates
between nodes and merges concurrent writes with **last-write-wins per key**. There are no
transactions, no compare-and-swap, and no cross-key atomicity. Design for that or lose
data.

### 1. One key per entry. Never a contended key.

The single most important rule. If two nodes can write the same key with different
content, one write is silently discarded.

```
BAD   /lookup/org/{provider}/{owner}          → account_id
GOOD  /lookup/org/{provider}/{owner}/{account_id} → linked-at
```

In the bad layout two nodes claiming the same namespace both write one key and LWW picks a
winner — the loser vanishes with no error. In the good layout they write **different**
keys, so nothing is lost.

Same rule kills read-modify-write on a collection. Never store a CBOR slice or map that
callers append to: reader A and reader B both read `[x]`, write `[x,y]` and `[x,z]`, and
one entry disappears. Split the collection into one key per element.

`services/accounts/paths.go` states this convention and the layouts follow it — lookup
indexes, passkey sub-collections, all one key per entry.

### 2. Put the discriminator in the key path.

If an entry belongs to a `(provider, owner, account)` tuple, all three go in the path. Any
part you leave out of the key is a part two writers can collide on.

Corollary: keys are also your index. `List(ctx, prefix)` is the only query mechanism, so
lay paths out so the prefix scans you need are cheap and the ones you don't need are
impossible.

### 3. Make conflict visible, then resolve it deterministically.

Rule 1 means a genuine conflict — two accounts legitimately claiming one namespace — shows
up as two entries rather than one silently winning. That is the point. Do not try to
prevent it with a lock you do not have; resolve it on read:

- scan the prefix
- order by a stable value carried **in the entry** (a timestamp), with a second key
  (the id) breaking ties for a total order
- take the first

Every node then computes the same answer from the same replicated state, with no
coordination and no dependence on write ordering or clock skew between nodes mattering to
correctness. `services/accounts/account.go`'s `lookupIDBySlug` does exactly this.

### 4. Read-then-write guards are UX, not correctness.

Checking "is this already claimed?" before writing is worth doing — it gives a clear error
in the overwhelmingly common uncontended case. It guarantees nothing under concurrency,
because another node can write between your read and your write. Never let correctness
depend on one. Rule 3 is what makes the outcome safe.

### 5. Deletes are writes too.

A delete is LWW against a concurrent write to the same key. A delete racing a re-create can
lose. If ordering matters, carry it in the value and resolve on read.

### 6. Only subscribed instances replicate.

A kvdb replicates to instances that are **open and subscribed**. A write acknowledged by a
single node can die with that node if no other holder had the database open. Anything doing
load/unload must keep claimants co-loaded during active writes and barrier on acknowledgement
from more than one holder. See `pkg/kvdb` and the hoarder replication path.

### 7. Byte order is your only sort order.

`List` returns keys in byte order. To scan chronologically, zero-pad the numeric segment to
fixed width so lexicographic order matches numeric order — `pkg/raft/storage.go:40` and
`pkg/raft/queue.go:60` pad indices to 20 digits for this reason. Unpadded numbers sort
`1, 10, 2`.

### 8. Key naming: path segments, not compound words.

Use `/lookup/account/slug/{slug}`, not `/lookup/account_slug/{slug}`. Segments are what the
prefix scanner understands; an underscore is invisible to it and forecloses scanning the
intermediate level later.

### 9. Normalise before keying.

Anything case-insensitive in the real world (email, provider namespaces) must be
canonicalised before it becomes part of a key, or `Acme` and `acme` become two entries for
one thing and every uniqueness property built on that key quietly fails. The store
lowercases emails in several places; do the same for any new identifier.

### Checklist

- [ ] Can two nodes write this exact key with different content? If yes, redesign.
- [ ] Am I appending to a stored collection? If yes, split it into keys.
- [ ] Is every part of the entry's identity in the key path?
- [ ] If a conflict happens anyway, does every node resolve it the same way?
- [ ] Does correctness rest on a read-then-write guard? It must not.
- [ ] Are numeric key segments zero-padded to fixed width?
- [ ] Are case-insensitive identifiers normalised before keying?
