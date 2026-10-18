//go:build dreaming

package dream_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/fleet"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/raft"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// project_test runs AUTHORED PROJECTS, compiled by tcc, in a universe.
//
// # Why these are yaml and not compiled objects
//
// Most fixtures in this package are the compiled object, written by hand. That
// is the right shape when what is under test is the runtime's reading of one
// field. It is the wrong shape for asking whether the PLATFORM works, because a
// hand-written object is a statement of what I believe the compiler produces —
// it keeps passing on the day the compiler produces something else, which is
// the day it matters.
//
// So these start where an author starts: a directory of yaml. tcc compiles it,
// the register publishes it, and the gateway is asked a question over HTTP. What
// a green test here says is that the whole path holds, from a file somebody
// wrote to bytes on a socket.
//
// # Where a project lives says what it exercises
//
// A project's path under projects/ mirrors the vocabulary's own axes, so the
// directory IS the description and nothing has to be looked up:
//
//	with/function                          one function, one route, nothing else
//	with/component                         …whose source is a COMPONENT, not a path
//	with/database/distributed/kv/crdt      …and a kv, tau's engine, merge resolution
//	with/database/distributed/kv/raft      …and a ledger, where writes are ordered
//	with/database/postgres/sql             …and a database behind a connector
//	with/sync/queue                        …and a queue
//
// `database` is a grid — four models, an engine axis, a consensus axis — and a
// project that said only "withdatabase" would claim to exercise all of it while
// touching one cell. ONE PROJECT PER CELL, so a directory is a claim somebody
// can check: the crdt one round-trips through a store whose writes merge, and
// the raft one through a group that orders them, and neither hides inside the
// other.
//
// A CELL MAY HOLD A PAIR, and the `-b` directories are the one reason for it. A
// project is also an ISOLATION boundary, and nothing can be said about a
// boundary with one project on the tree. So each `-b` is its sibling's twin —
// the same database id, the same label, the same environment, the same model —
// differing only in the project it is published under and in the one host name
// a register will not let two projects share (pkg/register/register.go:311-312).
// It is not a second claim about the cell; it is the other half of one.
//
// A TWIN'S OWN PROJECT ID IS GENERATED AND NOT TYPED. It is the one string in a
// twin that is new rather than copied from its sibling, and an invented base58
// string is not a content id: it fails at runtime with a multihash length
// nobody reading the yaml would think to look for. So each is a real CIDv0 over
// the project's own name — base58btc(0x12 0x20 || sha256(name)) — which is a
// line anybody can rerun against the name still in the file.
//
// # They start small on purpose
//
// `with/function` is the smallest project that answers anything: one function,
// one zone, one route. Everything that goes wrong in it goes wrong for every
// project after it, so it is the one to read first when the corpus turns red.
//
// # The one substitution
//
// Every function here names a source repository nobody built, because building
// it needs a git server and a toolchain. So each is pointed at a component this
// tree already ships. THAT IS A DEPLOYMENT FACT AND NOT A SHAPE ONE, and it is
// said where a deployment fact belongs: the ARTIFACT SEAM. The test puts the
// component in the asset store and tells the universe's seam which resource runs
// it, exactly as a cloud's build service does — see nodeDream.Built.
//
// IT USED TO BE SAID IN THE DOCUMENT, and that was the shape one. Every
// resource's `source` was overwritten with `{kind: component, component: <cid>}`
// before publishing, so `component` meant "this already IS an artifact" — which
// the vocabulary never said it means, and which only worked because the worker
// handed the reference straight to the asset store. The corpus was therefore
// incapable of catching that bug: the one source kind an author can write there
// was never exercised with what an author writes.

// artifacts says which component each resource is pointed at, keyed by the label
// the author wrote, with "" the artifact everything unnamed gets.
//
// A MAP RATHER THAN ONE CID BECAUSE A PROJECT MAY NEED TWO WORLDS. Every project
// here but one runs a single binary under several names, and the substitution
// that made that work — point everything carrying a `source` at the one artifact
// handed in — cannot express the authorizer project, where the guard is a
// DIFFERENT component built against a different host interface. Naming the
// exception and defaulting the rest leaves every other project saying what it
// said: one artifact, for everything.
type artifacts map[string]string

// of is the component this resource runs, which is its own entry when it has one.
func (a artifacts) of(label string) string {
	if cid, named := a[label]; named {
		return cid
	}
	return a[""]
}

// project compiles one of the authored projects under projects/ and says which
// component each of its resources runs. It answers the bytes a register is
// published, the object they were marshalled from — because a test that has to
// name a resource reads the id out of what was published rather than restating
// one, see idOf — and the mapping the universe's artifact seam is handed.
//
// THE COMPILED DOCUMENT IS NOT TOUCHED. What a resource RUNS is not in it: the
// author wrote a path in a repository and the artifact is what came out of
// building that, which is the build service's to say in a cloud and the test's to
// say here. Both say it through worker.Artifacts.
func project(t *testing.T, name string, components artifacts) ([]byte, map[string]any, map[string]string) {
	t.Helper()

	c, err := schema.New(
		schema.WithLocal(projectDir(name)),
		schema.WithBranch("main"))
	require.NoError(t, err)

	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)

	flat := obj.Flat()
	root, ok := flat["object"].(map[string]any)
	require.True(t, ok, "%s compiled to something with no object in it", name)

	pointed := map[string]string{}
	runs := 0
	for _, group := range root {
		instances, ok := group.(map[string]any)
		if !ok {
			continue
		}
		for id, v := range instances {
			res, ok := v.(map[string]any)
			if !ok {
				continue
			}
			// A RESOURCE WITH NO `source` RUNS NOTHING -- a zone, a cache, a
			// channel. It is also how the set of things needing an artifact is
			// read off the document rather than listed here.
			if _, isSource := res["source"]; !isSource {
				continue
			}
			// AN IMAGE IS ITS OWN ARTIFACT. A workload whose source names an
			// image says what runs in the document itself -- the worker reads it
			// straight off the resource (artifactOf in services/worker/resolve.go)
			// and nothing is built -- so there is nothing to stage for it, and
			// staging something anyway would hide a worker that went asking.
			if src, ok := res["source"].(map[string]any); ok && src["kind"] == "image" {
				runs++
				continue
			}
			label, _ := res["name"].(string)
			cid := components.of(label)
			require.NotEmpty(t, cid, "%s names %q and nothing was handed in for it", name, label)

			// A WORKLOAD WHOSE SOURCE IS A COMPONENT HAS NO BUILD OF ITS OWN, and
			// the mapping says exactly that: the artifact is filed under the
			// COMPONENT'S id -- which is the id the seam is asked about, see
			// artifactOf in services/worker/resolve.go. Staging the workload's own
			// id as well would let a seam asked the WRONG question answer anyway,
			// and with/component exists to ask the right one.
			if src, ok := res["source"].(map[string]any); ok {
				if component, _ := src["component"].(string); component != "" {
					pointed[component] = cid
					runs++
					continue
				}
			}
			pointed[id] = cid
			runs++
		}
	}
	require.NotZero(t, runs, "%s has nothing to run", name)

	raw, err := json.Marshal(flat)
	require.NoError(t, err)
	return raw, root, pointed
}

