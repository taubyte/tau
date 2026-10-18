// Package cspbft is Byzantine agreement on a sequence of opaque proposals, as a
// state machine and nothing else: Step, Timeout, Checkpointed, Installed and
// Restore go in, Actions come out. There is no goroutine, no socket, no disk
// and no clock read anywhere in this package.
//
// # What it is for
//
// Agreement among replicas that do NOT trust one another. Inside one cloud the
// nodes are trusted, and pkg/raft and pkg/kvdb are what they agree with: a
// crash is the only failure those tolerate, and it is the only one a node of
// the same operator is assumed to have. This package is for the boundary where
// that assumption stops holding — replicas run by parties that may lie,
// equivocate or collude — which is why its fault model is f Byzantine replicas
// out of 3f+1 and why every vote is a signature a third party can check.
//
// That shape is the whole design. A consensus bug is an interleaving — an
// equivocating primary reaching a backup that is partitioned from the rest,
// two views racing, a crash between signing a vote and sending it — and an
// interleaving is only testable if it is a value. Here "seed 41, n=7, primary
// equivocates on sequence 12, replica d partitioned for 3 views" is a table
// entry, not a network fixture. pkg/membership says the same thing about
// itself for the same reason.
//
// # What is classical PBFT here, verbatim
//
// Castro and Liskov, OSDI '99. Nothing in the agreement mechanism is invented:
//
//   - Three phases. PRE-PREPARE assigns a sequence number, PREPARE agrees the
//     assignment within a view, COMMIT carries it across views.
//   - prepared(v,n,d) is one PRE-PREPARE plus Quorum(n)-1 matching PREPAREs
//     from distinct other replicas — 2f of them when n is 3f+1.
//   - committed-local(v,n,d) is prepared plus Quorum(n) matching COMMITs from
//     distinct replicas, this one included.
//   - Water marks. A replica accepts a sequence number only in (h, h+L], with
//     h the last stable checkpoint and L = 2K, so a faulty primary cannot
//     exhaust the sequence space.
//   - Checkpoints every K sequence numbers, stable at Quorum(n) matching
//     CHECKPOINTs, and that is what moves h and truncates the log.
//   - VIEW-CHANGE carries the checkpoint proof C and the prepared certificates
//     P; NEW-VIEW carries Quorum(n) VIEW-CHANGEs as V and the recomputed
//     PRE-PREPAREs as O, gaps filled with null requests.
//   - A replica that hears f+1 VIEW-CHANGEs for views above its own moves to
//     the smallest of them, so a lagging replica cannot be left behind by a
//     quorum that has already moved.
//
// Two departures from the paper, both deliberate and both named:
//
//	DIGITAL SIGNATURES, NOT MAC AUTHENTICATORS. The whole point of the scoring
//	layer below is that misbehaviour can be punished, and the only sound input
//	to a punishment is a proof a third party can check. Two conflicting
//	messages signed by one replica are that proof; two conflicting MACs prove
//	nothing to anyone but their holder. It costs roughly 80µs per verify
//	instead of 0.24µs, and it buys Equivocation.
//
//	THE OPTIMIZATIONS OF SECTION 5 ARE NOT HERE. No tentative execution, no
//	read-only path, no digest replies, no request batching in the core. A
//	Proposal is opaque bytes, so a caller that wants batches of a hundred puts
//	a hundred requests in one.
//
// # What the scoring layer is, and what it is not
//
// The named reference for this package is "CS-PBFT: a comprehensive
// scoring-based Practical Byzantine Fault Tolerance consensus algorithm". IT
// IS PAYWALLED AND WAS NOT READ. Only its abstract is recoverable. So nothing
// here claims to implement that paper. What is here is classical PBFT with the
// one seam the title names — leader selection by score — made explicit,
// pluggable and bounded, and with the two claims the abstract makes that touch
// quorum-bearing steps left out entirely: its Commit/Reply communication
// optimization, and its "slave"/"alternate" replica roles. Both are unsafe to
// guess at. A communication optimization on COMMIT is an optimization of the
// exact step that anchors cross-view safety, and a role system may change who
// counts in a quorum, which is n and f. If the paper becomes readable, both
// are additive and neither changes the shape below.
//
// The seam is a Scorer, and its contract is seven clauses:
//
//  1. IT REORDERS, IT NEVER REPLACES. primary(v) is perm[v mod n] where perm
//     is a permutation of ALL n replicas. Every replica still takes its turn,
//     so the paper's liveness argument — a faulty primary holds the view for
//     one view and then it is someone else's turn — survives verbatim. A
//     Scorer can never "elect the top scorer": a replica that behaves well for
//     a thousand views to top a ranking and then stops would hold primacy
//     forever, because the score that would demote it is computed from a log
//     that has stopped advancing.
//  2. EPOCH BINDING. perm changes only at a stable checkpoint, the one state
//     Quorum(n) replicas have provably certified. Between checkpoints it is
//     frozen, so two correct replicas in the same view always agree on who the
//     primary is.
//  3. DISAGREEMENT IS DETECTABLE, NOT SILENT. A NEW-VIEW carries the epoch and
//     H(perm). A backup that computed a different permutation refuses it with
//     ErrRotationMismatch instead of sitting in a view whose primary it does
//     not believe in.
//  4. AGREED INPUTS ONLY. This is the rule the type cannot enforce and the doc
//     therefore has to state: a Scorer may read the caller's application state
//     AS OF Snapshot.StateDigest, because that is the only datum all correct
//     replicas provably hold identically. It may never read a round-trip time,
//     a timeout count or "I have not heard from j". A score from local
//     observation makes correct replicas disagree about primary(v), which is a
//     liveness failure that looks exactly like a network fault.
//  5. DETERMINISM, INTEGER ARITHMETIC, AND AN EXPLICIT TIE-BREAK ON ReplicaID.
//     Floating point is banned: a last-bit difference across heterogeneous
//     replicas silently yields two different primaries.
//  6. GARBAGE DEGRADES TO ROUND-ROBIN. A wrong length, a duplicate, an unknown
//     id, a nil return or a panic is caught, the identity permutation is used
//     and a Notice{ErrScorerPermutation} is emitted.
//  7. NO QUORUM CODE CAN SEE IT. The Scorer is held in rotation.go alone.
//     certificate.go and log.go take no scorer and have no path to one, so
//     scoring a vote is a compile error rather than a code review.
//
// THE COST OF THE LAYER, NAMED. Because a replica can be last in one epoch's
// permutation and first in the next, the fairness floor degrades from at most
// f consecutive faulty primaries to at most 2f, and that can happen at most
// once per K committed sequence numbers.
//
// The default Scorer is RoundRobin, the identity permutation, which reduces
// this package to textbook PBFT. That is the only sense in which "pluggable"
// means anything: take the layer away and a correct protocol remains.
//
// # Punishment closes through the caller
//
// This package detects equivocation and hands out Evidence carrying a proof
// anyone can verify. It does NOT feed that proof into the rotation, because
// whether replica i has SEEN a proof is local, and a rotation driven by
// locally-held proofs diverges. The loop a caller can close for free: submit
// the proof as an ordinary request. It commits, every replica's application
// state now holds it, and their Scorer reads it from StateDigest — agreed by
// construction, with no reconfiguration protocol and no change to n or f.
//
// # Durability is a contract, not a hope
//
// A replica that prepares (v,n,d), crashes, and comes back having forgotten it
// can prepare (v,n,d') and break safety. So this package does not persist
// anything and instead NAMES what must be persisted, in order:
//
//	A Persist action MUST be made durable before any action that follows it in
//	the same slice is performed.
//
// Step places the Persist that authorises a vote before the Broadcast of that
// vote, every time, and TestEveryBroadcastOfAVoteIsPrecededByThePersistThatAuthorisesIt
// walks every slice the simulator produces and holds it. On restart the caller
// replays the records through Restore, before the first Step.
//
// # What the caller owes this package
//
// Three obligations, all of them load-bearing, none of them enforceable from
// inside a state machine:
//
//  1. PERSIST BEFORE SENDING. A Persist action must be durable before any
//     action after it in the same slice is performed.
//  2. BUFFER A FUTURE VIEW. A Step that returns ErrFutureView must be retried
//     once this replica enters that view. A new primary broadcasts its
//     NEW-VIEW and then assigns in the new view immediately; a recipient that
//     drops the assignment because it arrived first will never be sent it
//     again.
//  3. RETRANSMIT FROM THE CLIENT. A client that has not seen enough replies
//     re-broadcasts its request. This is Castro-Liskov's rule and it is what
//     covers the two places a replica legitimately forgets it was waiting for
//     something: a request dropped by a faulty primary, and a request a state
//     transfer made unknowable (see Installed).
//
// WHAT A REPLICA REMEMBERS ABOUT WHAT IT EXECUTED ENDS AT THE STABLE
// CHECKPOINT, and it ends there because that is the one point every replica
// agrees on. A replica that took the state at a checkpoint by transfer never
// executed anything below it, so a memory reaching further back would be held
// by the replicas that were present and not by the one that was not — and a
// retransmission is then dropped by a primary that remembers and held
// outstanding by a backup that does not, which is a request that can never be
// ordered and a backup that forces a view change every timeout over it. Cut at
// the checkpoint, every replica forgets the same digests at the same moment. A
// retransmission arriving after it costs one sequence number everywhere alike,
// and the application's reply table suppresses the second execution — which it
// already has to, because a view change can order one request twice.
//
// # Time
//
// No message in this package carries a wall clock and no code in it reads one.
// PBFT's two timers are local and logical, so the core emits SetTimer and
// CancelTimer with a duration and is told, by Timeout, that one elapsed.
// Doubling and the reset-on-progress rule are arithmetic on a time.Duration.
// A throttled test machine therefore cannot make these tests lie.
//
// The one field that looks like a clock and is not is Message.Serial, which
// the paper calls a timestamp. It is a per-client monotonic counter, carried
// through to Execute so the application can make execution exactly-once, never
// compared across clients and never converted to a time.
//
// # The quorum card
//
//	f = Faults(n) = (n-1)/3            the most faulty replicas tolerated
//	q = Quorum(n) = ceil((n+f+1)/2)    any two quorums share a correct replica
//	prepared      1 PRE-PREPARE + q-1 PREPAREs from distinct others
//	committed     prepared + q COMMITs from distinct replicas, self included
//	stable        q matching CHECKPOINTs
//	hearsay       f+1 VIEW-CHANGEs above my view FROM OTHERS means I am behind
//	view budget   2f+2 future views remembered PER SENDER, smallest first
//
// The mnemonic: f+1 means AT LEAST ONE OF THEM IS CORRECT, so the claim is
// true of somebody. q means ANY TWO SUCH SETS SHARE A CORRECT REPLICA, so two
// decisions cannot contradict.
//
// Supported n is [4, 16]; Advice says so and says why. View change is O(n^3)
// bits, and past roughly sixteen replicas that is the protocol's cost rather
// than a tuning problem — it is HotStuff's linear view change that fixes it,
// which is a different protocol.
//
// # What is deliberately not here
//
// AN ABORT FOR A VIEW CHANGE. A replica that has sent a VIEW-CHANGE for v'
// stops voting in every view below it and has no way back, and that is load
// bearing rather than an omission. The promise is what makes the next
// primary's O set complete: a certificate prepared after the VIEW-CHANGE went
// out appears in no V that NEW-VIEW is built from, and two quorums are only
// guaranteed to share ONE correct replica — so a replica that returned to the
// old view and prepared there could be exactly that replica, and the next view
// would re-propose a digest a quorum had already committed.
//
// The cost is named rather than hidden: a replica whose timer fires while the
// rest of the cluster is healthy cannot vote again until some view change
// actually completes, and until then it is kept current by state transfer and
// counts toward n without counting toward a quorum. Two things keep that
// survivable. The reasons the timer fires when nothing is wrong are gone — a
// request the primary will never assign because the two disagree about what
// was already executed, a payload refused while an assignment waits for it,
// and a Timeout that falls silent on a view already asked for — and the
// replica keeps asking, so its VIEW-CHANGE is already in the next primary's
// hand the moment the cluster does move.
// TestAReplicaThatChangedViewWhileAloneTakesPartAgainAtTheNextViewChange is
// that recovery, run rather than argued.
//
// WHAT WOULD REMOVE THE COST is splitting the view change in two: an intent
// that says "my timer fired" without giving up the current view, and a
// VIEW-CHANGE sent only once f+1 replicas have said it, so the P set is built
// at the moment the promise is made. That is an eighth message kind and a
// change to the protocol rather than a fix to this one, which is why it is
// written here instead of implemented.
//
// RECONFIGURATION. (n, f) is fixed for the life of a Replica. Eviction in
// particular stays out on principle: at f+1 accusations, f faulty replicas
// plus one honest misfire evict a correct replica and repeat until n < 3f+1;
// at q, correct replicas would have to agree on evidence that is local timing,
// and asynchrony is indistinguishable from failure. Demotion to the tail of
// perm costs an offender all its scheduling advantage and changes neither n
// nor f.
//
// WEIGHTED VOTING. Uniform weights, full stop. The fault model bounds the
// COUNT of faulty replicas, not their weight, and weight earned by behaving
// well is exactly what a patient adversary earns for free.
//
// A WIRE FORMAT. Message is a plain struct; the caller encodes it however it
// likes. The signature is never over those bytes, because protobuf and cbor
// both admit several encodings of one value, so "verify the bytes I received"
// and "verify the bytes I would re-encode" can disagree. Signing is over
// Preimage(m), always.
//
// AN APPLICATION INTERFACE. There is no Reply action and no upcall. Execute
// carries everything a reply needs and the caller signs and sends it;
// TakeCheckpoint and FetchState are answered by Checkpointed and Installed.
// Two actions and two inputs instead of an interface with a lifetime.
//
// A DRIVER. The loop is about forty lines the caller writes against its own
// concurrency model. Example is that loop, compiled and run.
package cspbft
