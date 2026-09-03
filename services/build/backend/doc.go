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
// Ten rounds of this package's history each added a level. The writes are held
// by a handle; the handle is held by a closure; the closure's population is held
// by scans over this package's own source; the scans are held by assertions in a
// test file; and the scans' INPUT is held by a list of file names. "What guards
// the guard" has to stop somewhere, and this is where, said exactly rather than
// left for somebody to find.
//
// THE COMPILER'S HALF ENDS NOWHERE, because nothing has to run for it. Seventeen
// expressions do not exist, and a tree that compiles is a tree in which they are
// absent:
//
//	no Enter to leave and retake         no `commit([]raft.BatchOp)`
//	no accessor handing out a holding    no `finish(…, extra …)`
//	no mutex to touch                    no method on the entry taking a key
//	no exported field on a Holding       no `Reread(closure)`
//	no minting function                  no `held.id` to assign
//	no raw op to append to an entry      no `Holding.subject` to write
//	no slice of rows to splice into      no key the sink can be handed
//	no batch expression at all           no attempt key on a bare id
//	no attempt.Of to fill or re-point
//
// EACH IS A BUILD FAILURE AND EACH HAS BEEN COMPILED. The four this round added
// were measured against the escapes that made them necessary, in their own
// words:
//
//	undefined: row.Ops / undefined: set
//	invalid append: argument must be a slice; have e.ops (variable of struct
//	  type row.Entry)
//	not enough arguments in call to k.write
//	  have (string, string) / want (attemptRowKind, time.Time, string, string)
//	too many arguments in call to attemptLogKey
//	  have (string, number, time.Time) / want (attempt.Of, time.Time)
//
// THE LEFT COLUMN IS THE SECTION'S SHAPE AND THE RIGHT IS WHAT A WRITE MAY SAY.
// The bottom two rows of each are round ten's, and what they replaced is worth
// more than themselves: both were CHECKS ON AN ARGUMENT, and both were defeated
// by respelling the argument rather than by finding new ground.
//
//   - the store's door. commit handed Store.Batch `row.Ops(e.ops)`, and the
//     guard read the outermost callee's NAME — so `append(row.Ops(e.ops),
//     forged)` failed and `row.Ops(append(e.ops, forged))` PASSED. Eight lines
//     in one file, on the committed tree with every end of the previous round
//     intact, put a row moving another repository's ref anchor on every
//     replicated entry this service writes. The rows are a row.Entry now and
//     row.Entry.Apply takes the STORE rather than the rows: there is no
//     expression of raft's batch type in this package for a name to be read off.
//   - the sink's door. attemptSink.write took a KEY, and the census that says
//     every key names its writer's own subject exempted that one owner
//     unconditionally, because a door that takes a key cannot answer a question
//     about which keys its owner may compose. One line inside it —
//     set(refLastKey("github.com/evil/repo", "refs/heads/main"), …) — was green
//     across all seven build packages and moved another repository's anchor on
//     the happy path of every build, from a writer placed outside every section
//     BY DESIGN. The door takes a KIND now and composes the key from the attempt
//     the sink was constructed for, and that attempt is another package's struct
//     with two unexported fields. There is no exemption left to write.
//
// THE RUNTIME'S HALF IS WATCHED FROM EIGHT ANGLES, so it has no small set
// either. Deleting the identity comparison in section.Holding.live, or making
// the gate call the write instead of asking the holding, each fails eight named
// tests — and hollowing requireRefused's own assertions on top does not save
// it, because every escape form ALSO asserts that the record did not move. Its
// exposure is the file, not a line. The ORDER of Do's two closing statements is
// there too: section.TestTheIdentityDiesBeforeTheExclusionIsReleased measures it
// under the contention that makes a mutex hand the lock straight to the
// goroutine waiting on it.
//
// AND TWO OF THE RUNTIME'S GUARDS ARE COUNTS, WHICH IS THE ONE SHAPE A
// RESPELLING DOES NOT REACH. row.Entry.Apply refuses an entry with no rows, and
// attemptSink.write refuses one that does not hold exactly one — a fact about
// what the entry HOLDS rather than about how anything was spelled, so a kind the
// switch does not know and a second row spliced in beside the first are the same
// failure. They are compiled in everywhere: a check that is only in the test
// build is a production binary running code no test ran.
//
// SO THE TOWER IS THE STATIC HALF, AND IT ENDS AT THIRTEEN LINES FORMING TWELVE
// ENDS — up from nine, four of which are no longer ends at all. All of them are
// in population_test.go. MEASURED ONE AT A TIME: each control below is
// GO_TEST_EXIT=1 on this tree, and GO_TEST_EXIT=0 with that ONE line hollowed
// and nothing else changed. The one exception is marked, and it takes two.
//
//	in parsePackage, the scan's own input:
//	 1 the files parsed are the files listed  the filter told to skip errors.go,
//	                                           with a row method declared there
//	 2 the files on disk are those files      a new file carrying a row method,
//	                                           and the filter told to skip it
//
//	in TestEveryKeyAnEntryWritesIsBuiltWhereTheEntryIs:
//	 3 backend/row's three verbs are only     `mk := s.ops.Set`, then mk(…) with
//	   ever CALLED, never taken as a value      another repository's anchor,
//	                                           beside anchor()'s own row — and
//	                                           `ap := e.ops.Apply` stashed in a
//	                                           package var and called after the
//	                                           section closed. BOTH were green
//
//	in TestEveryKeyAnEntryWritesNamesTheSectionsOwnJob:
//	 4 every key names the subject its        an open row keyed s.rec.ID, which
//	   writer was constructed on                is begin's own old defect
//	 5 these nine rows and no others, AND     a tenth row method — TWO LINES,
//	   who may name Set and Delete              and both must go
//	 6 an entry's rows are never assigned,    an entry handed the rows of
//	   only added to                            another entry
//
//	in TestTheSinkWritesOnlyItsOwnJobsAttemptRows:
//	 7 the sink's door has two callers        a third Sink method calling it
//	 8 the sink is assembled in one place     the sink building a sibling
//	 9 attempt.Nth has one caller             a Service method minting an
//	                                           attempt of its own
//	10 nothing assigns the attempt a sink     a method that re-points one
//	   was constructed for
//
//	in TestEveryWriteIsMadeThroughTheHandleThatReadTheRecord:
//	11 Batch is named NOWHERE in this         a second replicated batch beside
//	   package                                  the sink's own row
//	12 Apply has exactly two callers          a third Apply, on a Service
//
// AND EVERY ONE OF THE TWELVE HOLDS A NAME. That is the distinction this round
// was made to state, and it is the honest one: the ACTS are the compiler's and
// the counts', and what is left up here reads an IDENTIFIER — a file name, a
// method name, a type name, a field name, the text of an argument. A scan cannot
// do otherwise; ast.Inspect walks names.
//
// WHAT CHANGED IS THAT EACH OF THEM NOW FAILS CLOSED. A name-keyed check has two
// failure directions, and only one of them is safe:
//
//	respelled at the call site  -> the check FAILS. `x := e.h.id()` and then a
//	                               key built from x is an argument the census
//	                               does not recognise, so it fails rather than
//	                               passes. So is `bk := refLastKey`. The two that
//	                               were NOT are `mk := e.ops.Set` and
//	                               `ap := e.ops.Apply`, and line 3 exists for
//	                               exactly those: a method value leaves the
//	                               taker reading as an ordinary caller, and the
//	                               call through it carries a name no census
//	                               knows. Both were measured green.
//	renamed at the declaration  -> the check must fail too, and two of them did
//	                               not. Lines 6 and 10 are require.Empty over a
//	                               map keyed by a FIELD NAME, so a field renamed
//	                               and reassigned left them looking at a key
//	                               nothing writes. Both now ask the type whether
//	                               the field is still there.
//
// A NAME THAT FAILS CLOSED IS NOT AN ACT, and this doc does not claim it is. It
// is a check whose defeat is an EDIT TO THE CHECK rather than a rewrite of the
// code under it — which is the same exposure as the twelve lines themselves, one
// level in, and is where this stops.
//
// AND SIX MORE LINES STAND OVER GROUND THE TWELVE ALREADY COVER. They are
// written down because a round that counts its own exposure at nine when it is
// twelve is worse than one that counts nothing: the next reader stops looking.
// Each was measured the same way — hollow it, run the control it was written
// for, and see what else fails:
//
//	the key a row names is a builder call    said twice, once per test, and the
//	  — the parameter branch is GONE           Set list catches it a third time:
//	                                           a key-taking door needs all three
//	                                           hollowed before it is green
//	the sink keys its rows by its OWN         the subject census (line 4) and TWO
//	  attempt                                  behavioural tests — a retry's logs
//	                                           stop surviving. It was an END last
//	                                           round and is a second line now,
//	                                           because the sink is inside the
//	                                           general census rather than exempt
//	"write" in the closing verb set of        a second line over line 7
//	  TestEveryWriteIsMadeThroughTheHandle…
//	the anchor's exemption: its three parts   a behavioural test catches the
//	  come off the record the section READ    anchor taken off the record being
//	                                           WRITTEN instead
//	one function names a job record key       pairs with line 5
//	who may reach a replicated batch at all   pairs with lines 11 and 12
//
// HOW THE COUNT MOVED, and it is not a rearrangement. Four of the old nine are
// gone as ends. TWO LOST THEIR SUBJECT: "set() is the only maker of a set row"
// and its delete twin were about two free functions that no longer exist, and
// what replaced them — who may name row.Entry.Set — is one of the two lines of
// line 5. ONE DOES NOT COMPILE ANY MORE: "what goes into an entry's rows came
// from one of those two" was written against an append of a forged element, and
// an entry's rows are not a slice. ONE WAS DEMOTED BY BEING GENERALISED: the
// sink keying its row by its own job was an end while the sink was exempt from
// the subject census, and is a second line now that it is inside it. SEVEN ARE
// NEW, and six of those are the sink: it had ONE line on it and now has four,
// because the round that gave it a construction census also found that nothing
// had ever said who may MAKE one.
//
// TWO GUARDS ARE ON THE CENSUSES' INPUT rather than on a write, and they are not
// ends. TestEveryKeyThisPackageWritesIsInSomeCensus requires every whole key
// store.go builds to be in exactly one class and every prefix in none — the
// state refLastKey was in for eight rounds was NONE, and that is what made the
// ninth round's anchor escape unrecoverable: the reach walk could not see the
// row. TestARespeltRowMakerIsStillCounted is the other, and it is a check on a
// check: it drives the scan over an aliased row maker and an aliased key builder
// and asserts that the first is still counted as naming the verb and the second
// is a key no builder built.
//
// EVERYTHING TOUCHING THEM IS HELD, which is why the set is twelve and not
// ninety. Measured one at a time, each of these fails BY NAME rather than
// passing:
//
//	a second job record in one entry          the row census and the record-
//	                                          writer census, both by name
//	a ref anchor for another repository,      the row census: the method appears
//	  appended in a method on the handle      building refLastKey
//	the anchor taken off the record being     the row census AND a behavioural
//	  written rather than the one read        test
//	an entry method that takes a key          three lines; the draft that had one
//	  (put/drop), called with a foreign key   measured GREEN
//	a new write method on the handle          three tests
//	  reaching a replicated batch
//	a raw op appended to an entry             it does not compile
//	a raw op spliced into what the store      it does not compile: there is no
//	  is handed                               batch expression to splice into
//	a key handed to the sink                  it does not compile
//	an attempt key built on a bare id         it does not compile
//	a foreign sink built in a handle method   FOUR lines, and all four must go
//	  and written through its own door
//	a second row in the sink's entry          the row census, AND the sink's own
//	                                          count, which is not a name
//	a `stated` assembled around another       three tests, including the one
//	  job's record                            that says who may make one
//	record() not stamping the subject onto    the behavioural test and the
//	  the record it encodes                   record-writer census
//	the retention row stamped with the        the retry test, by name
//	  record's CreatedAt
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
// WHY IT CANNOT BE SMALLER. The twelve are four regions and no line of one
// covers another. WHICH FILES ARE READ is under everything, because a scan reads
// what it is given. WHICH ROWS EXIST and EACH ROW NAMES ITS OWN SUBJECT cannot
// be folded together: a row naming the wrong subject is IN the census, and a
// check that every row names its own subject cannot see a row no census counted.
// WHAT MAKES A ROW is a third — the type says a row comes from Set or Delete,
// and only an enumeration can say those two are called from where a key was
// composed, and only line 3 can say a verb was called at all rather than taken.
// WHO MAY WRITE AT ALL is the fourth, and it has two halves that do not reach
// each other: the entry's, which is Apply and the absence of Batch, and the
// sink's, which is a door, a constructor, a minting call and a field. Under all
// of them is the thing no Go test escapes: a test fails by calling Errorf, so
// somewhere there is a first call nothing else watches, and a check on that
// check is another Errorf with the same property one level out — the regress
// this package's history walked nine times.
//
// AND ONE OF THEM IS A REVIEW-TIME GUARD WHERE A COMPILER COULD DO IT, said out
// loud. "About what" is the compiler's for the SUBJECT and an enumeration for
// the KEY GRAMMAR, because store.go's builders take an id as a string and those
// same builders are the READERS' — job(), admit(), lastCommit() and reconcile's
// listing all call them with an id no section handed over. Putting the grammar
// behind the subject would make a foreign key a compile error and would take
// every reader in the package with it. THE ATTEMPT ROWS ARE THE ONE PLACE IT WAS
// AFFORDABLE, and this round did it there: attemptLogKey and attemptAssetKey
// take an attempt.Of, because their only caller is a writer. That is the
// boundary, and where it sits is a choice rather than an oversight.
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
// them. These twelve are written here instead, in the open, with the measurement
// beside them.
//
// MEASURE THE MECHANISM'S OWN PACKAGES EITHER WAY. backend/section, backend/row
// and backend/attempt are exercised by their own tests and by this package's,
// and the figures agree — 100.0% plain each, 100.0% pinned together — because
// their own tests drive the whole surface:
//
//	go test -count=1 -p 2 -cover ./services/build/...
//	go test -count=1 -p 2 -coverpkg=./services/build/backend/section/...,./services/build/backend/row/...,./services/build/backend/attempt/... ./services/build/...
package backend