// lastSaid renders whatever the last attempt answered, at the moment the
// assertion gives up rather than at the moment it was written.
type lastSaid struct{ at *string }

func (l *lastSaid) String() string { return *l.at }

// running is a universe holding one project, and a way to ask it something.
type running struct {
	node *node.Node
	host string

	// object is the compiled project this universe was published, kept so a
	// test can ask what a label became rather than say it — see idOf.
	object map[string]any
}

// ask puts a request to the gateway the way the internet would.
func (r *running) ask(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	return r.askCarrying(t, method, path, "body")
}

// askCarrying is ask with the body named.
//
// A TEST THAT PUTS TWO PROJECTS ON ONE KEY HAS TO WRITE TWO VALUES, or a store
// shared between them would be indistinguishable from two stores: every read
// would answer "body" whichever copy it came out of, and the test would pass
// with the isolation gone. Nothing else here cares, which is why ask keeps the
// shorter shape.
func (r *running) askCarrying(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, "http://"+r.host+path, bytes.NewReader([]byte(body)))
	req.Host = r.host
	w := httptest.NewRecorder()
	r.node.Handler().ServeHTTP(w, req)
	return w
}

// on puts the same request to THE NODE NAMED, which ask cannot do.
//
// A GATEWAY CHOOSES A ROUTE AND NEVER A NODE: ask hands the invocation to
// placement, which takes the least loaded node that already has the project
// warm (services/v2/placement/placement.go:390-405) and does not favour the one
// it is running on (:126-140). So a test that means "this node" has to say it
// here, at the worker the gateway would have reached anyway — the guest still
// runs, with its grant, its tier and that node's own engine behind it.
func (r *running) on(ctx context.Context, at specs.Address, method, path string) (*invoke.Response, error) {
	return r.onCarrying(ctx, at, method, path, "body")
}

// onCarrying is on with the body named, for askCarrying's reason.
func (r *running) onCarrying(ctx context.Context, at specs.Address, method, path, body string) (*invoke.Response, error) {
	return r.node.Worker().Invoke(ctx, at, &invoke.Request{
		Method: method, Path: path, Body: []byte(body),
	})
}

// idOf answers what a LABEL compiled to, out of the object this universe was
// published.
//
// A RESOURCE LEAVES THE COMPILER WITH ITS NAME BEHIND IT. The worker copies the
// resource's ID into the grant handle (services/worker/resolve.go:189), so a
// test that asks the node about one has to say the same string — and a
// hardcoded id would be a second statement of what the compiler produced, which
// is the one thing these projects exist to stop. IT WOULD ALSO GO WRONG
// QUIETLY: Node.QueueDepth answers `ok` false for a handle this deployment
// declares no queue for, for a node that can reach no primitives service, and
// for one whose group has not formed alike, so a stale id is indistinguishable
// from a cloud that is not ready.
func (r *running) idOf(t *testing.T, group, label string) string {
	t.Helper()

	in, ok := r.object[group].(map[string]any)
	require.True(t, ok, "the project compiled no %s at all", group)

	for id, v := range in {
		res, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if res["name"] == label {
			return id
		}
	}

	require.FailNowf(t, "nothing compiled under that label", "no %s is named %q", group, label)
	return ""
}

// deployed names one project to put into a universe: the string the REGISTER
// knows it by, the corpus directory under projects/ it is compiled from, and
// the host it answers on.
//
// THE REGISTER'S PROJECT IS NOT project.yaml's `id:`, and only one of the two
// is an isolation boundary. The compiled object's id is an index prefix that
// locate() cuts (pkg/specs/project.go:949-953) and that nothing on the gateway,
// worker or database path ever compares to anything. This string is what
// pkg/register keys its pointers by (register.go:171), what the gateway puts
// into specs.Address, what the worker copies into the grant handle
// (services/worker/resolve.go:189) and what finally becomes
// database.Spec.Project (node/database/database.go) — the segment every isolation
// claim below rests on.
//
// TWO PROJECTS CANNOT SHARE A HOST, which is the one other thing that has to
// differ: a register refuses a second claim on an fqdn outright
// (pkg/register/register.go:311-312), so each corpus twin declares its own label.
type deployed struct {
	project string
	name    string
	host    string
}

// publish puts one project into a universe that is already running, and answers
// a door onto it through each of the nodes named.
//
// IT IS SPLIT OUT OF deploy BECAUSE A UNIVERSE WAS NEVER SINGLE-PROJECT and
// only this helper was. The register keys pointers by project, every cache is
// keyed by the deployment it belongs to, and a node's database connections hang
// off a handle carrying the project (node/database/database.go) — so publishing
// twice needs no change to the universe, the zone, or the node, only for the
// start and the publish to stop being one function.
//
// IT TAKES PEER IDS AND NOT NODES because a project is two publishes and not
// one: the register gets the compiled document, and every node's artifact seam
// gets what a build produced. Placement chooses which node runs a workload, so a
// staging on one of them would be a cloud where the build service is reachable
// from one machine.
func publish(t *testing.T, pids []string, at deployed, components artifacts) []*running {
	t.Helper()

	compiled, obj, pointed := project(t, at.name, components)
	stagedOn(t, pids, at.project, pointed)

	nodes := make([]*node.Node, 0, len(pids))
	for _, id := range pids {
		nodes = append(nodes, nodeOf(t, id))
	}

	_, err := nodes[0].Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: at.project, Environment: "prod", Revision: 1,
		Compiled: compiled, Hosts: []string{at.host},
	})
	require.NoError(t, err)

	out := make([]*running, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, &running{node: n, host: at.host, object: obj})
	}
	return out
}

