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

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	"github.com/taubyte/tau/pkg/fleet"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/raft"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// Authored projects, compiled by tcc, run in a universe: from yaml through publish
// to bytes on a socket, rather than hand-written compiled objects that stay green
// when the compiler changes.
//
// A project's path under projects/ mirrors the vocabulary's axes:
//
//	with/function/wasm                     one function, one route, nothing else
//	with/function/js                       …the same, on the js tier
//	with/function/runtimes                 …one host, a function on each tier
//	with/component                         …whose source is a component, not a path
//	with/database/distributed/kv/crdt      …and a kv, tau's engine, merge resolution
//	with/database/distributed/kv/raft      …and a ledger, where writes are ordered
//	with/database/postgres/sql             …and a database behind a connector
//	with/sync/queue                        …and a queue
//
// One project per cell of the database grid. A `-b` directory is its sibling's twin
// (same database id, label, environment and model) under another project and host,
// so isolation can be tested; its project id is a real CIDv0 of its name.
// with/function/wasm is the smallest project and the first to read when the corpus
// turns red. Sources nobody built are pointed at shipped components through the
// artifact seam (nodeDream.Built), not by rewriting `source`.

// artifacts maps a workload's label to the build its package is, "" being the default;
// a map since the authorizer project needs a second world.
type artifacts map[string]string

// of is the component this resource runs, which is its own entry when it has one.
func (a artifacts) of(label string) string {
	if cid, named := a[label]; named {
		return cid
	}
	return a[""]
}

// project compiles a project under projects/ and returns the published bytes, the
// object they came from (for idOf), and the mapping the artifact seam is handed.
// The compiled document is not touched.
func project(t *testing.T, name string, components artifacts) ([]byte, map[string]any, map[string]string) {
	t.Helper()
	return projectAs(t, name, components, schema.WithNoRegions())
}

// projectAs is project placed by the Regions given: WithNoRegions for the corpus,
// or WithRegion("region-1") for one region of a larger cloud.
func projectAs(t *testing.T, name string, components artifacts, regions schema.Regions) ([]byte, map[string]any, map[string]string) {
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

	// Placement is the platform's answer; a corpus project writes no `regions`.
	for _, f := range schema.Place(flat, schema.WithRegions(regions)) {
		require.NotEqual(t, schema.Broken, f.Severity, "%s: %s", name, f)
	}

	// WHAT RUNS IS A PACKAGE, staged under the package's id, the one the seam is asked
	// about; the workload naming it is what the author's label says to run.
	packages, _ := root["package"].(map[string]any)
	pointed := map[string]string{}
	runs := 0
	for _, group := range root {
		instances, ok := group.(map[string]any)
		if !ok {
			continue
		}
		for _, v := range instances {
			res, ok := v.(map[string]any)
			if !ok {
				continue
			}
			ref, ok := res["package"].(map[string]any)
			if !ok {
				continue
			}
			pkgID, _ := ref["name"].(string)
			pkg, _ := packages[pkgID].(map[string]any)
			// An image is its own artifact, read straight off the package, and a builtin
			// ships with the runtime, so nothing is staged for either.
			if src, ok := pkg["source"].(map[string]any); ok && (src["kind"] == "image" || src["kind"] == "builtin") {
				runs++
				continue
			}
			label, _ := res["name"].(string)
			cid := components.of(label)
			require.NotEmpty(t, cid, "%s names %q and nothing was handed in for it", name, label)
			if held, staged := pointed[pkgID]; staged {
				require.Equal(t, held, cid, "%s: two workloads run one package and were handed different builds", name)
			}
			pointed[pkgID] = cid
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
	node *instance.Instance
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

// askCarrying is ask with the body named, so two projects on one key write
// different values and shared storage would show.
func (r *running) askCarrying(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, "http://"+r.host+path, bytes.NewReader([]byte(body)))
	req.Host = r.host
	w := httptest.NewRecorder()
	r.node.Handler().ServeHTTP(w, req)
	return w
}

// on puts the request to the named node's worker, which ask cannot: a gateway
// picks a route and placement picks the node.
func (r *running) on(ctx context.Context, at specs.Address, method, path string) (*invoke.Response, error) {
	return r.onCarrying(ctx, at, method, path, "body")
}

// onCarrying is on with the body named, for askCarrying's reason.
func (r *running) onCarrying(ctx context.Context, at specs.Address, method, path, body string) (*invoke.Response, error) {
	return r.node.Worker().Invoke(ctx, at, &invoke.Request{
		Method: method, Path: path, Body: []byte(body),
	})
}

// idOf is what a label compiled to, read from the published object rather than
// restated, since a stale id would read like an unready cloud.
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

// deployed names one project for a universe: the register's project (the isolation
// boundary every key is built from, unlike project.yaml's `id:`), the corpus
// directory, and the host, which two projects cannot share.
type deployed struct {
	project string
	name    string
	host    string

	// regions places it; nil is WithNoRegions, the corpus's one region.
	regions schema.Regions
}

// publish puts one project into a running universe and answers a door onto it
// through each node named. It takes peer ids because every node's artifact seam
// gets what a build produced: placement chooses which node runs a workload.
func publish(t *testing.T, pids []string, at deployed, components artifacts) []*running {
	t.Helper()

	regions := at.regions
	if regions == nil {
		regions = schema.WithNoRegions()
	}
	compiled, obj, pointed := projectAs(t, at.name, components, regions)
	stagedOn(t, pids, at.project, pointed)

	nodes := make([]*instance.Instance, 0, len(pids))
	for _, id := range pids {
		nodes = append(nodes, nodeOf(t, id))
	}

	// Cut as a deploy cuts it, one view per region; publishing uncut would let a node
	// serve what is placed elsewhere.
	var flat map[string]any
	require.NoError(t, json.Unmarshal(compiled, &flat))
	_, err := nodes[0].Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: at.project, Environment: "prod", Revision: 1,
		Compiled: compiled, Hosts: []string{at.host}, Views: cutFor(t, flat, regions),
	})
	require.NoError(t, err)

	out := make([]*running, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, &running{node: n, host: at.host, object: obj})
	}
	return out
}

