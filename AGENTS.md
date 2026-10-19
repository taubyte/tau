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
correctness.

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
from more than one holder. See `pkg/kvdb`.

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

## Every resource kind gets a test project

A test project per resource kind, and projects that **combine** kinds, is how the
logic gets validated and how the v2 schema gets refined. Both halves matter, and the
second is the one that is easy to skip.

`node/dream/projects/` is the **only** thing in this repo that runs a declaration all
the way through: yaml → tcc → compile → publish → resolve → grant. A test that builds
a compiled object in Go and publishes that has skipped every step where the schema has
an opinion.

### A kind is not proven until a project shows it

A passing test that assembles its own compiled object proves that object works. It says
nothing about the kind as an author declares it, because the declaration never ran. Do
not describe such a kind as working, done, or covered — describe it as having a test
that does not exercise the declared path. The distinction is the whole point of this
rule, and it is easy to lose in a status table.

### Why this is a rule and not a preference

`node/dream/queue_test.go` used to hand-build its compiled object. Converting it to a
real corpus project showed the fixture had been describing **a different deployment
than the one under test**:

- the dead-letter queue was missing `max_attempts` and `visibility_timeout` entirely —
  the compiler materialises them from `DefaultWhen("type","queue")`, so the runtime
  always had them and the fixture under-stated the queue it claimed to test;
- `implements` was empty, where the vocabulary defaults it to `handler` and
  `services/worker/worker_test.go` asserts exactly that.

Neither was visible from the Go side. Both fell out of compiling the yaml once.

### Negative projects, too

A project that must **fail** to compile is the only way a missing check surfaces. Two
validators are named in the schema's own comments as enforcing sync constraints and are
**defined nowhere** — `checkSyncOps` (`definition.go:1317`, `:1500`, `:2321`,
`services/worker/consume.go:171`) and `checkDeadLetterIsAQueue` (`definition.go:1499`).
Today `ops: [append]` on a queue compiles clean, and so does a `dead_letter` pointing at
a log. Nothing noticed, because nothing ever asked the compiler to refuse.

### Combinations are where the references live

A single-kind project tests the kind. A project combining kinds tests what sits
*between* them — Refs, namespaces, scoping. `dead_letter` is `Ref("sync")`, and the
schema comment says why the check cannot live on the Ref: *"a Ref cannot say 'a sync of
type queue'"*. That constraint can only be enforced across resources, which is exactly
the shape nobody wrote.

### Only for kinds that are actually implemented

A project for a kind whose runtime is half-built tests nothing and fails for reasons
that teach nothing. Write projects for kinds that are finished; for the rest, the
project is part of finishing them.

### Checklist

- [ ] Does this kind have a project under `node/dream/projects/`?
- [ ] Does the test DEPLOY that project, rather than publish a Go literal?
- [ ] Is there a project combining this kind with the kinds it references?
- [ ] Is there a project that must be REFUSED, covering each constraint the schema
      claims to enforce?
- [ ] Are the ids real CIDv0 — `base58btc(0x12 0x20 || sha256(<bare resource name>))`,
      no trailing newline? A fabricated one fails at runtime with
      `multihash length inconsistent`, not at compile.
- [ ] If the compiled object differs from what you expected, is the COMPILER wrong, or
      were you? It is usually you.