// deploy starts a universe, publishes the named project into it, and answers a
// door onto the one node it started.
//
// NOTHING HERE WAITS FOR THE PROJECT TO BE IN USE. Publishing puts the
// register's pointer in place and returns; a node resolves and warms a project
// when something first asks it for one. So the FIRST assertion a test makes is
// the one that has to retry, and every test below is written that way.
func deploy(t *testing.T, name, host string, wasm []byte) *running {
	t.Helper()
	return deployWith(t, name, host, wasm, nil)
}

// deployWith is deploy for a project that needs more than one binary, keyed by
// the label the author wrote — see the artifacts map.
func deployWith(t *testing.T, name, host string, wasm []byte, extra map[string][]byte) *running {
	t.Helper()
	return deployServing(t, name, host, wasm, extra,
		node.Register, node.Worker, node.Stats, node.Gateway, node.Database, node.Primitives)
}

// deployServing is deployWith over a chosen set of services.
//
// THE SET IS A PARAMETER BECAUSE NOT EVERY WAY IN IS HTTP. A zone answered by a
// function is reached over UDP by a resolver, and that node runs no gateway at
// all -- so the default list is the common case rather than the only one.
func deployServing(t *testing.T, name, host string, wasm []byte, extra map[string][]byte, services ...string) *running {
	t.Helper()

	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{Services: services})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	pids := u.Pids()
	require.NotEmpty(t, pids)

	store, ok := nodeDream.Assets(pids[0])
	require.True(t, ok)

	cid, err := store.Put(t.Context(), bytes.NewReader(wasm))
	require.NoError(t, err)

	held := artifacts{"": cid}
	for label, code := range extra {
		id, err := store.Put(t.Context(), bytes.NewReader(code))
		require.NoError(t, err)
		held[label] = id
	}

	return publish(t, pids[:1], deployed{project: "shop", name: name, host: host}, held)[0]
}