// cutFor is the views a deploy publishes for a compiled project, one per region.
func cutFor(t *testing.T, flat map[string]any, regions schema.Regions) []pkgregister.View {
	t.Helper()
	views, _, err := schema.Views(flat, schema.WithRegions(regions))
	require.NoError(t, err)
	out := make([]pkgregister.View, 0, len(views))
	for region, v := range views {
		b, err := json.Marshal(v.Object)
		require.NoError(t, err)
		out = append(out, pkgregister.View{Destination: region, Compiled: b})
	}
	return out
}

// deploy starts a universe, publishes the named project, and answers a door onto
// its one node. Nothing waits for the project to be in use: a node resolves it on
// first ask, so a test's first assertion is the one that retries.
func deploy(t *testing.T, name, host string, wasm []byte) *running {
	t.Helper()
	return deployWith(t, name, host, wasm, nil)
}

// deployWith is deploy for a project needing more than one binary (see artifacts).
func deployWith(t *testing.T, name, host string, wasm []byte, extra map[string][]byte) *running {
	t.Helper()
	return deployServing(t, name, host, wasm, extra,
		instance.Register, instance.Worker, instance.Discovery, instance.Gateway, instance.Database, instance.Primitives)
}

// deployServing is deployWith over a chosen set of services: a zone answered by a
// function is reached over UDP and needs no gateway.
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

func TestTheSmallestProjectAnswers(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	r := deploy(t, "with/function/wasm", "hello.example.com", handlerWASM(t))

	got := r.ask(t, http.MethodGet, "/")
	require.Equal(t, 200, got.Code, "a GET reached the function")
	require.Equal(t, "/", got.Header().Get("x-path"), "and the path it was asked for came back")

	posted := r.ask(t, http.MethodPost, "/orders")
	require.Equal(t, 201, posted.Code, "and a POST is a different answer from the same component")
	require.Equal(t, "/orders", posted.Header().Get("x-path"))
}

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

func TestAProjectWithAKvDatabaseKeepsSomething(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	r := deploy(t, "with/database/distributed/kv/crdt", "keep.example.com", kitchenSinkWASM(t))

	// A write is 204 and a refusal 403, so the status names the arm that answered.
	stored := r.ask(t, http.MethodPost, "/kv-put/sessions/token-1")
	require.Equal(t, 204, stored.Code, "the write was refused: %s", stored.Body.String())

	read := r.ask(t, http.MethodGet, "/kv-get/sessions/token-1")
	require.Equal(t, 200, read.Code, "the read was refused: %s", read.Body.String())
	require.Equal(t, "body", read.Body.String(), "what came back is not what went in")

	// A key nobody wrote is a miss, not an empty success.
	missing := r.ask(t, http.MethodGet, "/kv-get/sessions/never-written")
	require.Equal(t, 404, missing.Code)
	require.NotEqual(t, "body", missing.Body.String())

}

