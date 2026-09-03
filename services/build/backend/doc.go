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
// Nine rounds of this package's history each added a level. The writes are held
// by a handle; the handle is held by a closure; the closure's population is held
// by scans over this package's own source; the scans are held by assertions in a
// test file; and the scans' INPUT is held by a list of file names. "What guards
// the guard" has to stop somewhere, and this is where, said exactly rather than
// left for somebody to find.
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
//	no raw op to append to an entry      no `Holding.subject` to write
//
// EACH IS A BUILD FAILURE AND EACH HAS BEEN COMPILED: `cannot use raft.BatchOp{…}
// as row.Op value`, `cannot assign`, `too many arguments`, `undefined`, `has no
// field or method`. The left column is the section's shape and the right is the
// entry's.
//
// THE LAST ONE IS THE NINTH ROUND'S, and what it replaced in this table is worth
// more than itself. The line here used to read "no write reachable without a
// *held", WHICH IS NOT THE COMPILER'S AT ALL: s.store.Batch is an expression any
// function in this package can write, and what refuses it is an enumeration
// three paragraphs down. A table of things the compiler guarantees had a thing
// it does not guarantee in it, and the prose under it said "the seven on the
// left" and "the five on the right" of six and six. Both are corrected above.
//
// THE RUNTIME'S HALF IS WATCHED FROM EIGHT ANGLES, so it has no small set
// either. Deleting the identity comparison in section.Holding.live, or making
// the gate call the write instead of asking the holding, each fails eight named
// tests — and hollowing requireRefused's own assertions on top does not save
// it, because every escape form ALSO asserts that the record did not move. Its
// exposure is the file, not a line. The ORDER of Do's two closing statements is
// there too, and was carried by nothing until this round: every test that
// looked at it looked after Do had returned, when both statements have run
// either way. section.TestTheIdentityDiesBeforeTheExclusionIsReleased measures
// the order itself, under the contention that makes a mutex hand the lock
// straight to the goroutine waiting on it.
//
// SO THE TOWER IS THE STATIC HALF, AND IT ENDS AT NINE LINES — up from six,
// three of which are no longer ends at all. All nine are in population_test.go:
// two in parsePackage, which every scan in the file calls, and seven in three
// tests. MEASURED ONE AT A TIME: each control below is GO_TEST_EXIT=1 on this
// tree, and GO_TEST_EXIT=0 with that ONE line hollowed and nothing else
// changed.
//
//	in parsePackage, the scan's own input:
//	 1 the files parsed are the files listed  the filter told to skip errors.go,
//	                                           with a row method declared there
//	 2 the files on disk are those files      a new file carrying a row method,
//	                                           and the filter told to skip it
//
//	in TestEveryKeyAnEntryWritesNamesTheSectionsOwnJob:
//	 3 every key names the subject            an open row keyed s.rec.ID, which
//	                                           is begin's own old defect
//	 4 these eight rows and no others         a second method building a
//	                                           project-index row
//	 5 set() is the only maker of a set row   the sink composing a second row
//	 6 del() is the only maker of a delete      with row.Set / row.Delete, on
//	                                           another repository's ref anchor
//	 7 what goes into an entry's rows came    a row forged past the type with
//	   from one of those two                    unsafe, appended inside anchor()
//
//	in TestEveryWriteIsMadeThroughTheHandleThatReadTheRecord:
//	 8 the store is handed row.Ops(rows)      a raw op appended beside the rows
//	   and nothing else                         in commit(), on a key spliced by
//	                                           hand so no builder is called
//
//	in TestTheSinkWritesOnlyItsOwnJobsAttemptRows:
//	 9 the sink keys its row by its own job   the sink writing one more row into
//	                                           another job's attempt prefix
//
// AND FIVE MORE LINES STAND OVER GROUND THE NINE ALREADY COVER. They are
// written down because a round that counts its own exposure at six when it is
// nine is worse than one that counts nothing: the next reader stops looking.
// Each was measured the same way — hollow it, run the control it was written
// for, and see what else fails:
//
//	the anchor's exemption: its three parts    a behavioural test catches the
//	  come off the record the section READ     anchor taken off the record being
//	                                           WRITTEN instead
//	the appenders enumeration: an entry's      line 7 catches the same forge;
//	  rows are appended by the row methods     hollow both and it is green
//	one function names a job record key        pairs with line 4 — hollow either
//	                                           and the other catches a second
//	                                           record in one entry
//	who may call Store.Batch at all            both pair with line 8: a new write
//	nothing else on the handle reaches it        method on the handle reaching the
//	                                             store needs all three hollowed
//	                                             before it is green
//
// HOW THE COUNT MOVED, and it is not a rearrangement. Two of the old six are
// still ends: every key names the subject, and these eight rows and no others.
// FOUR ARE NOT. One of the four lost its SUBJECT rather than its strength — the
// appenders enumeration was written against a raw raft.BatchOp appended to an
// entry, and that does not compile any more — and the other three are second
// lines over ground lines 4 and 8 cover. Six of the nine were written this
// round; the seventh, the sink keying its row by its own job, was here all along
// and had never been counted. The one that should worry a reader is line 1:
// nothing had ever pinned WHICH FILES the scans read, and one line in a filter
// took a writer out of every census at once, which is more reach than any of the
// six ever had.
//
// TWO GUARDS ARE ON THE CENSUSES' INPUT rather than on a write, and they are not
// ends. TestEveryKeyThisPackageWritesIsInSomeCensus requires every whole key
// store.go builds to be in exactly one class and every prefix in none — the
// state refLastKey was in for eight rounds was NONE, and that is what made the
// ninth round's anchor escape unrecoverable: the reach walk could not see the
// row. It is measured against its own control (a builder classified nowhere),
// and the classification it protects is load-bearing: with line 8 hollowed, a
// raw op naming refLastKey inside commit() is still caught, by the site table,
// because that key is in a census now.
//
// EVERYTHING TOUCHING THEM IS HELD, which is why the set is nine and not ninety.
// Measured one at a time, each of these fails BY NAME rather than passing:
//
//	a second job record in one entry          the row census and the record-
//	                                          writer census, both by name
//	a ref anchor for another repository,      the row census: the method appears
//	  appended in a method on the handle      building refLastKey
//	the anchor taken off the record being     the row census AND a behavioural
//	  written rather than the one read        test
//	an entry method that takes a key          there is none; the draft that had
//	  (put/drop), called with a foreign key   one measured GREEN, which is why
//	a new write method on the handle          three tests
//	  reaching Store.Batch
//	a raw op appended to an entry             it does not compile
//	a raw op handed to Store.Batch beside     the argument census, by name
//	  the rows
//	a `stated` assembled around another       three tests, including the one
//	  job's record                            that says who may make one
//	record() not stamping the subject onto    the behavioural test and the
//	  the record it encodes                   record-writer census
//	the sink writing a ref anchor instead     four tests, two of them
//	  of an attempt row                       behavioural
//	the retention row stamped with the        the retry test, by name — and this
//	  record's CreatedAt, or the delete       is the ninth round's own: the two
//	  reading it                              were invisible until a test moved
//	                                          the clock between a job's creation
//	                                          and its finish
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
//	Do releasing the exclusion before it      the ordering test in
//	  drops the identity                      backend/section, by name
//	parsePackage returning nothing            every census at once: an empty
//	                                          scan cannot match a non-empty
//	                                          expectation
//
// WHY IT CANNOT BE SMALLER. The nine are four regions and no line of one covers
// another. WHICH FILES ARE READ is under everything, because a scan reads what
// it is given. WHICH ROWS EXIST and EACH ROW NAMES THIS JOB cannot be folded
// together: a row naming the wrong job is IN the census, and a check that every
// row names this job cannot see a row that no census counted. WHAT MAKES A ROW
// is a third: the type says a row comes from set() or del(), and only an
// enumeration can say those two are called from where a key was composed. WHAT
// REACHES THE STORE is the fourth, and neither of the others can say it, because
// both start from a call this package makes to its own key builders. Under all
// of them is the thing no Go test escapes: a test fails by calling Errorf, so
// somewhere there is a first call nothing else watches, and a check on that
// check is another Errorf with the same property one level out — the regress
// this package's history walked eight times.
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
// AND WHAT IS NOT COVERED AT ALL, in the same voice. A record's IDENTITY FIELDS
// are not pinned: record() stamps rec.ID with the subject, so an entry writes
// the job its section was entered on — but RepositoryKey, Ref, CommitSHA,
// ProjectID and CreatedAt are whatever the body hands over, so a body can write
// THIS job's record carrying a neighbour's repository and ref. The rows keyed by
// the record then follow the record rather than the neighbour, and the anchor
// follows the record the section READ, so nothing crosses into another job's
// rows; what a body can do is make this job's record SAY something else. Pinning
// them is a product decision about what a transition may change, and it is the
// owner's.
//
// WHAT THIS IS THE SAME AS. It is the exposure every Go test suite already
// carries and does not write down. A TestMain that returns before m.Run(). A
// build tag on the file the assertions live in. population_test.go never added
// to the package at all. `go test` never run in CI. Each is a small edit that
// leaves a green run and no trace, and no test framework defends against any of
// them. These nine are written here instead, in the open, with the measurement
// beside them.
//
// MEASURE THE MECHANISM'S OWN PACKAGES EITHER WAY. backend/section and
// backend/row are both exercised by their own tests and by this package's, and
// the figures agree — 100.0% plain each, 100.0% pinned together — because their
// own tests drive the whole surface:
//
//	go test -count=1 -p 4 -cover ./services/build/...
//	go test -count=1 -p 4 -coverpkg=./services/build/backend/section/...,./services/build/backend/row/... ./services/build/...
package backend