func handlerWASM(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile(filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", "handler.wasm"))
	require.NoError(t, err, "run `cargo component build --release` in pkg/invoke/wasm/fixtures/handler")
	return code
}

// TestTheSmallestProjectAnswers. One function, one zone, one route — authored as
// yaml, compiled by tcc, published, and asked a question over HTTP.
//
// THE STATUS IS THE PROOF AND NOT THE BODY. The component answers 200 to a GET
// and 201 to a POST, so a passing assertion here cannot be a constant somewhere
// in the path: the method the gateway parsed reached the guest and came back.
func TestTheSmallestProjectAnswers(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	r := deploy(t, "with/function", "hello.example.com", handlerWASM(t))

	got := r.ask(t, http.MethodGet, "/")
	require.Equal(t, 200, got.Code, "a GET reached the function")
	require.Equal(t, "/", got.Header().Get("x-path"), "and the path it was asked for came back")

	posted := r.ask(t, http.MethodPost, "/orders")
	require.Equal(t, 201, posted.Code, "and a POST is a different answer from the same component")
	require.Equal(t, "/orders", posted.Header().Get("x-path"))
}

// TestAFunctionThatNamesAComponentRunsTheCOMPONENTSARTIFACT.
//
// THE ONE SOURCE KIND WHOSE ARTIFACT BELONGS TO SOMETHING ELSE. Everywhere else
// in this corpus a workload's source is a path and the artifact is its own; here
// the function names a COMPONENT, and what runs is what that component's build
// published — filed under the COMPONENT'S id and not under the function's.
//
// IT IS THE PROJECT THAT WOULD HAVE CAUGHT THE BUG, and the reason this corpus
// could not. `source.component` is a `Ref("component")`, so a compiled resource
// carries the component's RESOURCE id; the worker used to hand that to the asset
// store verbatim, which is a content store being asked for a configuration
// document. Nothing ran. The corpus could not see it because it rewrote every
// source into exactly that shape with a real cid in it — so the one source kind
// an author can write there was never exercised with what an author writes.
//
// A 200 HERE IS THE REFERENCE HAVING BEEN RESOLVED: the artifact is staged under
// the component's id, the way a build publishes it, and nothing is staged under
// the function's.
func TestAFunctionThatNamesAComponentRunsItsArtifact(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	r := deploy(t, "with/component", "shared.example.com", handlerWASM(t))

	got := r.ask(t, http.MethodGet, "/")
	require.Equal(t, 200, got.Code, "the component the function names answered")
	require.Equal(t, "/", got.Header().Get("x-path"), "and the request reached it")
}

func kitchenSinkWASM(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile(filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", "cacher.wasm"))
	require.NoError(t, err, "run `cargo component build --release` in pkg/invoke/wasm/fixtures/cacher")
	return code
}

// TestAProjectWithAKvDatabaseKeepsSomething.
//
// The smallest project where a request changes state that outlives it: one
// function, one kv database it is granted, one route. The guest picks its
// operation off the path and carries the value in the body, so a PUT and a GET
// are two requests through the whole platform — gateway, placement, the tier,
// the grant, and tau's own replicated store — rather than two calls to a fake.
//
// WHAT THIS CATCHES THAT A UNIT TEST CANNOT: the grant has to survive being
// compiled, published, resolved into an invoke.Spec and handed to the tier under
// the label the AUTHOR wrote. `sessions` is that label, and the guest asks for
// it by name; a break anywhere along it is a refusal here.
func TestAProjectWithAKvDatabaseKeepsSomething(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	r := deploy(t, "with/database/distributed/kv/crdt", "keep.example.com", kitchenSinkWASM(t))

	// A WRITE IS 204 AND A REFUSAL IS 403, so the status says which arm of the
	// host interface answered rather than only that something did.
	stored := r.ask(t, http.MethodPost, "/kv-put/sessions/token-1")
	require.Equal(t, 204, stored.Code, "the write was refused: %s", stored.Body.String())

	read := r.ask(t, http.MethodGet, "/kv-get/sessions/token-1")
	require.Equal(t, 200, read.Code, "the read was refused: %s", read.Body.String())
	require.Equal(t, "body", read.Body.String(), "what came back is not what went in")

	// AND A KEY NOBODY WROTE IS A MISS, which is its own arm of the interface
	// rather than an empty success.
	missing := r.ask(t, http.MethodGet, "/kv-get/sessions/never-written")
	require.Equal(t, 404, missing.Code)
	require.NotEqual(t, "body", missing.Body.String())

}

// TestALEDGERANSWERSEVERYNODEWITHWHATITSLEADERCOMMITTED.
//
// `consensus: raft` promises that a write costs a round trip and that nothing
// is silently discarded, which is the opposite of crdt's: a merge loses no
// write either, but it resolves two into one and the loser is gone with no
// error to anybody. A ledger cannot be kept that way.
//
// WHAT MAKES THIS A PROOF AND NOT A SMOKE TEST is the second assertion. Every
// read below is a first try with no Eventually around it, taken through a node
// that did not take the write — which under crdt would race propagation and
// under raft reaches the leader behind a barrier. A crdt store cannot pass it.
//
// FOUR NODES AND A FLOOR OF THREE, so one node is deliberately NOT a voter and
// has to send the call to one that is. Three voters is also the smallest group
// that survives losing one, which is the last assertion and the only one that
// makes the word "raft" mean anything here.
func TestALedgerAnswersEveryNodeWithWhatItsLeaderCommitted(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	cloud := deployFleet(t, 4, "with/database/distributed/kv/raft", "ledger.example.com", kitchenSinkWASM(t))

	// THE FIRST WRITE IS A RETRY AND NOT A FAILURE, which is the whole of what
	// "the group is still forming" means: raft.New waits for peers before it
	// bootstraps, so a cloud whose ledger has never been written to has no
	// group yet and the first caller is the one that starts it.
	//
	// THE BUDGET IS THE FORMING ITSELF, PRICED — see formingBudget.
	var last string
	require.Eventually(t, func() bool {
		got := cloud[0].ask(t, http.MethodPost, "/kv-put/ledger/entry-1")
		last = fmt.Sprintf("%d %s", got.Code, got.Body.String())
		return got.Code == 204
	}, formingBudget, time.Second, "no node ever led the ledger's group: %s", &lastSaid{&last})

	// AND EVERY NODE ANSWERS WITH IT, FIRST TRY. This is the discriminator: a
	// store whose writes converge would race here, and one whose reads reach
	// the leader behind a barrier cannot.
	for i, at := range cloud {
		read := at.ask(t, http.MethodGet, "/kv-get/ledger/entry-1")
		require.Equal(t, 200, read.Code, "node %d refused a committed entry: %s", i, read.Body.String())
		require.Equal(t, "body", read.Body.String(), "node %d answered something else", i)
	}

	// EXACTLY THREE OF THE FOUR ARE IN THE GROUP, which is the floor the cloud
	// published rather than a number this test chose. A node holds a ledger's
	// engine only by being one of its voters; a node that routed to one holds
	// nothing, and that is how the fourth is found rather than assumed.
	var voters []int
	outsider := -1
	for i, at := range cloud {
		if at.node.Database().Held() > 0 {
			voters = append(voters, i)
			continue
		}
		outsider = i
	}
	require.Len(t, voters, 3, "the ledger's group is not the floor the cloud published")
	require.NotEqual(t, -1, outsider)

	// A WRITE THROUGH THE NODE PLACEMENT DID NOT CHOOSE. It may not open the
	// group — opening it would make it a voter, which is a quorum nobody placed
	// it in — so the call goes to a node that is one.
	require.Eventually(t, func() bool {
		return cloud[outsider].ask(t, http.MethodPost, "/kv-put/ledger/entry-2").Code == 204
	}, 60*time.Second, time.Second, "a write through a node outside the group was never answered")

	back := cloud[voters[0]].ask(t, http.MethodGet, "/kv-get/ledger/entry-2")
	require.Equal(t, 200, back.Code, "%s", back.Body.String())
	require.Equal(t, "body", back.Body.String())

	// A KEY NOBODY WROTE IS A MISS FROM EVERY NODE, never a 200 and never a
	// hang: a ledger's reader acts on "no such entry".
	for i, at := range cloud {
		missing := at.ask(t, http.MethodGet, "/kv-get/ledger/never-written")
		require.Equal(t, 404, missing.Code, "node %d", i)
	}

	// AND ONE OF THE THREE GOES AWAY. Two of three is still a quorum, so the
	// entries are still there and a new one still commits — which is the only
	// assertion here that makes the word "raft" mean anything: a single writer
	// would have taken the ledger with it.
	//
	// THE NODE IS CLOSED RATHER THAN KILLED THROUGH THE UNIVERSE, because a v2
	// node is not one of the legacy services dream's kill helper knows how to
	// take apart. Closing is what a lost machine does to the fleet anyway: the
	// group loses a voter, and the rest of the universe finds out the way it
	// would in production rather than by being told.
	require.NoError(t, cloud[voters[2]].node.Close())
	survivor := cloud[voters[0]]

	// AND THE BUDGET HERE IS AN ELECTION AND NOT A FORMING — see electionBudget.
	// Nothing bootstraps or joins: the two survivors already are the group.
	require.Eventually(t, func() bool {
		read := survivor.ask(t, http.MethodGet, "/kv-get/ledger/entry-1")
		return read.Code == 200 && read.Body.String() == "body"
	}, electionBudget, 2*time.Second, "the ledger stopped answering when one of its three voters was lost")

	require.Eventually(t, func() bool {
		return survivor.ask(t, http.MethodPost, "/kv-put/ledger/entry-3").Code == 204
	}, electionBudget, 2*time.Second, "the ledger stopped committing when one of its three voters was lost")

	read := survivor.ask(t, http.MethodGet, "/kv-get/ledger/entry-3")
	require.Equal(t, 200, read.Code, "%s", read.Body.String())
	require.Equal(t, "body", read.Body.String())
}

// keeper is the corpus's one function, addressed with the five fields the
// gateway would have filled in for it. The zone sits at the project root, so
// there is no scope — see route.go's address().
//
// THE PROJECT IS A PARAMETER because it is the only field that tells two
// corpus twins apart: both publish a function labelled `keeper`, in `prod`,
// reaching a database of the same name.
func keeper(project string) specs.Address {
	return specs.Address{Project: project, Environment: "prod", Kind: "function", Name: "keeper"}
}

// TestAKVDATABASEANSWERSFROMANODETHATNEVERTOOKTHEWRITE.
//
// `consensus: crdt` promises what the ledger above does not, so it has to be
// proven the opposite way round. Raft's discriminator is that every node
// answers FIRST TRY, because the read went to the leader behind a barrier.
// Crdt's is that a node which never ran the write answers EVENTUALLY, because
// the value travelled to it — so the Eventually below IS the assertion and not
// a guard around a flake. A store that was secretly node-local would never
// satisfy it however long it were given, which is the whole point of writing it
// this way: until now `consensus: crdt` was proven on one node, where a store
// that replicated nothing would have passed every test in the corpus.
//
// THE READ IS PINNED AND NOT ASKED OF THE GATEWAY, for the reason on() gives:
// a GET written over ask can be served by the very node that took the PUT, and
// usually is, because that node is the one already warm. Such a test cannot
// tell a replicated store from a node-local one, which is worse than no test.
//
// THREE NODES AND NO FLOOR TO BE OUTSIDE OF. A crdt database has no group and
// no leader: the node that is asked opens its own copy and answers from it,
// with nothing routed and nothing forwarded (services/database/service.go:
// 453-458). That is also how the reader is FOUND rather than assumed — after
// the write exactly one node has the database open, so the other two cannot
// have seen the value except across the swarm.
func TestAKvDatabaseAnswersFromANodeThatNeverTookTheWrite(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	cloud := deployFleet(t, 3, "with/database/distributed/kv/crdt", "keep.example.com", kitchenSinkWASM(t))

	// A CRDT WRITE IS 204 ON THE FIRST TRY, with no Eventually around it —
	// there is no group to form and nobody to elect, which is the cost the
	// ledger's first write pays and this one does not.
	stored, err := cloud[0].on(t.Context(), keeper("shop"), http.MethodPost, "/kv-put/sessions/token-1")
	require.NoError(t, err)
	require.Equal(t, 204, stored.Status, "the write was refused: %s", stored.Body)

	// AND EXACTLY ONE NODE HOLDS THE DATABASE: the one that ran it. So the
	// other two have never had this database open, and a read served by one of
	// them later cannot be the write coming back from where it landed.
	//
	// THIS IS NOT A RACE WITH THE MAINTENANCE SWEEP, which is the first thing
	// to suspect of a strict count taken against a live fleet. A NODE OPENS A
	// CRDT COPY ONLY BY BEING ASKED TO SERVE ONE: its own sweep reconsiders
	// what is already on its disk and nothing else (services/database/loop.go:
	// 87), and the repair sweep on the node that DOES hold it only asks — Hold
	// answers out of holdings.Held without opening anything
	// (services/database/rpc.go:277-293). Measured rather than argued: seventy
	// seconds past this line, with the writer reporting pending=1 so its repair
	// round had run and found the floor unmet, the node this test never invoked
	// still held nothing.
	var holders, untouched []int
	for i, at := range cloud {
		if at.node.Database().Held() > 0 {
			holders = append(holders, i)
			continue
		}
		untouched = append(untouched, i)
	}
	require.Equal(t, []int{0}, holders, "a crdt copy is opened by being asked, and one node was asked")
	require.Len(t, untouched, 2)

	// AND THE VALUE REACHES A NODE THAT NEVER TOOK IT. The reader opens its own
	// engine when this asks it to, which is AFTER the write was made, so it
	// joins the database's topic too late for the write's own broadcast: the
	// row can only arrive on a rebroadcast, and that is the path under test.
	//
	// SIXTY SECONDS BECAUSE ONE ROUND IS SIX AND A HALF. The store re-announces
	// its heads every five seconds (node/locate.go:28, reaching the datastore
	// at pkg/kvdb/service.go:120) randomized by ±30% (pkg/kvdb/crdt.go:664-671,
	// applied to the rebroadcast timer at :674), so a node that subscribed just
	// after a tick waits at most 6.5s for the next one, plus a gossipsub round
	// to graft it into the mesh and one delta fetched over the DAG. Sixty
	// seconds is nine of those rounds — headroom over a bound that is in the
	// tree, rather than a number raised until the test went green.
	reader := cloud[untouched[0]]
	var last string
	require.Eventually(t, func() bool {
		got, err := reader.on(t.Context(), keeper("shop"), http.MethodGet, "/kv-get/sessions/token-1")
		if err != nil {
			last = err.Error()
			return false
		}
		last = fmt.Sprintf("%d %s", got.Status, got.Body)
		return got.Status == 200 && string(got.Body) == "body"
	}, 60*time.Second, time.Second, "a value written on one node never reached another: %s", &lastSaid{&last})

	// AND IT ANSWERED OUT OF ITS OWN COPY, which is what closes the last way
	// this could pass without proving anything: Invoke runs the guest on this
	// node's instance pool and never hands it on (services/worker/service.go:
	// 177-210), so a reader holding the database is a reader that opened it —
	// the row is on its disk and did not arrive down a connection to the writer.
	require.Positive(t, reader.node.Database().Held(), "the reader answered without ever opening a copy")

	// A KEY NOBODY WROTE IS A MISS ON EVERY NODE, including the one the value
	// just converged on: a store that answered everything would satisfy the
	// assertion above without replicating anything at all.
	for i, at := range cloud {
		missing, err := at.on(t.Context(), keeper("shop"), http.MethodGet, "/kv-get/sessions/never-written")
		require.NoError(t, err)
		require.Equal(t, 404, missing.Status, "node %d answered a key nobody wrote: %s", i, missing.Body)
	}
}

// TestTwoProjectsNamingTheirKvTheSameThingNeverSeeEachOthersWrites.
//
// Two projects in one universe, each with a `sessions` it keeps things in. The
// database's id, its label, its environment and its model are THE SAME STRING
// on both sides — crdt-b is crdt's twin — so the only thing between the two
// stores is the project segment of Spec.Prefix (pkg/database/database.go:443).
//
// THAT ONE SEGMENT REACHES TWO PLACES AND BOTH ARE LOAD-BEARING. Prefix names
// the STORE — database.Name turns it into a path (node/database/database.go) and
// pkg/kvdb turns that one variable into both the on-disk prefix and the gossip
// topic (pkg/kvdb/service.go:136 and :107) — and Prefix is ALSO what the kv
// engine puts in front of every key it writes (pkg/database/engines/kv/kv.go:91
// and :105). Drop it and the two projects are one store, on one topic, writing
// one key: each one's `sessions` is the other's, merged by a resolver doing
// exactly what it was asked to do, with no error to either side and both
// reporting success.
//
// THE ROWS DO NOT COVER THE TOPIC, WHICH IS WHY THE TOPICS ARE COUNTED AT THE
// END. Measured: with the project dropped from pkg/kvdb's topic alone, while
// the path it is derived from keeps it, every row assertion below stays green,
// because the key prefix still tells the two projects' entries apart. So the
// rows catch the segment falling out of Prefix, the topic count catches it
// falling out of the topic, and neither says what the other says.
//
// IT IS STILL A FLEET, and not because one node would pass: one node shares the
// store cache (node/database/holdings.go) and the key prefix both, so a
// single-node version of this fails too. It is a fleet because a topic nobody
// gossips over is a name and not a channel — the value has to TRAVEL for the
// topic to be a thing at all, and the negative has to be asked of a node that
// took neither write.
//
// AND THE NEGATIVE IS NEVER AN Eventually. "market never sees shop's value" is
// a claim about a window, and an Eventually around it would only ever say the
// window was short. So it is asked of the node where shop's OTHER row has
// demonstrably arrived: a row that crossed the swarm and landed in this node's
// store has had every chance a leak would need, down the same topic and into
// the same datastore. The window is measured rather than assumed.
func TestTwoProjectsNamingTheirKvTheSameThingNeverSeeEachOthersWrites(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	u := deployFleetOf(t, 3, kitchenSinkWASM(t), nil,
		deployed{project: "shop", name: "with/database/distributed/kv/crdt", host: "keep.example.com"},
		deployed{project: "market", name: "with/database/distributed/kv/crdt-b", host: "keepb.example.com"})
	shop, market := u.fleets[0], u.fleets[1]

	// PINNED AND NOT ASKED OF THE GATEWAY, for the reason on() gives: placement
	// is free to serve a request from whichever node is warm, and this test is
	// about WHICH node holds what.
	put := func(through *running, project, key, value string) {
		t.Helper()
		got, err := through.onCarrying(t.Context(), keeper(project), http.MethodPost, "/kv-put/sessions/"+key, value)
		require.NoError(t, err)
		require.Equal(t, 204, got.Status, "%s could not write %s: %s", project, key, got.Body)
	}
	read := func(through *running, project, key string) (*invoke.Response, error) {
		return through.on(t.Context(), keeper(project), http.MethodGet, "/kv-get/sessions/"+key)
	}

	// EACH PROJECT WRITES ON A DIFFERENT NODE AND NODE 2 WRITES NOTHING, so
	// everything node 2 answers below arrived across the swarm.
	//
	// THE PRIVATE KEY GOES IN FIRST, and that order is load-bearing. It lands in
	// the same store as token-1 and is carried in the same heads, so a node
	// holding token-1 has held only-<project> for at least as long — which is
	// what makes the miss asked for at the end a fact about this node's disk
	// rather than a guess about timing.
	put(shop[0], "shop", "only-shop", "shop-kept")
	put(shop[0], "shop", "token-1", "shop-kept")
	put(market[1], "market", "only-market", "market-kept")
	put(market[1], "market", "token-1", "market-kept")

	// AND BOTH REACH THE NODE THAT TOOK NEITHER WRITE. This Eventually IS an
	// assertion and not a guard: node 2 opens its copy when this read asks it
	// to, which is after both writes, so it joins the topic too late for their
	// own broadcasts and the rows can only arrive on a rebroadcast.
	//
	// SIXTY SECONDS BECAUSE ONE ROUND IS SIX AND A HALF. Heads are re-announced
	// every five seconds (node/locate.go:28) randomized by ±30%
	// (pkg/kvdb/crdt.go:664-671), so a node that subscribed just after a tick
	// waits at most 6.5s for the next, plus a gossipsub round to graft it into
	// the mesh and one delta fetched over the DAG. Sixty is nine of those —
	// headroom over a bound that is in the tree, not a number raised until this
	// went green.
	converged := func(fleet []*running, project, value string) {
		t.Helper()
		var last string
		require.Eventually(t, func() bool {
			for _, key := range []string{"token-1", "only-" + project} {
				got, err := read(fleet[2], project, key)
				if err != nil {
					last = err.Error()
					return false
				}
				last = fmt.Sprintf("%s %d %s", key, got.Status, got.Body)
				if got.Status != 200 || string(got.Body) != value {
					return false
				}
			}
			return true
		}, 60*time.Second, time.Second, "%s's kv never reached a node that never took its write: %s", project, &lastSaid{&last})
	}
	converged(shop, "shop", "shop-kept")
	converged(market, "market", "market-kept")

	// AND EACH STILL ANSWERS ITS OWN VALUE, ASKED AGAIN NOW THAT BOTH ARE LIVE
	// ON THIS ONE NODE. Re-asking is not redundant: shop's Eventually above
	// would have passed either way, because under a single shared store
	// market's token-1 only lands on top of shop's afterwards. Two different
	// values for one key out of one node is the thing one store cannot do.
	for _, at := range []struct {
		fleet   []*running
		project string
		value   string
	}{{shop, "shop", "shop-kept"}, {market, "market", "market-kept"}} {
		got, err := read(at.fleet[2], at.project, "token-1")
		require.NoError(t, err)
		require.Equal(t, 200, got.Status, "%s lost its own key: %s", at.project, got.Body)
		require.Equal(t, at.value, string(got.Body), "%s was answered the other project's value", at.project)
	}

	// AND NEITHER SEES THE KEY THE OTHER KEPT TO ITSELF. The row IS on this
	// node — it arrived with the token-1 the Eventually waited for — so the
	// only question left is whether the other project's store is the same
	// store, which needs no waiting to answer.
	missed, err := read(market[2], "market", "only-shop")
	require.NoError(t, err)
	require.Equal(t, 404, missed.Status, "market read a key only shop ever wrote: %s", missed.Body)

	missed, err = read(shop[2], "shop", "only-market")
	require.NoError(t, err)
	require.Equal(t, 404, missed.Status, "shop read a key only market ever wrote: %s", missed.Body)

	// AND THE NODE THAT ANSWERED BOTH HAS TWO ENGINES OPEN, NOT ONE. Held is a
	// count over everything a node holds (services/database/service.go:410),
	// and this universe contains exactly two databases — so the number is the
	// question rather than a proxy for it: one store serving both projects
	// reads as 1. shop[2] and market[2] are the same node wearing two doors.
	require.Equal(t, 2, shop[2].node.Database().Held(), "one node answered two projects out of one engine")

	// AND THE TWO STORES ARE TWO TOPICS ON THE WIRE. This is the one assertion
	// here the rows do not make: they stay apart on the key prefix alone, so a
	// topic that had lost the project would carry both projects' heads and
	// nothing above would say so. A store joins its topic when it opens
	// (pkg/kvdb/service.go:107), so the set of them across the fleet IS the set
	// of stores this universe opened.
	//
	// THE NAMES ARE SPELLED OUT AND NOT DERIVED, because they are the thing
	// under test — a derivation sharing an expression with the code would agree
	// with it however it changed. The database id is the corpus's own and is
	// IDENTICAL in both projects (projects/…/kv/crdt{,-b}/database/sessions.yaml),
	// which is what leaves the project as the only separator.
	const sessions = "QmVLDAzEjEbfBPuoyLPWnPMhKp8ceDXwGdmMKY5W3nZWfG"
	topics := map[string]bool{
		"databases/shop/prod/kv/" + sessions + "/broadcast":   false,
		"databases/market/prod/kv/" + sessions + "/broadcast": false,
	}
	for _, n := range u.dream.Peers() {
		for _, topic := range n.Messaging().GetTopics() {
			if !strings.HasPrefix(topic, "databases/") {
				continue
			}
			_, named := topics[topic]
			require.True(t, named, "a store topic no database in this universe names: %s", topic)
			topics[topic] = true
		}
	}
	for topic, joined := range topics {
		require.True(t, joined, "no node in the fleet ever joined %s", topic)
	}
}

// TestTwoProjectsNamingTheirLedgerTheSameThingAreTwoGroups.
//
// The same claim as the crdt twin above, aimed at the mechanism raft uses
// instead of a topic. A ledger's group is named by ledger.Namespace, which is
// ledger.Domain joined to the same Spec.Prefix (node/ledger/ledger.go), and
// pkg/raft makes that name the libp2p protocol id its voters dial each other on
// (pkg/raft/stream.go:65-71). Drop the project segment and the two projects
// derive ONE group: they join it, and they elect one leader between them.
//
// A SHARED GROUP DOES NOT LEAK ROWS, and that had to be measured rather than
// assumed. With the project dropped from ledger.Namespace alone, both projects
// commit into one log and every row assertion below still passes: the kv engine
// prefixes every key with Spec.Prefix (pkg/database/engines/kv/kv.go:91 and
// :105) whatever log carried it, so the entries stay apart inside the one log.
// What a shared group costs is the isolation `consensus: raft` was asked for —
// one leader ordering two projects' commits, so one project's latency and one
// project's loss of quorum become the other's, and placement, which opens a
// group only for the ledgers a node is a voter for, decides nothing.
//
// SO THE NAMESPACE ASSERTION IS THE ONE THAT COVERS THE GROUP, which is the
// reverse of what it looks like. It is not the rows restated one layer down; it
// is the only assertion here that a shared group fails. The rows are still
// worth asserting — they are what catches the segment falling out of Prefix
// itself, the one place it enters the group name and the key prefix both — but
// they are the weaker of the two.
//
// ONE KEY, TWO VALUES. entry-1 is written once by each project with a different
// value, so a node answering out of the wrong place is caught by the string it
// returns and not only by the status.
//
// THE NEGATIVE NEEDS NO WINDOW HERE, which is the one way raft is easier to
// state than crdt: a read is answered by the group's leader behind a barrier,
// so it sees everything that log has committed and there is nothing left to
// wait for. So the miss is asserted directly, with no Eventually anywhere near
// it.
func TestTwoProjectsNamingTheirLedgerTheSameThingAreTwoGroups(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	u := deployFleetOf(t, 3, kitchenSinkWASM(t), nil,
		deployed{project: "shop", name: "with/database/distributed/kv/raft", host: "ledger.example.com"},
		deployed{project: "market", name: "with/database/distributed/kv/raft-b", host: "ledgerb.example.com"})
	shop, market := u.fleets[0], u.fleets[1]

	put := func(through *running, project, key, value string) (*invoke.Response, error) {
		return through.onCarrying(t.Context(), keeper(project), http.MethodPost, "/kv-put/ledger/"+key, value)
	}
	read := func(through *running, project, key string) (*invoke.Response, error) {
		return through.on(t.Context(), keeper(project), http.MethodGet, "/kv-get/ledger/"+key)
	}

	// THE FIRST WRITE TO EACH LEDGER IS A RETRY AND NOT A FAILURE, for the
	// reason the single-project ledger test gives: raft.New waits for peers
	// before it bootstraps, so a ledger nobody has written to has no group yet
	// and the first caller is the one that starts it. TWO ledgers means TWO
	// groups to form, and a fleet that could only ever form one is the first
	// thing this would notice.
	commit := func(fleet []*running, project, key, value string) {
		t.Helper()
		var last string
		require.Eventually(t, func() bool {
			got, err := put(fleet[0], project, key, value)
			if err != nil {
				last = err.Error()
				return false
			}
			last = fmt.Sprintf("%d %s", got.Status, got.Body)
			return got.Status == 204
		}, formingBudget, time.Second, "no node ever led %s's ledger: %s", project, &lastSaid{&last})
	}
	commit(shop, "shop", "entry-1", "shop-kept")
	commit(market, "market", "entry-1", "market-kept")

	// A SECOND ENTRY, FIRST TRY AND WITH NOTHING AROUND IT. The group that just
	// committed has a leader, so the one thing the Eventually above was waiting
	// for has already happened. This entry is shop's alone and is what market
	// is asked about at the end.
	only, err := put(shop[0], "shop", "only-shop", "shop-kept")
	require.NoError(t, err)
	require.Equal(t, 204, only.Status, "shop's group stopped committing: %s", only.Body)

	// AND EVERY NODE ANSWERS EACH PROJECT WITH ITS OWN ENTRY, FIRST TRY. The
	// reads are pinned rather than routed, so this is three nodes each asked
	// twice and not six requests placed wherever the gateway liked.
	for i := range shop {
		kept, err := read(shop[i], "shop", "entry-1")
		require.NoError(t, err)
		require.Equal(t, 200, kept.Status, "node %d refused shop's entry: %s", i, kept.Body)
		require.Equal(t, "shop-kept", string(kept.Body), "node %d answered shop with something else", i)

		other, err := read(market[i], "market", "entry-1")
		require.NoError(t, err)
		require.Equal(t, 200, other.Status, "node %d refused market's entry: %s", i, other.Body)
		require.Equal(t, "market-kept", string(other.Body), "node %d answered market with something else", i)
	}

	// AND AN ENTRY ONLY SHOP COMMITTED IS A MISS FOR MARKET, on every node.
	for i := range market {
		missing, err := read(market[i], "market", "only-shop")
		require.NoError(t, err)
		require.Equal(t, 404, missing.Status, "node %d let market read an entry shop committed: %s", i, missing.Body)
	}

	// AND THE TWO GROUPS ARE TWO NAMES ON THE WIRE — the assertion the rows do
	// not make, for the reason given above. Every voter registers a stream
	// handler per group it opened, under the protocol id pkg/raft derives from
	// the namespace, so the set of those ids across the fleet IS the set of
	// groups this universe formed.
	//
	// THE NAMES ARE SPELLED OUT AND NOT DERIVED, because they are the thing
	// under test — a derivation sharing an expression with the code would agree
	// with it however it changed. The database id is the corpus's own, and it
	// is IDENTICAL in both projects (projects/…/kv/raft{,-b}/database/ledger.yaml),
	// which is what leaves the project as the only separator.
	const ledger = "QmVYRA67L6sJ1c8bZTwcs7UBvFdYesu414kPa2V9whE42e"
	const domain = "/tau/databases/v1"
	groups := map[string]bool{
		domain + "/shop/prod/kv/" + ledger:   false,
		domain + "/market/prod/kv/" + ledger: false,
	}
	for _, peer := range u.dream.Peers() {
		for _, id := range peer.Peer().Mux().Protocols() {
			namespace, isRaft := strings.CutPrefix(string(id), raft.ProtocolRaftPrefix)
			if !isRaft || !strings.HasPrefix(namespace, domain+"/") {
				continue
			}
			// A GROUP ANSWERS ON TWO IDS — its command service and its
			// transport (pkg/raft/stream.go:65-71) — and both name the same
			// namespace.
			namespace = strings.TrimSuffix(namespace, "/transport")
			_, named := groups[namespace]
			require.True(t, named, "a raft group no database in this universe names: %s", namespace)
			groups[namespace] = true
		}
	}
	for namespace, formed := range groups {
		require.True(t, formed, "no node in the fleet ever opened %s", namespace)
	}
}

// deployFleet is deploy over a universe of several nodes in one region, with a
// replication floor the cloud has published.
//
// THE FLOOR IS PUBLISHED AND NOT CONFIGURED, because it is the one placement
// input that does not heal: two nodes reading it from their own config disagree
// about who should hold a database forever. Three is what makes a group that
// survives losing a node.
func deployFleet(t *testing.T, copies int, name, host string, wasm []byte) []*running {
	t.Helper()
	return deployFleetWith(t, copies, name, host, wasm, nil)
}

// deployFleetWith is deployFleet for a project that needs more than one binary,
// keyed by the label the author wrote — see the artifacts map.
func deployFleetWith(t *testing.T, copies int, name, host string, wasm []byte, extra map[string][]byte) []*running {
	t.Helper()
	return deployFleetOf(t, copies, wasm, extra,
		deployed{project: "shop", name: name, host: host}).fleets[0]
}

// universe is what deployFleetOf leaves running: dream's own handle on the
// fleet, and one view of that fleet per project published into it.
type universe struct {
	dream  *nodeDream.Universe
	fleets [][]*running
}

// deployFleetOf is deployFleet over SEVERAL PROJECTS in one universe.
//
// NOTHING BELOW THE HELPERS WAS EVER SINGLE-PROJECT — see publish — so all this
// adds is a publish per project and a settle barrier that waits for every one
// of them. The universe, the region, the fleet document and the component are
// shared exactly as a real cloud shares them between its tenants.
func deployFleetOf(t *testing.T, copies int, wasm []byte, extra map[string][]byte, projects ...deployed) universe {
	t.Helper()
	require.NotEmpty(t, projects, "a universe with nothing published in it proves nothing")

	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services: []string{
			node.Register, node.Worker, node.Stats, node.Gateway,
			node.Database, node.Primitives},
		// ONE REGION, NAMED, because the project pins its state to it and a
		// node that cannot say where it is competes for nothing anybody
		// pinned.
		Regions: []string{"eu"},
		Copies:  copies,
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	pids := u.Pids()
	require.Len(t, pids, copies)

	nodes := make([]*node.Node, 0, copies)
	for _, id := range pids {
		nodes = append(nodes, nodeOf(t, id))
	}

	document, err := fleet.Encode(fleet.Fleet{Replication: 3})
	require.NoError(t, err)
	_, err = nodes[0].Register().Local().PublishFleet(t.Context(), 1, document)
	require.NoError(t, err)

	store, ok := nodeDream.Assets(pids[0])
	require.True(t, ok)
	cid, err := store.Put(t.Context(), bytes.NewReader(wasm))
	require.NoError(t, err)

	// EVERY EXTRA BINARY IS STORED THE SAME WAY, keyed by the label the author
	// wrote. A project whose functions are built against different worlds needs
	// more than one, and `""` stays what everything unnamed gets.
	held := artifacts{"": cid}
	for label, code := range extra {
		id, err := store.Put(t.Context(), bytes.NewReader(code))
		require.NoError(t, err)
		held[label] = id
	}

	fleets := make([][]*running, 0, len(projects))
	for _, at := range projects {
		fleets = append(fleets, publish(t, pids, at, held))
	}

	// EVERY NODE HAS TO HAVE EVERY PROJECT, THE FLOOR AND THE FLEET before
	// anything is asked of the ledger. A node that cannot see the other three
	// computes a placement over itself alone and opens a group nobody placed it
	// in — so without this the fleet under test is whatever had gossiped by the
	// time the first request arrived, and the node that is supposed to be
	// OUTSIDE the group might be in it.
	//
	// AND IT SAYS WHICH OF THE THREE IT GAVE UP ON. They are three different
	// mechanisms — two CRDTs and a presence window — and a message that blamed
	// the project every time sent an investigation after the wrong one for a
	// whole workstream.
	var waiting string
	require.Eventually(t, func() bool {
		for _, n := range nodes {
			for _, at := range projects {
				if _, err := n.Register().Local().Resolve(t.Context(), at.project, "prod"); err != nil {
					waiting = "the project " + at.project
					return false
				}
			}
			if _, err := n.Register().Local().ResolveFleet(t.Context()); err != nil {
				waiting = "the fleet document"
				return false
			}
			if len(n.Stats().Placement()["eu"]) != copies {
				waiting = "the placement set"
				return false
			}
		}
		return true
	}, 120*time.Second, time.Second, "a node never reached %s", &lastSaid{&waiting})

	return universe{dream: u, fleets: fleets}
}

// projectDir is where a named project lives: under projects/, or — for a test
// that rewrote a copy, such as one pointing a database at a server it started —
// at the absolute path it was given.
func projectDir(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join("projects", name)
}
