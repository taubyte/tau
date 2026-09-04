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
// It does not run a build. Runner, Work, Output and Assets are the seam, and
// the reason is not effort: v2 has no tns schema to publish a compiled config
// to, and reaching for v1's clients would import the cbor stack v2 exists to
// leave. Everything around the run — idempotency, claiming, attempts,
// cancellation, the before..after anchor, the log a viewer can follow and the
// cids of what a run produced — ships now, so the runner is a drop-in.
//
// It does not STORE the bytes either. Assets (config.go) is the seam onto v2's
// storage service — v1's hoarder — which is not written yet, and what a real
// implementation owes it is written down there rather than guessed at here. A
// node without one still builds; its logs are followable while the build runs
// and gone afterwards, and its jobs carry no cids.
//
// # A log is a file, and only its cid is replicated
//
// The one part of a build that streams while it runs, and the one this package
// used to write a REPLICATED ENTRY PER LINE for. Three lives (output.go):
//
//	while the build runs   an append-only file this node owns
//	                       (services/build/livelog). No consensus, no RPC per
//	                       line, nothing agreed by the queue's group.
//	while somebody watches a reader forks off it AT AN OFFSET and follows,
//	                       blocking at the end rather than seeing EOF. Many
//	                       readers, each at its own offset. Service.Follow is
//	                       the seam; v1 has no version of this at all.
//	when the build ends    the file goes to the asset store and the cid goes on
//	                       the record, in the entry that settles the job — and
//	                       it goes there only if the WHOLE log reached the
//	                       store, counted rather than assumed, because a cid
//	                       over a prefix reads like a log to everything
//	                       downstream of it.
//
// WHAT THAT DELETED IS A CLASS OF DEFECT, not a component. The sink was THE ONE
// WRITER OF A REPLICATED ROW IN THIS PACKAGE OUTSIDE EVERY SECTION, by design,
// because a build produces rows while it runs — and three of the last four
// escapes found here went through it. No row is written outside a section now,
// and four of the ends below went with it.
//
// A WRITER OUTSIDE EVERY SECTION IS NOT THE SAME THING AS A ROW, AND THE ROUND
// THAT SAID SO OVERCLAIMED. There are two stores here. Deleting the sink closed
// the raft one; the ASSET store — durable on another node before it returns,
// the only door in this package that leaves this machine — is opened by a build
// WHILE IT RUNS, from the runner's goroutine, with no section anywhere, and it
// always was. It cannot be otherwise: a section is jobLock, so a Put inside one
// would hold every job on this node behind the slowest build running on it.
// What changed with the sink is what that writer may leave behind — CONTENT,
// whose cid goes no further than the run's own memory until the section that
// settles the job records it — and what was missing until now is that anything
// counted it. Nothing did: a second door on the Service in output.go, storing
// an object per viewer from Follow, was GREEN across every package under
// services/build, all fifteen censuses included. Six of the ends below are the
// answer, and they are the first this package has ADDED rather than removed
// since the sink went.
//
// It still does not serve log or artifact BYTES to a caller. v1's
// GET /logs/{cid} is an open IPFS gateway gated only by a token being valid —
// so a cid, which is an ADDRESS, is treated as a credential — and its /download
// route has no authorization at all. When those come back they are per-job
// routes on the frontend, authorized like Get on the caller's write access to
// the job's repository. Following a RUNNING build is the same rule and the same
// gate; where the route lives is decided by the topology change, and output.go's
// Follow says why it is not here.
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
// THE COMPILER'S HALF ENDS NOWHERE, because nothing has to run for it. Fifteen
// expressions do not exist, and a tree that compiles is a tree in which they are
// absent:
//
//	no Enter to leave and retake         no `commit([]raft.BatchOp)`
//	no accessor handing out a holding    no `finish(…, extra …)`
//	no mutex to touch                    no method on the entry taking a key
//	no exported field on a Holding       no `Reread(closure)`
//	no minting function                  no `held.id` to assign
//	no raw op to append to an entry      no `Holding.subject` to write
//	no slice of rows to splice into      no cid a runner can hand over
//	no batch expression at all           no attempt row to key at all
//
// EACH IS A BUILD FAILURE AND EACH HAS BEEN COMPILED. The one this round added
// and the row it is about, in their own words:
//
//	cannot use "cid-forged" (constant of type string) as "io".Reader value in
//	  argument to out.Asset: string does not implement "io".Reader (missing
//	  method Read)
//	undefined: attemptLogKey
//
// THE FIRST IS WHAT AN Output CANNOT SAY. A run hands over BYTES and never a
// content id, so a runner cannot make a job point at something nobody stored —
// the record's cids are all composed by the one thing that put the bytes
// somewhere. The second is the row class this round deleted: there is no log
// key to build, because there is no log row.
//
// AND THREE EXPRESSIONS LEFT THIS LIST WITH THE WRITER THEY WERE ABOUT. "No key
// the sink can be handed", "no attempt key on a bare id" and "no attempt.Of to
// fill or re-point" were the compiler's half of guarding the one writer outside
// every section, down to a whole package (backend/attempt) whose only purpose
// was to make an attempt un-assignable. A guard whose subject is gone is not a
// guard, so all of it went rather than being kept as reassurance.
//
// THE LEFT COLUMN IS THE SECTION'S SHAPE AND THE RIGHT IS WHAT A WRITE MAY SAY.
// The store's door is the one worth restating, because it was a CHECK ON AN
// ARGUMENT and was defeated by respelling the argument rather than by finding
// new ground: commit handed Store.Batch `row.Ops(e.ops)`, and the guard read
// the outermost callee's NAME — so `append(row.Ops(e.ops), forged)` failed and
// `row.Ops(append(e.ops, forged))` PASSED. Eight lines in one file, on the
// committed tree with every end of that round intact, put a row moving another
// repository's ref anchor on every replicated entry this service writes. The
// rows are a row.Entry now and row.Entry.Apply takes the STORE rather than the
// rows: there is no expression of raft's batch type in this package for a name
// to be read off.
//
// THE OTHER DOOR IS GONE RATHER THAN NARROWED, and it is the round this one is.
// The sink's write() took a KEY, and the census that says every key names its
// writer's own subject exempted that one owner unconditionally, because a door
// that takes a key cannot answer a question about which keys its owner may
// compose. One line inside it —
// set(refLastKey("github.com/evil/repo", "refs/heads/main"), …) — was green
// across all seven build packages and moved another repository's anchor on the
// happy path of every build, from a writer placed outside every section BY
// DESIGN. The round after that made the door take a KIND and composed the key
// from an attempt it could not assign; this one asks what the door was FOR, and
// the answer is a log — which is content, which needs no consensus, and which
// is a file (output.go). What is deleted is the writer, the two key builders,
// the row class, the package that made an attempt un-assignable, and the four
// ends that watched all of it.
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
// AND ONE OF THE RUNTIME'S GUARDS IS A COUNT, WHICH IS THE ONE SHAPE A
// RESPELLING DOES NOT REACH: row.Entry.Apply refuses an entry with no rows — a
// fact about what the entry HOLDS rather than about how anything was spelled.
// There were two, and the second was the sink's "exactly one row", which went
// with the door it guarded. It is compiled in everywhere: a check that is only
// in the test build is a production binary running code no test ran.
//
// SO THE TOWER IS THE STATIC HALF, AND IT ENDS AT FIFTEEN LINES FORMING
// FOURTEEN ENDS — nine and eight of them over the rows, six more over the
// other store. All of them are in population_test.go. Each was MEASURED ONE AT
// A TIME when it was written: GO_TEST_EXIT=1 on the tree of its round, and
// GO_TEST_EXIT=0 with that ONE line hollowed and nothing else changed. The one
// exception is marked, and it takes two.
//
// SIX WERE ADDED THIS ROUND AND TWO SPELLINGS WERE CORRECTED, which is the
// first time the list has grown since the sink went. The six are the asset
// store's: a build's content leaves this node from outside every section, and
// nothing had ever counted the doors it leaves by (lines 9 to 14). Line 13
// caught the others out: the four written before it were all GREEN against
// `door := o.s.assetStore.Put`, taken inside a caller they allow and called
// through a package-level var.
//
// AND TWO OF THE ATTACKS BELOW WERE SPELT SO THAT THE MEASUREMENT THEY CLAIM
// COULD NOT BE REPRODUCED. Both are corrected in place, and both were the same
// error — an attack that trips OTHER tests as well is not a measurement of the
// line it is written against:
//
//   - line 7 read "a second replicated batch beside an entry's own rows",
//     which is a Batch spliced INSIDE commit. Measured: four tests, two of them
//     behavioural (TestAPushThatFailsAfterTheRecordIsReported and
//     TestCancelReportsAWithdrawalThatFailedAndTheNextCancelFinishesIt), so
//     hollowing line 7 leaves the tree RED. Only an isolated placement — a door
//     onto the store's batch that nothing calls yet — makes line 7 the sole
//     catcher, and that is what it now says.
//   - line 5's "ninth row method" trips THREE lines when the row it writes is
//     an OPEN row, because the record-and-open-row pairing catches that one
//     too. Spelled as a project-index row it is the two the line claims, both
//     of which must go for GO_TEST_EXIT=0.
//
// The count itself was a recount when the sink went, and that still holds for
// the nine: nothing was added then, four were removed with their subject, and
// the two lists inside end 5 and end 8 shrank by the rows and the caller the
// deletion took. What was re-measured then is that the survivors still FIRE on
// the file that replaced the sink: a row method on runOutput, in output.go,
// composing another repository's anchor and applying a batch of its own. Two
// lines named it — "runOutput.forge makes a row and hangs off "runOutput",
// which names no subject a key can be composed from" (line 4) and Apply's
// caller list (line 8) — because a census that silently stopped reading a NEW
// file would be the failure mode line 2 exists for, and a new file is exactly
// what that round added.
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
//	 5 these eight rows and no others, AND    a ninth row method writing a
//	   who may name Set and Delete              PROJECT-INDEX row — TWO LINES,
//	                                           and both must go. An OPEN row
//	                                           instead trips a third, in the
//	                                           pairing test below
//	 6 an entry's rows are never assigned,    an entry handed the rows of
//	   only added to                            another entry
//
//	in TestEveryWriteIsMadeThroughTheHandleThatReadTheRecord:
//	 7 Batch is named NOWHERE in this         a door onto the store's batch, on
//	   package                                  the Service, that nothing calls
//	                                           yet. Spliced inside commit
//	                                           instead it trips four tests, two
//	                                           of them behavioural
//	 8 Apply has exactly ONE caller           a second Apply, on a Service
//
//	in TestEveryWriteToTheAssetStoreIsNamedAndPlaced, over the OTHER store:
//	 9 who may NAME the asset store           an accessor handing it out, which
//	                                           calls nothing on it
//	10 who may CALL Put                       a door HANDED the store, which
//	                                           names no field
//	11 publish has exactly ONE caller         a second publish door, unused —
//	                                           and a door is used next round
//	12 nothing here calls Asset               this package storing something of
//	                                           its own through the runner's door
//	13 Put is CALLED and never taken          the method value, stashed in a
//	                                           package var — the taker is an
//	                                           allowed caller and the call
//	                                           through it names nothing
//	14 who may TOUCH the run's cids           a reader of them outside the two,
//	                                           spending a cid where no section
//	                                           can see it
//
// AND EVERY ONE OF THE FOURTEEN HOLDS A NAME. That is the distinction this round
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
// code under it — which is the same exposure as the nine lines themselves, one
// level in, and is where this stops.
//
// AND SIX MORE LINES STAND OVER GROUND THE THIRTEEN ALREADY COVER. They are
// written down because a round that counts its own exposure at five when it is
// eight is worse than one that counts nothing: the next reader stops looking.
// Each was measured the same way — hollow it, run the control it was written
// for, and see what else fails:
//
//	the key a row names is a builder call    said twice, once per test, and the
//	  — the parameter branch is GONE           Set list catches it a third time:
//	                                           a key-taking door needs all three
//	                                           hollowed before it is green
//	the anchor's exemption: its three parts   a behavioural test catches the
//	  come off the record the section READ    anchor taken off the record being
//	                                           WRITTEN instead
//	one function names a job record key       pairs with line 5
//	the publish is inside settle's OWN        the two runner tests that assert a
//	  section                                  lost delivery ships nothing catch
//	                                           a settle that publishes first —
//	                                           hollowing this leaves both red
//	who may WRITE the run's cids              a new writer is a new toucher, so
//	                                           line 14 has it; an allowed
//	                                           toucher that writes trips five
//	                                           behavioural tests
//	the run's cid field still exists          line 14 and the writer line above
//	                                           are matches against NON-EMPTY
//	                                           lists, so a rename fails them on
//	                                           its own — unlike the two
//	                                           require.Empty lines this shape
//	                                           was written for
//
// HOW THE COUNT MOVED, TWICE. The round before this one took FOUR OF THE TWELVE
// AWAY AT ONCE: all four were the sink — who may call its door, who may make
// one, who may mint the attempt it is for, and that nothing re-points that
// attempt — and there is no sink. Three of the six extra lines went the same
// way: "the sink keys its rows by its own attempt", "'write' in the closing
// verb set", and the sink's own row count. Nothing was ADDED then, which is the
// honest shape of a deletion: the writer that package spent four rounds fencing
// in was not fenced better, it was not there.
//
// AND THAT IS WHERE THE OVERCLAIM WAS. "There is no writer outside a section"
// was true of ROWS and read as true of writes, and the sentence went into three
// files. A build's content still leaves this node from outside every section,
// because it must; what was missing was any census of the door it leaves by.
// This round adds six ends and three lines over covered ground — the first
// addition since the sink — and corrects the sentence in the three places it
// was written: here, output.go, and the two censuses that said the class was
// closed.
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
// EVERYTHING TOUCHING THEM IS HELD, which is why the set is fourteen and not
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
//	a cid a runner hands over instead of      it does not compile
//	  the bytes
//	a log row keyed by anything at all        it does not compile: there is no
//	                                          builder for one
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
// WHY IT CANNOT BE SMALLER. The fourteen are five regions and no line of one
// covers another. WHICH FILES ARE READ is under everything, because a scan reads
// what it is given. WHICH ROWS EXIST and EACH ROW NAMES ITS OWN SUBJECT cannot
// be folded together: a row naming the wrong subject is IN the census, and a
// check that every row names its own subject cannot see a row no census counted.
// WHAT MAKES A ROW is a third — the type says a row comes from Set or Delete,
// and only an enumeration can say those two are called from where a key was
// composed, and only line 3 can say a verb was called at all rather than taken.
// WHO MAY WRITE A ROW AT ALL is the fourth, and it is ONE half now rather than
// two: the entry's, which is Apply and the absence of Batch. The other half was
// the sink's — a door, a constructor, a minting call and a field — and it is
// gone with it. WHO MAY WRITE TO THE OTHER STORE is the fifth, and no line of
// the first four reaches it: they are all about rows, and what leaves this node
// during a build is bytes. Under all of them is the thing no Go test escapes: a test fails by calling Errorf, so
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
// every reader in the package with it. The attempt rows were the one place it
// was affordable, and a round did it there — those two builders took a struct
// only their writer could make. The rows are deleted now, so the boundary is
// back where it was: every key builder in store.go takes a string, and the
// enumeration is what says who may call one. Where it sits is a choice rather
// than an oversight.
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
// AND THE DOOR OUT OF THIS NODE IS COUNTED BUT NOT BOUNDED. The census says who
// may call Put and from where; it says nothing about HOW MUCH, and nothing
// else does either — MaxLogBytes caps the log a build may write and there is no
// equivalent for the artifacts it hands over, in number or in bytes, so what a
// build script stores is what a build script decides. It compounds with the
// debt config.go already names: nothing in this service unreferences an asset.
// A bound belongs with the storage service that does not exist yet, and it is
// the owner's call where it goes.
//
// WHAT THIS IS THE SAME AS. It is the exposure every Go test suite already
// carries and does not write down. A TestMain that returns before m.Run(). A
// build tag on the file the assertions live in. population_test.go never added
// to the package at all. `go test` never run in CI. Each is a small edit that
// leaves a green run and no trace, and no test framework defends against any of
// them. These fourteen are written here instead, in the open, with the
// measurement beside them.
//
// MEASURE THE MECHANISM'S OWN PACKAGES EITHER WAY. backend/section and
// backend/row are exercised by their own tests and by this package's, and the
// figures agree — 100.0% plain each, 100.0% pinned together — because their own
// tests drive the whole surface. backend/attempt was a third and is deleted:
// its whole purpose was to make the sink's attempt un-assignable. The log
// writer that replaced it is measured the same way and reads the same, plain
// and pinned.
//
//	go test -count=1 -p 2 -cover ./services/build/...
//	go test -count=1 -p 2 -coverpkg=./services/build/backend/section/...,./services/build/backend/row/... ./services/build/...
//	go test -count=1 -p 2 -coverpkg=./services/build/livelog/... ./services/build/...
package backend