func TestALedgerAnswersEveryNodeWithWhatItsLeaderCommitted(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	cloud := deployFleet(t, 4, "with/database/distributed/kv/raft", "ledger.example.com", kitchenSinkWASM(t))

	// The first write is retried: raft.New waits for peers before bootstrapping, so
	// the first caller starts the group. Budget: formingBudget.
	var last string
	require.Eventually(t, func() bool {
		got := cloud[0].ask(t, http.MethodPost, "/kv-put/ledger/entry-1")
		last = fmt.Sprintf("%d %s", got.Code, got.Body.String())
		return got.Code == 204
	}, formingBudget, time.Second, "no node ever led the ledger's group: %s", &lastSaid{&last})

	// Every node answers with it, first try: a converging store would race here; reads
	// reaching the leader behind a barrier cannot.
	for i, at := range cloud {
		read := at.ask(t, http.MethodGet, "/kv-get/ledger/entry-1")
		require.Equal(t, 200, read.Code, "node %d refused a committed entry: %s", i, read.Body.String())
		require.Equal(t, "body", read.Body.String(), "node %d answered something else", i)
	}

	// Exactly three of the four are in the group, the published floor. A node holds a
	// ledger's engine only as a voter, which is how the fourth is found.
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

	// A write through the node placement did not choose. It must not open the group
	// (that would make it an unplaced voter), so the call goes to a voter.
	require.Eventually(t, func() bool {
		return cloud[outsider].ask(t, http.MethodPost, "/kv-put/ledger/entry-2").Code == 204
	}, 60*time.Second, time.Second, "a write through a node outside the group was never answered")

	back := cloud[voters[0]].ask(t, http.MethodGet, "/kv-get/ledger/entry-2")
	require.Equal(t, 200, back.Code, "%s", back.Body.String())
	require.Equal(t, "body", back.Body.String())

	// A key nobody wrote is a miss from every node, never a 200 or a hang.
	for i, at := range cloud {
		missing := at.ask(t, http.MethodGet, "/kv-get/ledger/never-written")
		require.Equal(t, 404, missing.Code, "node %d", i)
	}

	// One of the three goes away. Two of three is a quorum, so entries remain and a new
	// one commits; a single writer would have taken the ledger with it. Closing the
	// node is what a lost machine does to the fleet.
	require.NoError(t, cloud[voters[2]].node.Close())
	survivor := cloud[voters[0]]

	// Budget: electionBudget. Nothing bootstraps: the two survivors are the group.
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

// keeper is the corpus's one function, addressed with the fields the gateway fills
// in. The zone is at the project root, so no scope (route.go's address()). The
// project is the only field telling two corpus twins apart.
func keeper(project string) specs.Address {
	return specs.Address{Project: project, Environment: "prod", Kind: "function", Name: "keeper"}
}

func TestAKvDatabaseAnswersFromANodeThatNeverTookTheWrite(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	cloud := deployFleet(t, 3, "with/database/distributed/kv/crdt", "keep.example.com", kitchenSinkWASM(t))

	// A CRDT write is 204 on the first try: no group to form.
	stored, err := cloud[0].on(t.Context(), keeper("shop"), http.MethodPost, "/kv-put/sessions/token-1")
	require.NoError(t, err)
	require.Equal(t, 204, stored.Status, "the write was refused: %s", stored.Body)

	// Exactly one node holds the database: the one that ran it, so a later read from
	// another cannot be the write coming back. Not a race with the maintenance sweep: a
	// node opens a CRDT copy only when asked to serve one (services/database/loop.go),
	// and Hold answers from holdings without opening (services/database/rpc.go).
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

	// The value reaches a node that never took it. The reader opens its engine after
	// the write, too late for its broadcast, so the row arrives on a rebroadcast. Heads
	// are re-announced every 5s ±30% (instance/locate.go, pkg/kvdb/crdt.go): at most 6.5s
	// plus a gossipsub graft and one DAG fetch. 60s is nine rounds.
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

	// It answered from its own copy: Invoke runs the guest on this node's pool and
	// never hands it on (services/worker/service.go), so holding the database means it
	// opened it.
	require.Positive(t, reader.node.Database().Held(), "the reader answered without ever opening a copy")

	// A key nobody wrote is a miss on every node, or a store answering everything would
	// pass the assertion above.
	for i, at := range cloud {
		missing, err := at.on(t.Context(), keeper("shop"), http.MethodGet, "/kv-get/sessions/never-written")
		require.NoError(t, err)
		require.Equal(t, 404, missing.Status, "node %d answered a key nobody wrote: %s", i, missing.Body)
	}
}

func TestTwoProjectsNamingTheirKvTheSameThingNeverSeeEachOthersWrites(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	u := deployFleetOf(t, 3, kitchenSinkWASM(t), nil,
		deployed{project: "shop", name: "with/database/distributed/kv/crdt", host: "keep.example.com"},
		deployed{project: "market", name: "with/database/distributed/kv/crdt-b", host: "keepb.example.com"})
	shop, market := u.fleets[0], u.fleets[1]

	// Pinned, not routed through the gateway: this test is about which node holds what.
	put := func(through *running, project, key, value string) {
		t.Helper()
		got, err := through.onCarrying(t.Context(), keeper(project), http.MethodPost, "/kv-put/sessions/"+key, value)
		require.NoError(t, err)
		require.Equal(t, 204, got.Status, "%s could not write %s: %s", project, key, got.Body)
	}
	read := func(through *running, project, key string) (*invoke.Response, error) {
		return through.on(t.Context(), keeper(project), http.MethodGet, "/kv-get/sessions/"+key)
	}

	// Each project writes on a different node and node 2 writes nothing, so whatever
	// node 2 answers crossed the swarm. The private key goes first: it rides the same
	// heads as token-1, so a node holding token-1 holds only-<project> too, making the
	// final miss a fact rather than timing.
	put(shop[0], "shop", "only-shop", "shop-kept")
	put(shop[0], "shop", "token-1", "shop-kept")
	put(market[1], "market", "only-market", "market-kept")
	put(market[1], "market", "token-1", "market-kept")

	// Both reach the node that took neither write, on a rebroadcast since node 2 opens
	// its copy after both writes. 60s is nine 6.5s rebroadcast rounds (instance/locate.go,
	// pkg/kvdb/crdt.go).
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

	// Each still answers its own value, asked again now both are live on this node: two
	// values for one key from one node is what a single store cannot do.
	type testCase struct {
		fleet   []*running
		project string
		value   string
	}
	for _, at := range []testCase{{shop, "shop", "shop-kept"}, {market, "market", "market-kept"}} {
		got, err := read(at.fleet[2], at.project, "token-1")
		require.NoError(t, err)
		require.Equal(t, 200, got.Status, "%s lost its own key: %s", at.project, got.Body)
		require.Equal(t, at.value, string(got.Body), "%s was answered the other project's value", at.project)
	}

	// Neither sees the key the other kept. The row is on this node (it came with
	// token-1), so no wait is needed.
	missed, err := read(market[2], "market", "only-shop")
	require.NoError(t, err)
	require.Equal(t, 404, missed.Status, "market read a key only shop ever wrote: %s", missed.Body)

	missed, err = read(shop[2], "shop", "only-market")
	require.NoError(t, err)
	require.Equal(t, 404, missed.Status, "shop read a key only market ever wrote: %s", missed.Body)

	// The node that answered both has two engines open. Held counts everything a node
	// holds and this universe has exactly two databases; one shared store reads as 1.
	require.Equal(t, 2, shop[2].node.Database().Held(), "one node answered two projects out of one engine")

	// The two stores are two topics on the wire, which the rows alone cannot show. A
	// store joins its topic when it opens (pkg/kvdb/service.go), so the fleet's topics
	// are the stores opened. Names are spelled out, not derived, so they cannot agree
	// with the code by construction; the database id is identical in both projects,
	// leaving the project as the only separator.
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

	// The first write to each ledger is retried, as in the single-project test. Two
	// ledgers mean two groups to form.
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

	// A second entry, first try: the group now has a leader. Only shop has it; market is
	// asked about it below.
	only, err := put(shop[0], "shop", "only-shop", "shop-kept")
	require.NoError(t, err)
	require.Equal(t, 204, only.Status, "shop's group stopped committing: %s", only.Body)

	// Every node answers each project with its own entry, first try, pinned not routed.
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

	// The two groups are two names on the wire. Every voter registers a stream handler
	// per group under a protocol id derived from the namespace, so the fleet's ids are
	// the groups formed. Names are spelled out, not derived; the database id is
	// identical in both projects.
	const ledger = "QmVYRA67L6sJ1c8bZTwcs7UBvFdYesu414kPa2V9whE42e"
	const domain = "/tau/databases/v2"
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
			// A group answers on two ids, its command service and its transport
			// (pkg/raft/stream.go), both naming the namespace.
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

// deployFleet is deploy over several nodes in one region, with a published
// replication floor. Published, not configured, since two nodes disagreeing about it
// would disagree about placement forever. Three survives losing a node.
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

// deployFleetOf is deployFleet over several projects in one universe: a publish per
// project and a settle barrier waiting for all of them.
func deployFleetOf(t *testing.T, copies int, wasm []byte, extra map[string][]byte, projects ...deployed) universe {
	t.Helper()
	require.NotEmpty(t, projects, "a universe with nothing published in it proves nothing")

	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services: []string{
			instance.Register, instance.Worker, instance.Discovery, instance.Gateway,
			instance.Database, instance.Primitives},
		Copies: copies,
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	pids := u.Pids()
	require.Len(t, pids, copies)

	nodes := make([]*instance.Instance, 0, copies)
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

	// Every extra binary is stored the same way, keyed by the author's label.
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

	// Every node must have every project, the floor and the fleet before anything is
	// asked: a node seeing no others places over itself and opens a group nobody
	// placed it in. The message names which of the three (two CRDTs and a presence
	// window) it gave up on.
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
			if len(n.Discovery().Placement()) != copies {
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
