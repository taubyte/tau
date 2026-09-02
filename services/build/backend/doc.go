// Package backend is build's private half: the queue, the job records and the
// run. It serves both of build.proto's service blocks — QueueService and
// JobService — to other tau nodes over p2p/rpc, and it is reachable no other
// way. The public half, which takes webhooks off the internet and calls in
// here, is services/build/frontend.
//
// It is v1's patrick (the job store and the queue) and v1's monkey (the thing
// that runs a job) as ONE process, which is what deletes most of what those two
// spent their code on: patrick's dequeue/isAssigned/timeout RPCs existed only
// because claiming work and reporting a stale claim had to cross a service
// boundary, and monkey's cancel round trip existed only because the record and
// the run lived in different processes. Here they are two lines in one.
//
// # Four rules, and everything else follows from them
//
//  1. EVERY RPC IS LEADER-ONLY, READS INCLUDED, and a follower answers
//     CodeUnavailable. See leader.go — it is one rule rather than five, and it
//     is what removes forwarding, a leader client cache and the staleness
//     question in one go.
//
//  2. ACK IS THE COMMIT POINT. A run's terminal record is written only after
//     the queue has accepted the acknowledgement, because Ack is the only
//     compare-and-swap this service has: the loser of a leadership flap gets
//     ErrStaleDelivery and must publish nothing. v1 wrote its results first and
//     asked whether it still owned the job afterwards, if at all.
//
//  3. THE CLAIM'S OWN DEADLINE BOUNDS THE RUN. A run that outlives its claim is
//     a second claimer with the same job, on one node as much as on five —
//     the queue's own repair() would hand the item back and the very next cycle
//     of this loop would take it. So the run context's deadline is the claim
//     deadline less a margin, capped by the operator's BuildTimeout.
//
//  4. CANCEL IS A RECORD WRITE, not a message, and A JOB RECORD'S READ AND THE
//     ENTRY IT DECIDES ARE ONE SECTION. A queued job needs no special case
//     because the claimer re-reads the record before it runs — but re-reading
//     is only worth something if nothing writes between the read and the entry
//     it decided, and Cancel and Retry answer on rpc goroutines while the claim
//     loop runs on its own. raft orders their entries and cannot see that the
//     second decided on a stale read. section.go's jobLock is that section and
//     its boundary is stated there. A SECTION IS A CLOSURE (section.go, over
//     the backend/section package): withJob takes the lock, takes the barrier,
//     takes the read, and hands the body a handle whose methods are the only
//     writes there are, about the one job it was entered on. v1 polled a claim
//     record thirty times at two-second intervals, blocking the request for up
//     to a minute, and still could not cancel a job that had not been picked
//     up yet.
//
// # The property in full
//
// Rule 4 was stated with two of its three clauses for eight rounds, and the
// missing one is the one that kept being defeated. All three, spelled out:
//
//	WHEN       the write is made while the section the deciding read was taken
//	           in is STILL HELD. A read is only true until something else
//	           writes, so a write decided on a read whose section has ended is
//	           a write decided on nothing.
//	WHO        it is made BY THE BODY that took that read, or by something that
//	           body handed its handle to. Not "by a function that holds a lock",
//	           and not "while some section is running" — those are presence,
//	           and presence is what every defeated guard was checking.
//	ABOUT WHAT every row the write names is A ROW OF THE JOB THAT READ WAS
//	           TAKEN ON. Not the job named in the record the body happens to
//	           have in hand, and not a job whose id the body composed a key
//	           from: the section's own subject, which the body cannot assign.
//
// WHERE EACH CLAUSE LIVES. WHEN is the runtime's: a handle is dead the instant
// withJob returns, and Go has no lifetime on a parameter, so a handle captured,
// deferred or sent elsewhere is refused rather than stopped. WHO is the
// compiler's: every write is a method on the handle, the handle is made in one
// place, and a helper takes one or cannot write. ABOUT WHAT is the compiler's
// for the SUBJECT — a handle has no id field, because the id is inside another
// package's struct — and a review-time enumeration for the KEY GRAMMAR, which
// is the one place this package's guarantees stop being the compiler's. See
// below.
//
// # What it is built from
//
// A host to answer on, a replicated store, and one question it can ask auth.
// Nothing else, and nothing it constructs itself: New binds no port, opens no
// database and invents no libp2p identity, so a node can construct every
// service before starting any of them (service-conventions.md §2a) and this
// package's own tests stand it up on mocknet.GenPeer() and raft.NewMockCluster()
// with nothing else running.
//
// The store is ONE store — pkg/v2/queue's Store, method for method — so the
// fact that decides a claim and the fact that records it are written by one
// writer against one log. Two stores is exactly the seam v1's /jobs/ record and
// its separate /assigned/ row fell through.
//
// # What it deliberately does not do
//
// It does not run a build. Runner, Work and Sink are the seam, and the reason
// is not effort: v2 has no tns schema to publish a compiled config to and no
// storage contract to stash bytes at, and reaching for v1's clients would
// import the cbor stack v2 exists to leave. Everything around the run —
// idempotency, claiming, attempts, cancellation, the before..after anchor, the
// per-attempt log and asset rows — ships now, so the runner is a drop-in.
//
// It also does not serve log or artifact BYTES. v1's GET /logs/{cid} is an open
// IPFS gateway gated only by a token being valid, and its /download route has
// no authorization at all; when those come back they are per-job routes on the
// frontend, authorized like Get and checked against that job's own maps.
//
// # Where the tower ends
//
// Eight rounds of this package's history each added a level. The writes are
// held by a handle; the handle is held by a closure; the closure's population
// is held by scans over this package's own source; and the scans are held by
// assertions in a test file. "What guards the guard" has to stop somewhere, and
// this is where, said exactly rather than left for somebody to find.
//
// THE COMPILER'S HALF ENDS NOWHERE, because nothing has to run for it. Twelve
// expressions do not exist, and a tree that compiles is a tree in which they
// are absent:
//
//	no Enter to leave and retake         no `commit([]raft.BatchOp)`
//	no accessor handing out a holding    no `finish(…, extra …)`
//	no mutex to touch                    no method on the entry taking a key
//	no exported field on a Holding       no `Reread(closure)`
//	no minting function                  no `held.id` to assign
//	no write reachable without a *held   no `Holding.subject` to write
//
// The seven on the left were confirmed by the round that made a section a
// closure. The five on the right were written into this tree and compiled: each
// is `cannot assign`, `too many arguments`, `undefined`, or `has no field or
// method`, which is a build failure and not a test failure.
//
// THE RUNTIME'S HALF IS WATCHED FROM EIGHT ANGLES, so it has no small set
// either. Deleting the identity comparison in section.Holding.live, or making
// the gate call the write instead of asking the holding, each fails eight named
// tests — and hollowing requireRefused's own assertions on top does not save
// it, because every escape form ALSO asserts that the record did not move. Its
// exposure is the file, not a line.
//
// SO THE TOWER IS THE STATIC HALF, AND IT ENDS AT SIX LINES, all in
// population_test.go, in three tests. In
// TestEveryKeyAnEntryWritesNamesTheSectionsOwnJob:
//
//	require.Contains(t, args, e+".h.id()",
//	require.Equal(t, map[string]string{ … }, rows,
//	require.ElementsMatch(t, names(rows), names(appenders),
//
// in TestEveryWriteOfAJobRecordIsInsideASection:
//
//	require.ElementsMatch(t, []string{"entry.record"}, writers,
//
// and in TestEveryWriteIsMadeThroughTheHandleThatReadTheRecord the pair:
//
//	require.ElementsMatch(t, []string{"entry.commit", "attemptSink.write"},
//	require.False(t, f.calls[verb],
//
// The first says every key a row builds names the section's own job. The second
// says these are all the rows there are. The third says the slice they append
// to is theirs. The fourth says one method writes a job record. The fifth and
// sixth say nothing else reaches the store at all.
//
// MEASURED, one at a time, each against a control that is GO_TEST_EXIT=1:
//
//	an open row keyed s.rec.ID rather than    red; with the first line hollowed
//	  s.h.id() — begin's own old defect         to a t.Logf, GREEN
//	a second method building a project-       red; with the second line
//	  index row                                 weakened to Subset, GREEN
//	a raw raft.BatchOp appended to an entry    red; with the third line hollowed,
//	  naming another repository's ref anchor    GREEN
//	a second job record in the same entry      red; with the first three hollowed
//	                                            it is the fourth that fails, and
//	                                            with the fourth hollowed too,
//	                                            GREEN
//	a raw raft.BatchOp handed straight to      red; with the fifth weakened to
//	  Store.Batch from a new handle method       Subset AND the sixth hollowed,
//	                                            GREEN
//
// EVERYTHING TOUCHING THEM IS HELD, which is why the set is six and not sixty.
// Measured one at a time, each of these fails BY NAME rather than passing:
//
//	a second job record in one entry          the row census and the record-
//	                                          writer census, both by name
//	a ref anchor for another repository,      the row census: ends() appears
//	  appended in a method on the handle      building refLastKey
//	an entry method that takes a key          there is none; the draft that had
//	  (put/drop), called with a foreign key    one measured GREEN, which is why
//	a new write method on the handle          three tests
//	  reaching Store.Batch
//	a raw BatchOp appended to an entry        the appenders enumeration
//	a raw BatchOp handed to Store.Batch       the Batch census and the closed-
//	                                          from-the-other-side loop
//	a `stated` assembled around another       three tests, including the one
//	  job's record                            that says who may make one
//	record() not stamping the subject onto    the behavioural test and the
//	  the record it encodes                   record-writer census
//	the sink writing a ref anchor instead     four tests, two of them
//	  of an attempt row                       behavioural
//	the second read dropping its barrier      three tests, including the
//	                                          barrier budget
//	the source's Subject and its First        the reader census, by name — and
//	  naming different jobs                   this one is why that check is
//	                                          there: unguarded it does not fail,
//	                                          the suite HANGS
//	decodeJobRecord asking "has an id"        the store test, by name
//	  instead of "is this id"
//	the ack's subject check deleted           the ack test, by name
//	the holding's liveness comparison, and    eight named tests each
//	  the gate that asks it
//	parsePackage returning nothing            every census at once: an empty
//	                                          scan cannot match a non-empty
//	                                          expectation
//
// WHY IT CANNOT BE SMALLER. The six are three pairs and neither half of any
// pair can be folded into the other. Two say WHICH ROWS EXIST and two say EACH
// ROW NAMES THIS JOB: a census cannot also be the check that a row names the
// right job, because a row naming the wrong one is IN the census; and a check
// that every row names this job cannot see a row built without calling set() or
// del() at all. The last two say NOTHING ELSE REACHES THE STORE, which neither
// of the other pairs can say, because both of them start from a call this
// package makes to its own key builders. Under all six is the thing no Go test
// escapes: a test fails by calling Errorf, so somewhere there is a first call
// nothing else watches, and a check on that check is another Errorf with the
// same property one level out — the regress this package's history walked seven
// times.
//
// AND ONE OF THEM IS A REVIEW-TIME GUARD WHERE A COMPILER COULD DO IT, said out
// loud. "About what" is the compiler's for the SUBJECT and an enumeration for
// the KEY GRAMMAR, because store.go's builders take an id as a string and those
// same builders are the READERS' — job(), admit(), lastCommit() and reconcile's
// listing all call them with an id no section handed over. Putting the grammar
// behind the subject would make a foreign key a compile error and would take
// every reader in the package with it. That is the boundary, and it is a choice
// rather than an oversight.
//
// WHAT THIS IS THE SAME AS. It is the exposure every Go test suite already
// carries and does not write down. A TestMain that returns before m.Run(). A
// build tag on the file the assertions live in. population_test.go never added
// to the package at all. `go test` never run in CI. Each is a small edit that
// leaves a green run and no trace, and no test framework defends against any of
// them. These six are written here instead, in the open, with the measurement
// beside them.
//
// MEASURE THE MECHANISM'S OWN PACKAGE EITHER WAY. backend/section is exercised
// by its own tests and by this package's, and the two figures agree — 100.0%
// plain, 100.0% pinned — because its own tests drive the whole surface:
//
//	go test -count=1 -p 4 -cover ./services/build/...
//	go test -count=1 -p 4 -coverpkg=./services/build/backend/section/... ./services/build/...
package backend
