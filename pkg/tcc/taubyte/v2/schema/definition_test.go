package schema_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// root is the compiled project. The chroot envelope ({object, indexes}) only
// appears once a group declares an index footprint, so unwrap it if it is there
// and the tests keep working when v2 gains one.
func root(t *testing.T, flat map[string]any) map[string]any {
	t.Helper()
	if obj, ok := flat["object"].(map[string]any); ok {
		return obj
	}
	return flat
}

// resources reads one compiled group. Resources are keyed by id after
// compilation, so the tests read fields rather than names.
func resources(t *testing.T, flat map[string]any, group string) map[string]any {
	t.Helper()
	obj := root(t, flat)
	g, ok := obj[group].(map[string]any)
	assert.Assert(t, ok, "group %q missing; got %v", group, obj)
	return g
}

func only(t *testing.T, group map[string]any) map[string]any {
	t.Helper()
	assert.Equal(t, len(group), 1, "expected exactly one instance")
	for _, v := range group {
		r, ok := v.(map[string]any)
		assert.Assert(t, ok)
		return r
	}
	return nil
}

// namespace picks one instance of the container group by its authored name;
// after compilation the instances are keyed by id, like every other resource.
func namespace(t *testing.T, obj map[string]any, name string) map[string]any {
	t.Helper()
	ns, ok := obj["namespace"].(map[string]any)
	assert.Assert(t, ok, "namespaces missing")
	for _, v := range ns {
		if n := v.(map[string]any); n["name"] == name {
			return n
		}
	}
	t.Fatalf("namespace %q not found", name)
	return nil
}

// namedResource picks one compiled instance by its authored name.
func namedResource(t *testing.T, group map[string]any, name string) map[string]any {
	t.Helper()
	for _, v := range group {
		if r := v.(map[string]any); r["name"] == name {
			return r
		}
	}
	t.Fatalf("no resource named %q", name)
	return nil
}

// compileBasic is the fixture AS WRITTEN. Compiling lowers, so the object a runtime gets
// also holds the queues a workflow becomes — which is right, and is not what most of
// these tests are about: they check what the vocabulary does with what somebody wrote.
// The tests that are about lowering ask for the lowered object by name.
func compileBasic(t *testing.T) map[string]any {
	t.Helper()
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Authored(context.Background())
	assert.NilError(t, err)
	return obj.Flat()
}

// The whole point of v2: a function is code and limits. If any of these keys comes
// back, the trigger block was extended rather than deleted and reachability is
// still the function's business.
func TestFunctionHasNoTrigger(t *testing.T) {
	fn := namedResource(t, resources(t, compileBasic(t), "function"), "checkout")

	for _, gone := range []string{"type", "local", "protocol", "command", "method", "methods", "domains", "paths", "secure"} {
		_, present := fn[gone]
		assert.Assert(t, !present, "functions carries %q; the trigger block is meant to be deleted, not extended", gone)
	}

	// The pubsub kind is the one v1 trigger that survives on a function, and it means
	// the opposite of what it meant there. In v1 it was a TRIGGER — a scalar naming the
	// channel that fired this function. Here it is an ACCESS BLOCK — entries keyed by
	// the stream's name, each with its ops — so the direction is the function declaring
	// what it reaches. The shape is what tells them apart, so the shape is what this
	// asserts.
	if ch, present := fn["stream"]; present {
		_, isBlock := ch.(map[string]any)
		assert.Assert(t, isBlock, "functions carries a scalar %q: that is v1's trigger coming back", "stream")
	}

	assert.Equal(t, nested(fn, "execution", "call"), "checkout")
	assert.Equal(t, nested(fn, "execution", "timeout"), int64(20000000000), "20s parses to nanoseconds")
	assert.Equal(t, nested(fn, "execution", "memory"), int64(32000000), "32MB parses to bytes")
}

// `libraries` -> `component` carries into the reference form: a named component
// resolves against the compiled index — and compiles to `{component: <id>}`,
// two levels, because a kind and an id are two facts rather than one string to split.
func TestComponentSourceResolves(t *testing.T) {
	flat := compileBasic(t)
	modID := ""
	for id := range resources(t, flat, "component") {
		modID = id
	}
	assert.Assert(t, modID != "", "no component compiled")

	fn := namedResource(t, resources(t, flat, "function"), "checkout")
	assert.DeepEqual(t, fn["source"], map[string]any{"kind": "component", "component": modID})
}

// A queue's dead-letter is a ref to another queue, so a typo is a compile error
// rather than a silently undelivered item.
func TestQueueDeadLetterResolves(t *testing.T) {
	queues := resources(t, compileBasic(t), "queue")
	assert.Equal(t, len(queues), 2)

	var dl string
	for id, v := range queues {
		q := v.(map[string]any)
		if m, ok := q["dead_letter"].(map[string]any); ok {
			s, _ := m["name"].(string)
			assert.Assert(t, s != "", "dead-letter resolved to no id: %v", m)
			dl = s
			assert.Assert(t, dl != id, "a queue must not dead-letter to itself")
		}
	}
	assert.Assert(t, dl != "", "dead-letter was not set")
	_, exists := queues[dl]
	assert.Assert(t, exists, "dead-letter %q did not resolve to a compiled queue id", dl)
}

// A CREDENTIAL NEVER ENTERS THE CONFIG, and the two ways of naming one are both here
// with the difference between them stated, because it is easy to read them as the same
// thing.
//
// This used to be asserted on `connector.credentials` — a field on a kind since
// deleted. Both halves of what it held survive, in the places they belong:
//
//   - A REFERENCE resolves. `stream/shipments` names the secret its kafka authenticates
//     with, the attribute is a `Ref("secret")`, and what compiles out is the secret's
//     ID. The name is checked at compile: a secret that is not there is an error.
//   - AN ACCESS ENTRY does not, and is keyed by the NAME of what it reaches (§8b of
//     AGENTS.md). `function/poll_mentions` — the poller the fixture's connector became
//     — reaches `secret/slack` this way, which is how every workload reaches a secret
//     it reads at run time.
//
// The cost of the second is real and is the vocabulary's, not this deletion's: nothing
// resolves or checks an entry's key, so a typo in one is not a compile error the way a
// bad `credentials:` was.
func TestACredentialIsNeverAValue(t *testing.T) {
	flat := compileBasic(t)

	// A REFERENCE: the id, not the name, and never the value.
	shipments := namedResource(t, resources(t, flat, "stream"), "shipments")
	creds, _ := shipments["credentials"].(map[string]any)
	id, _ := creds["name"].(string)
	assert.Assert(t, strings.HasPrefix(id, "Qm"), "a kafka's credentials did not resolve to a secret id: %v", shipments["credentials"])
	assert.Assert(t, resources(t, flat, "secret")[id] != nil, "%q is not a compiled secret", id)

	// AN ACCESS ENTRY: the name, and the verb the kind declares.
	poller := namedResource(t, resources(t, flat, "function"), "poll_mentions")
	secrets, _ := poller["secret"].(map[string]any)
	entry, _ := secrets["slack"].(map[string]any)
	assert.Assert(t, entry != nil, "the poller reaches no secret: %v", poller["secret"])
	assert.DeepEqual(t, entry["ops"], []string{"get"})
	assert.Assert(t, namedResource(t, resources(t, flat, "secret"), "slack") != nil)
}

// A database's engine key IS the engine, from a closed set, so an external system
// that is not a database cannot be named as one. This replaced a `provides` role list
// on the `connector` kind — since deleted whole — which asked the author to restate a
// fact about software they did not write, and would have compiled just as happily when
// they got it wrong.
//
// THE SET IS PER TYPE, and that is what keeps a WRONG PAIR unrepresentable rather than
// merely checkable: a Mongo is not a thing a `sql:` block can name, and a Postgres is
// not a thing a `document:` block can, because neither is a key that exists there. One
// `engine:` beside one `type:` would have made both values legal on their own and only
// the pair wrong — a rule somebody has to remember to write, and a document that
// compiles until they do.
func TestDatabaseEngineIsClosed(t *testing.T) {
	compile := func(body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/database/orders.yaml",
			[]byte("id: QmSgvGwSJmtqBCJ8Zw3rNKtCCTqTFcqSUQqLYyPBnW6cVs\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		return err
	}

	err := compile("sql:\n    kafka:\n        host: broker:9092\n")
	assert.ErrorContains(t, err, "unknown key 'kafka'")
	assert.ErrorContains(t, err, "distributed, mysql, postgres",
		"and it says which engines a sql database has")

	// The pair, which is the half a single engine key could not refuse.
	assert.ErrorContains(t, compile("document:\n    postgres:\n        host: db:5432\n"),
		"unknown key 'postgres'; valid keys here: distributed, mongo")
	assert.ErrorContains(t, compile("sql:\n    mongo:\n        host: m:27017\n"),
		"unknown key 'mongo'; valid keys here: distributed, mysql, postgres")
	assert.ErrorContains(t, compile("kv:\n    postgres:\n        host: db:5432\n"),
		"unknown key 'postgres'; valid keys here: distributed")

	// And a data model that is not one of the four is not a block that can be written.
	assert.ErrorContains(t, compile("graph:\n    distributed:\n        size: 1GB\n"),
		"unknown key 'graph'")
	// Two of them at once is the switch refusing, not a winner being picked.
	assert.ErrorContains(t, compile("kv:\n    distributed:\n        size: 1MB\nsql:\n    distributed:\n        size: 1MB\n"),
		"ambiguous path: kv and sql each match")
	// And none at all is a database that is nothing.
	assert.ErrorContains(t, compile("description: nothing\n"), "attribute type has an empty key")
}

// A CONTAINER WORKER HAS NO ENTRYPOINT TO NAME, and it used to be refused for not
// naming one. `execution/call` was Required() inside the helper three kinds share,
// and only `worker` has a tier at all.
//
// The tier is Required() with no default, and that is the load-bearing half.
// RequiredWhen reads the discriminator FROM THE DOCUMENT — an absent one means the
// condition cannot hold — so `runtime` defaulting to wasm would have gated the
// entrypoint on a value the loader never sees, and every worker that wrote no
// `runtime:` line would have quietly stopped being asked for one. This pins all five
// combinations because the trap here is a check that passes by checking less.
func TestWorkerEntrypointIsRequiredOnTheWasmTierOnly(t *testing.T) {
	const head = "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n"
	compile := func(body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
		assert.NilError(t, afero.WriteFile(fs, "/worker/probe.yaml", []byte(head+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		return err
	}
	const limits = "execution:\n    timeout: 20s\n    memory: 32MB\n"
	const wasmLimits = "execution:\n    call: c\n    timeout: 20s\n    memory: 32MB\n"

	assert.ErrorContains(t, compile(limits), "required attribute 'runtime'",
		"a tier with no default has to be written")
	assert.NilError(t, compile("runtime: wasm\n"+wasmLimits))
	assert.ErrorContains(t, compile("runtime: wasm\n"+limits), "required attribute 'execution/call'")
	assert.NilError(t, compile("runtime: container\nimage: busybox\n"+limits),
		"a container names an image, not an exported symbol")
	assert.ErrorContains(t, compile("runtime: container\n"+limits), "required attribute 'image'")

	// The two kinds that are ALWAYS a component keep the unconditional demand. A gate
	// written inside the shared helper would have named an attribute neither has, and
	// a condition on a field that is not there is never true.
	for _, kind := range []string{"function", "entity"} {
		fs := afero.NewMemMapFs()
		assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
		assert.NilError(t, afero.WriteFile(fs, "/"+kind+"/probe.yaml", []byte(head+limits), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		assert.ErrorContains(t, err, "required attribute 'execution/call'", "kind %q", kind)
	}
}

// An external engine carries its own connection; tau's own carries a size and no
// host at all, so neither borrows the other's fields. And the TYPE is the block that
// was written rather than a word anybody typed.
func TestDatabaseEngineCarriesItsOwnConnection(t *testing.T) {
	flat := compileBasic(t)
	dbs := resources(t, flat, "database")

	orders := namedResource(t, dbs, "orders")
	assert.Equal(t, orders["type"], "sql", "the type is captured from the block that was written")
	assert.Equal(t, nested(orders, "sql", "engine"), "postgres")
	assert.Equal(t, nested(orders, "sql", "host"), "db.example.com:5432")
	sql, _ := orders["sql"].(map[string]any)
	creds, _ := sql["credentials"].(map[string]any)
	assert.Assert(t, creds != nil && creds["name"] != "",
		"credentials resolve to a secret id, never a value: %v", sql)
	_, borrowed := nestedOK(orders, "sql", "size")
	assert.Assert(t, !borrowed, "an external server's disk is not this project's to size")

	// A second external engine, with a set of its own: mongo is a document engine and
	// the fields it carries are the same ones, under its own type's block.
	profiles := namedResource(t, dbs, "profiles")
	assert.Equal(t, profiles["type"], "document")
	assert.Equal(t, nested(profiles, "document", "engine"), "mongo")
	assert.Equal(t, nested(profiles, "document", "host"), "mongo.example.com:27017")

	// tau's own engine, on the type that exists to have it.
	tokens := namedResource(t, dbs, "tokens")
	assert.Equal(t, tokens["type"], "kv")
	assert.Equal(t, nested(tokens, "kv", "engine"), "distributed")
	assert.Equal(t, nested(tokens, "kv", "size"), int64(64000000))
	for _, gone := range []string{"host", "database", "tls", "credentials"} {
		_, present := nestedOK(tokens, "kv", gone)
		assert.Assert(t, !present, "a distributed kv has no %q", gone)
	}

	obj := root(t, flat)
	ledger := namedResource(t, namespace(t, obj, "billing")["database"].(map[string]any), "ledger")
	assert.Equal(t, ledger["type"], "sql")
	assert.Equal(t, nested(ledger, "sql", "engine"), "distributed")
	for _, gone := range []string{"host", "credentials", "database"} {
		_, present := nestedOK(ledger, "sql", gone)
		assert.Assert(t, !present, "a distributed database has no %q", gone)
	}
}

// A FIELD REQUIRED ONLY ON ONE Either() BRANCH IS ENFORCED, and this is where that
// claim is kept. `engine/distributed/size` says RequiredWhen against a Captures()
// sibling; that read the branch's CONTENTS rather than its name until 2026-08-18
// (ENGINE-CHANGES.md #5), so a distributed database with no size compiled clean. The
// comment beside engine/tls recorded it as a live defect long after it was fixed,
// which is the reason a constraint belongs in a test and not in prose.
func TestRequiredOnOneEngineBranchIsEnforced(t *testing.T) {
	compile := func(body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/database/d.yaml",
			[]byte("id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		return err
	}
	assert.ErrorContains(t, compile("sql:\n    distributed: {}\n"), "required attribute 'sql/distributed/size'")
	assert.NilError(t, compile("sql:\n    distributed:\n        size: 1GB\n        consensus: crdt\n"))
	// The other branch owes nothing to the first one's field.
	assert.ErrorContains(t, compile("sql:\n    postgres: {}\n"), "required attribute 'sql/postgres/host'")
	// And every type asks the same question of its own block.
	assert.ErrorContains(t, compile("kv:\n    distributed: {}\n"), "required attribute 'kv/distributed/size'")

	// A vector index is required to state its width, and it is required whichever
	// engine holds it — an index built at another width is a different index.
	assert.ErrorContains(t, compile("vector:\n    distributed:\n        size: 1GB\n        consensus: crdt\n"),
		"required attribute 'vector/dimensions'")
	assert.NilError(t, compile("vector:\n    dimensions: 8\n    distributed:\n        size: 1GB\n        consensus: crdt\n"))

	// NO DEFAULT ON `metric`, and this is the constraint that decided it: a default
	// fires whenever the value is absent, whatever else the document says, so one on a
	// field of the vector block would write `metric: cosine` onto every kv, sql and
	// document database in the project. It says what omitting it means instead, the way
	// `tls` does.
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/database/d.yaml",
		[]byte("id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nkv:\n    distributed:\n        size: 1MB\n        consensus: crdt\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	d := namedResource(t, resources(t, obj.Flat(), "database"), "d")
	_, leaked := nestedOK(d, "vector", "metric")
	assert.Assert(t, !leaked, "a kv database carries no claim about a vector metric: %v", d)
}

// CONSENSUS IS A FIELD OF `distributed`, AND IT IS ONLY THERE. What an author states
// is what the store has to do with the second of two writes that land on one key;
// which shape of raft delivers `single-writer` — a synchronous replica set, or one
// master and followers — is a runtime decision and is never in the document.
//
// Two things are pinned because both can rot silently. That the field reaches EVERY
// type's own `distributed` block, because it is declared once in
// databaseType.attributes() and a fifth data model gets it by arriving; and that an
// external engine's block REFUSES it, because a Postgres has whatever consistency
// Postgres has and a key claiming otherwise is a promise this project cannot keep. The
// second is the literal path doing its work — `Path(t.name, tauEngine, "consensus")`
// names the branch rather than matching the engine set, so the wrong branch is an
// unknown key instead of a value filed where nothing reads it.
func TestDatabaseConsensusIsUnderTheDistributedEngineOnly(t *testing.T) {
	compile := func(path, body string) (map[string]any, error) {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, path,
			[]byte("id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		obj, _, err := c.Compile(context.Background())
		if err != nil {
			return nil, err
		}
		return obj.Flat(), nil
	}
	db := func(body string) (map[string]any, error) { return compile("/database/d.yaml", body) }

	// Every data model, under its own type block, with the type's own extra fields —
	// and both answers, since a set of one is a field nobody has to write.
	for _, tc := range []struct{ typ, body, want string }{
		{"kv", "kv:\n    distributed:\n        size: 1MB\n        consensus: crdt\n", "crdt"},
		{"document", "document:\n    distributed:\n        size: 1MB\n        consensus: single-writer\n", "single-writer"},
		{"sql", "sql:\n    distributed:\n        size: 1MB\n        consensus: single-writer\n", "single-writer"},
		{"vector", "vector:\n    dimensions: 8\n    distributed:\n        size: 1MB\n        consensus: crdt\n", "crdt"},
	} {
		flat, err := db(tc.body)
		assert.NilError(t, err, "type %q", tc.typ)
		d := namedResource(t, resources(t, flat, "database"), "d")
		assert.Equal(t, nested(d, tc.typ, "consensus"), tc.want, "type %q keeps it in its own block", tc.typ)
	}

	// AND NOWHERE ELSE. An external engine's block does not have the key at all, so
	// the error is the schema's own list of what is legal there rather than silence.
	_, err := db("sql:\n    postgres:\n        host: db:5432\n        consensus: crdt\n")
	assert.ErrorContains(t, err, "unknown key 'consensus'; valid keys here: credentials, database, host, tls",
		"a postgres has whatever consistency postgres has")
	_, err = db("document:\n    mongo:\n        host: m:27017\n        consensus: crdt\n")
	assert.ErrorContains(t, err, "unknown key 'consensus'; valid keys here: credentials, database, host, tls")

	// Nor beside the type block, where it would read as a property of the resource
	// rather than of the engine holding it.
	_, err = db("consensus: crdt\nkv:\n    distributed:\n        size: 1MB\n")
	assert.ErrorContains(t, err, "unknown key 'consensus'")

	// AND NOT ON A `cache`, WHICH HAS NO CONSENSUS QUESTION TO ASK. It is eventually
	// consistent and AP by definition, so there is nothing to coordinate and no engine
	// branch for this to hang under — its `size` and `ttl` sit at the document root
	// because there is nothing to choose. What a cache says instead is `replicated`,
	// which is a different question and has its own test below.
	_, err = compile("/cache/c.yaml", "size: 1MB\nconsensus: crdt\n")
	assert.ErrorContains(t, err, "unknown key 'consensus'")
	_, err = compile("/cache/c.yaml", "distributed:\n    size: 1MB\n")
	assert.ErrorContains(t, err, "unknown key 'distributed'")
}

// A CACHE SAYS WHERE ITS DATA LIVES, AND NOTHING ABOUT COORDINATION. The two fields
// are easy to conflate and they answer different questions, so both halves are pinned:
// `replicated` is a cache's and a database has no such key, `consensus` is a
// database's and a cache has no such key (above).
//
// THE ABSENT VALUE IS `false`, and unlike `consensus` that is a defensible silence:
// node-local is the cheap answer and replication is what an author asks for. The
// compiled object carries no key at all when nothing is written, so a consumer reads
// Go's zero value and gets the cheap one.
//
// AND IT IS NOT REACHABILITY. Who may read a cache is the access block another
// resource writes — per resource and per verb — and this says only where the data
// sits. `reach: { network: all | subnet | host }` used to be a second, coarser answer
// to the first question; it is gone, and this pins that it is.
func TestCacheSaysWhereItsDataLives(t *testing.T) {
	compile := func(path, body string) (map[string]any, error) {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, path,
			[]byte("id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		obj, _, err := c.Compile(context.Background())
		if err != nil {
			return nil, err
		}
		return obj.Flat(), nil
	}
	cache := func(body string) (map[string]any, error) {
		flat, err := compile("/cache/c.yaml", body)
		if err != nil {
			return nil, err
		}
		return namedResource(t, resources(t, flat, "cache"), "c"), nil
	}

	// It is a bool at the document root, beside size and ttl, because there is no
	// engine block to put it in.
	c, err := cache("size: 1MB\nreplicated: true\n")
	assert.NilError(t, err)
	assert.Equal(t, c["replicated"], true)

	// Absent is node-local, and it is absent from the object rather than written as
	// false: nothing was said, so nothing is recorded.
	c, err = cache("size: 1MB\n")
	assert.NilError(t, err)
	_, present := c["replicated"]
	assert.Assert(t, !present, "an unwritten replicated is absent, not false: %v", c)

	// A WRITTEN `false` IS KEPT, so an author who puts the default on the page next to
	// the reason for it gets it back from decompile rather than losing the line.
	c, err = cache("size: 1MB\nreplicated: false\n")
	assert.NilError(t, err)
	assert.Equal(t, c["replicated"], false)

	// AND THE COARSE REACHABILITY KEY IS GONE, not replaced: the access block is the
	// only thing that says who may reach a resource.
	_, err = cache("size: 1MB\nreach:\n    network: all\n")
	assert.ErrorContains(t, err, "unknown key 'reach'")
	_, err = compile("/storage/s.yaml", "object:\n    size: 1MB\nreach:\n    network: all\n")
	assert.ErrorContains(t, err, "unknown key 'reach'")

	// A DATABASE HAS NO SUCH KEY. Replication is not the axis a system of record
	// chooses on — it chooses consensus, and a database that is not replicated is not
	// something this platform offers.
	_, err = compile("/database/d.yaml", "kv:\n    distributed:\n        size: 1MB\n        consensus: crdt\n        replicated: true\n")
	assert.ErrorContains(t, err, "unknown key 'replicated'")
	_, err = compile("/database/d.yaml", "replicated: true\nkv:\n    distributed:\n        size: 1MB\n        consensus: crdt\n")
	assert.ErrorContains(t, err, "unknown key 'replicated'")

	// And the fixture carries the opt-in on the cache where it is the point, with the
	// rest of them silent — so "absent is legal" is held by every other cache in the
	// project rather than by an assertion about one.
	flat := compileBasic(t)
	orders := namedResource(t, resources(t, flat, "cache"), "orders")
	assert.Equal(t, orders["replicated"], true)
}

// REQUIRED, AND CLOSED — the two halves that leave no answer the schema has not been
// told about. This is the property a boolean could not have: `crdt: bool` had an
// absent state, and whichever way that state was read it was the wrong answer for
// somebody who never saw the field. There is no absent state now; there is an error
// that names the field, at the document that owes it.
//
// The other half is the value set. `raft` is the plausible wrong answer — it is what
// an author reaches for when the field asks about consensus — and it is exactly what
// this vocabulary refuses to be told, because the algorithm behind `single-writer` is
// the platform's to change without touching anybody's config.
//
// AN EXTERNAL ENGINE STILL OWES NOTHING, which is the half `Required()` could not
// express: the requirement rides the engine branch, so a postgres compiles clean
// without it and a kv database is not asked for `sql/distributed/consensus`.
func TestDatabaseConsensusIsRequiredAndClosed(t *testing.T) {
	compile := func(body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/database/d.yaml",
			[]byte("id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		return err
	}

	// WRITING NOTHING IS REFUSED, on every type, and the error names the field rather
	// than the resource: the author is told which line to add and where.
	for _, tc := range []struct{ typ, body string }{
		{"kv", "kv:\n    distributed:\n        size: 1MB\n"},
		{"document", "document:\n    distributed:\n        size: 1MB\n"},
		{"sql", "sql:\n    distributed:\n        size: 1MB\n"},
		{"vector", "vector:\n    dimensions: 8\n    distributed:\n        size: 1MB\n"},
	} {
		assert.ErrorContains(t, compile(tc.body), "required attribute '"+tc.typ+"/distributed/consensus'",
			"type %q owes an answer", tc.typ)
	}

	// Writing the key with nothing after it is the same omission, and is reported as
	// one rather than compiling an empty string into the object.
	assert.ErrorContains(t, compile("kv:\n    distributed:\n        size: 1MB\n        consensus:\n"),
		"required attribute 'kv/distributed/consensus'")

	// A VALUE OUTSIDE THE SET IS REFUSED — including the one that names the algorithm,
	// which is the mistake the closed set exists to catch: `single-writer` is a
	// guarantee and raft is one way of keeping it, which is the platform's to change.
	for _, v := range []string{"raft", "true", "single_writer", "eventual", "crdt "} {
		assert.ErrorContains(t, compile("kv:\n    distributed:\n        size: 1MB\n        consensus: \""+v+"\"\n"),
			"invalid value", "consensus: %q is not one of the two answers", v)
	}

	// AN EXTERNAL ENGINE OWES NOTHING. The requirement rides the engine branch, so
	// `database/orders` — a postgres — compiles with no answer at all, and a kv
	// database is never asked for `sql/distributed/consensus` either.
	flat := compileBasic(t)
	dbs := resources(t, flat, "database")
	orders := namedResource(t, dbs, "orders")
	_, present := nestedOK(orders, "sql", "consensus")
	assert.Assert(t, !present, "an external server's consistency is not this project's to state: %v", orders["sql"])

	// And the fixture holds both answers, on the resources where the choice is the
	// point: a token is the same token however often it is written at once, a debit
	// that merges with another debit is a debit nobody made, and an embedding a merge
	// discards costs a re-embed rather than a fact.
	assert.Equal(t, nested(namedResource(t, dbs, "tokens"), "kv", "consensus"), "crdt")
	assert.Equal(t, nested(namedResource(t, dbs, "knowledge"), "vector", "consensus"), "crdt")

	ledger := namedResource(t, namespace(t, root(t, flat), "billing")["database"].(map[string]any), "ledger")
	assert.Equal(t, nested(ledger, "sql", "consensus"), "single-writer")
}

// The new kinds load at all, with their scalars parsed.
func TestNewKindsCompile(t *testing.T) {
	flat := compileBasic(t)

	assert.Equal(t, only(t, resources(t, flat, "cron"))["schedule"], "0 3 * * *")
	assert.Equal(t, only(t, resources(t, flat, "cache"))["ttl"], int64(86400000000000), "24h in nanoseconds")
	assert.Equal(t, namedResource(t, resources(t, flat, "secret"), "stripe")["source"], "value", "default applies")
}

// `namespaces` is the SCOPE container: its instances hold resources of their own,
// compiled in their own scope. An inline source (".") is left alone by ref
// resolution. `applications` is a compute kind here, not a scope.
func TestNamespaceScope(t *testing.T) {
	obj := root(t, compileBasic(t))
	shop := namespace(t, obj, "shop")

	fn := namedResource(t, shop["function"].(map[string]any), "notify")
	_, hasSource := fn["source"]
	assert.Assert(t, !hasSource, "a resource built from its own code writes no source block")
	assert.Equal(t, nested(fn, "execution", "call"), "notify")

	kv := only(t, shop["cache"].(map[string]any))
	assert.Equal(t, kv["name"], "sessions")

	app := namedResource(t, resources(t, compileBasic(t), "application"), "api")
	assert.Equal(t, app["image"], "ghcr.io/acme/api:1.4", "applications is a compute kind, not a scope")
	// what the platform assigns is never authored: the port is handed to the
	// workload and tunnelled, and how many instances run is placement
	for _, gone := range []string{"port", "replicas"} {
		_, present := app[gone]
		assert.Assert(t, !present, "%q must not be authorable", gone)
	}
}

// Access is declared per TARGET, named once, with its ops beside it and a namespace
// path when it lives somewhere else. The block is a group nested in the resource's
// own document, so the resource stays a plain file with its id at the root.
func TestAccessIsPerTarget(t *testing.T) {
	flat := compileBasic(t)
	fn := namedResource(t, resources(t, flat, "function"), "checkout")

	caches, ok := fn["cache"].(map[string]any)
	assert.Assert(t, ok, "no caches block on the function")

	orders := caches["orders"].(map[string]any)
	assert.DeepEqual(t, orders["ops"], []string{"get", "put"})
	_, scoped := orders["namespace"]
	assert.Assert(t, !scoped, "a target in the same scope needs no namespace")

	// the whole point of the namespace field: naming a resource in another one
	sessions := caches["sessions"].(map[string]any)
	assert.Equal(t, sessions["namespace"], "/shop")
	assert.DeepEqual(t, sessions["ops"], []string{"get"})

	// nesting did NOT cost the resource its own compile passes
	assert.Equal(t, nested(fn, "execution", "timeout"), int64(20000000000), "scalars still parse")
	src, _ := fn["source"].(map[string]any)
	assert.Assert(t, src != nil && strings.HasPrefix(src["component"].(string), "Qm"),
		"refs still resolve: %v", fn["source"])

	// and a verb the kind does not implement is refused at load
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/function/checkout.yaml", []byte(`
id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh
execution: {call: checkout, timeout: 20s, memory: 32MB}
cache:
    orders:
        ops: [query]
`), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "\"query\" is not something a cache does")
}

// Ingress lives on the gateway, in its own flat document, the way a v1 function
// carried its whole trigger block in one file.
//
// The handler is a switch on the target kind — an Either() in the attribute path
// with Captures() capturing the branch — so `handler: { function: { namespace, name } }`
// compiles to the kind, the namespace path and the name, and a key outside the
// closed set is a compile error rather than a route that never fires.
func TestGatewayRoute(t *testing.T) {
	flat := compileBasic(t)

	gw := namedResource(t, resources(t, flat, "gateway"), "shop")
	// The name lives on the gateway, and it names a ZONE plus a label rather than a
	// flat fqdn — so a project moved to another cloud does not carry a name it does
	// not have there.
	assert.Equal(t, nested(gw, "host", "kind"), "zone")
	assert.Equal(t, nested(gw, "host", "name"), "shop")
	host, _ := gw["host"].(map[string]any)
	zone, _ := host["zone"].(map[string]any)
	in, _ := zone["in"].(map[string]any)
	assert.Assert(t, in != nil && in["name"] != "", "the zone resolves to a compiled zone id: %v", zone)
	assert.Equal(t, nested(gw, "certificate", "source"), "generated", "generated unless it is provided")

	assert.Equal(t, nested(gw, "route", "path"), "/checkout")
	assert.Equal(t, nested(gw, "route", "match"), "prefix", "default applies")
	assert.DeepEqual(t, nested(gw, "route", "methods"), []string{"POST"})

	// the switch, and the resource path through namespaces
	assert.Equal(t, nested(gw, "route", "handler", "kind"), "application", "Captures() captures which branch the handler took")
	assert.Equal(t, nested(gw, "route", "handler", "name"), "api")
	assert.Equal(t, nested(gw, "route", "handler", "port"), "http", "a workload with two ports is told which")

	// there is no route resource kind
	_, standalone := root(t, flat)["routes"]
	assert.Assert(t, !standalone, "routes must not be a resource kind")

	// and the function itself still says nothing about being reachable
	fn := namedResource(t, resources(t, flat, "function"), "checkout")
	for _, gone := range []string{"domains", "paths", "method", "type"} {
		_, present := fn[gone]
		assert.Assert(t, !present, "functions carries %q", gone)
	}
}

// The handler set is NARROWER than the vocabulary, and that is the property worth
// asserting: `kv` is a perfectly real kind and it is not something an HTTP request
// can be handed to. The Either() matches no key, so there is nothing for the switch
// to capture and the compile fails — an unroutable handler cannot become a route
// that silently never fires.
func TestNonHTTPHandlerKindIsRefused(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/gateway/shop.yaml", []byte(`
id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG
route:
    path: /checkout
    handler:
        kv:
            name: orders
`), 0o644))

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	// The Either() enumerates what a handler may be, so an unlisted key is now
	// rejected as an unknown key BEFORE the switch fails to capture one — same
	// refusal, at the offending line, naming what was allowed instead.
	assert.ErrorContains(t, err, "unknown key 'kv'",
		"a real but non-HTTP kind must not be a handler")
	assert.ErrorContains(t, err, "application, function, gateway, mcp, website",
		"the refusal has to say what a handler may be")
	// the position is the value's, so for a key holding a block it lands on the
	// first line inside that block rather than on the key
	assert.ErrorContains(t, err, "/gateway/shop.yaml:7:",
		"and where")
}

// Namespaces nest — v1's applications did not. A tenant can hold its own tenants,
// down to MaxNamespaceDepth, which is a declared cap because the schema is a finite
// node tree rather than a recursive grammar.
func TestNamespacesNest(t *testing.T) {
	shop := namespace(t, root(t, compileBasic(t)), "shop")

	inner, ok := shop["namespace"].(map[string]any)
	assert.Assert(t, ok, "a namespace must be able to hold namespaces")
	eu := only(t, inner)
	assert.Equal(t, eu["name"], "eu")

	// a third `sessions`, two levels down, distinct from both above it
	kv := only(t, eu["cache"].(map[string]any))
	assert.Equal(t, kv["name"], "sessions")
	assert.Equal(t, nested(kv, "size"), int64(2000000))
}

// The cost of nesting, asserted so it cannot change silently: the interpreter's
// scope walk covers the project root and ONE level of the container, so a reference
// written inside a namespace below the first level is not resolved. It is not an
// error and nothing reports it — the config compiles and the name stays a name.
func TestDeepNamespaceRefsAreNotResolved(t *testing.T) {
	flat := compileBasic(t)

	// the contrast first: a first-level namespace declares access like anywhere else
	shop := namespace(t, root(t, flat), "shop")
	notify := namedResource(t, shop["function"].(map[string]any), "notify")
	sessions := notify["cache"].(map[string]any)["sessions"].(map[string]any)
	assert.DeepEqual(t, sessions["ops"], []string{"get"})

	// two levels down there is nothing to resolve against yet, so the assertion is
	// simply that the scope compiles at all and holds its own resources
	eu := only(t, shop["namespace"].(map[string]any))
	assert.Equal(t, eu["name"], "eu")
	assert.Equal(t, len(eu["cache"].(map[string]any)), 1)
}

// Every verb has an effect class, assigned once, in the table next to the kind.
// invoke is write: a call may do anything the callee can do.
func TestEffectClasses(t *testing.T) {
	for _, c := range []struct {
		kind, verb, effect string
	}{
		{"cache", "get", schema.Read},
		{"cache", "put", schema.Write},
		{"cache", "watch", schema.Read}, // observes writes it cannot cause
		{"database", "query", schema.Read},
		{"database", "exec", schema.Write}, // the host cannot classify arbitrary SQL
		{"database", "get", schema.Read},   // kv, and document
		{"database", "put", schema.Write},
		{"database", "watch", schema.Read},   // kv only, and nothing implements it yet
		{"database", "search", schema.Read},  // vector
		{"database", "upsert", schema.Write}, // vector
		{"database", "delete", schema.Write}, // kv, document AND vector, one effect
		{"stream", "publish", schema.Write},
		{"stream", "subscribe", schema.Read},
		{"channel", "dial", schema.Write}, // opaque bytes, so it can be anything
		{"function", "invoke", schema.Write},
		{"secret", "get", schema.Read},
	} {
		got, ok := schema.EffectOf(c.kind, c.verb)
		assert.Assert(t, ok, "%s/%s has no effect class", c.kind, c.verb)
		assert.Equal(t, got, c.effect, "%s/%s", c.kind, c.verb)
	}

	// there is no write verb on a secret from a workload, ever
	_, ok := schema.EffectOf("secret", "put")
	assert.Assert(t, !ok, "secrets must have no write verb")

	// and an HTTP method is a route's, never a kind's: a workload nameable by GET
	// would be a second door into it that no gateway knows about
	for _, kind := range []string{"function", "application", "website", "gateway", "mcp"} {
		for _, m := range []string{"GET", "POST"} {
			_, ok := schema.EffectOf(kind, m)
			assert.Assert(t, !ok, "%s must not carry the HTTP method %q as an op", kind, m)
		}
	}
}

// ---- namespaces ------------------------------------------------------------
//
// `namespaces` is a CONTAINER group: its iterator is itself a group holding a
// nested copy of every resource group, which is what gives each instance its own
// scope. These tests exercise that scope rather than just its existence.

// The same NAME in two namespaces is two different resources. If this ever
// collapsed, every multi-tenant claim in v2 would be false.
func TestNamespacesIsolateNames(t *testing.T) {
	obj := root(t, compileBasic(t))
	assert.Equal(t, len(obj["namespace"].(map[string]any)), 2, "fixture should carry two namespaces")

	shop, billing := namespace(t, obj, "shop"), namespace(t, obj, "billing")

	sessionID := func(scope map[string]any) string {
		for id, v := range scope["cache"].(map[string]any) {
			if v.(map[string]any)["name"] == "sessions" {
				return id
			}
		}
		return ""
	}
	shopSessions, billingSessions := sessionID(shop), sessionID(billing)
	assert.Assert(t, shopSessions != "" && billingSessions != "")
	assert.Assert(t, shopSessions != billingSessions,
		"cache/sessions in two namespaces must compile to two different resources")
}

// Where a target lives is now stated, not inferred: an entry with no `namespace` is
// in the resource's own scope, and one with a namespace path names another. That is
// what makes a sibling namespace reachable at all — the ref resolver understands the
// current scope, the root and parents, and nothing else.
//
// The cost, asserted so it cannot change silently: an access target is a map KEY, so
// nothing resolves it at compile time. A typo in a target name is not a compile error
// the way a component reference to a name nothing declares is.
func TestAccessNamesItsScope(t *testing.T) {
	flat := compileBasic(t)

	shop := namespace(t, root(t, flat), "shop")
	notify := namedResource(t, shop["function"].(map[string]any), "notify")
	own := notify["cache"].(map[string]any)["sessions"].(map[string]any)
	_, scoped := own["namespace"]
	assert.Assert(t, !scoped, "a target in the resource's own scope needs no namespace")

	// and from the project root, reaching into a namespace
	fn := namedResource(t, resources(t, flat, "function"), "checkout")
	elsewhere := fn["cache"].(map[string]any)["sessions"].(map[string]any)
	assert.Equal(t, elsewhere["namespace"], "/shop")

	// the two `sessions` are different resources; the entries name them, and the
	// compiler does not resolve either one to an id
	for _, v := range []any{own["ops"], elsewhere["ops"]} {
		assert.Assert(t, v != nil)
	}
}

// A sibling namespace IS nameable now, and that is a change worth stating: v1 had no
// syntax for it, so isolation held by absence of grammar. `namespace: /billing` is
// grammar. Nothing in the compiler decides whether it is ALLOWED — the connection and
// the target-side grant that are supposed to bound it do not exist yet, so this
// compiles and the check has to live at publish or at runtime.
func TestSiblingNamespaceIsNameableAndUnchecked(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))

	// shop reaches into billing's books
	assert.NilError(t, afero.WriteFile(fs, "/namespace/shop/function/notify.yaml", []byte(`
id: QmbWqxBEKC3P8tqsKc98xmWNzrzDtRLMiMPL8wBuTGsMnR
execution: {call: notify, timeout: 5s, memory: 16MB}
database:
    ledger:
        namespace: /billing
        ops: [exec]
`), 0o644))

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err, "naming a sibling namespace compiles; nothing authorises it yet")

	shop := namespace(t, root(t, obj.Flat()), "shop")
	notify := namedResource(t, shop["function"].(map[string]any), "notify")
	ledger := notify["database"].(map[string]any)["ledger"].(map[string]any)
	assert.Equal(t, ledger["namespace"], "/billing")
	assert.DeepEqual(t, ledger["ops"], []string{"exec"})
}

func copyTree(dst afero.Fs, from, to string) error {
	return filepath.Walk(from, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return afero.WriteFile(dst, filepath.Join(to, rel), b, 0o644)
	})
}

// A zone is authoritative DNS, which is a different job from routing: it says what
// a name resolves to at all — mail, verification, a third-party CNAME, a box that
// is not tau. Folding it into the gateway would have deleted the whole surface.
func TestZoneIsNotRouting(t *testing.T) {
	flat := compileBasic(t)

	zone := only(t, resources(t, flat, "zone"))
	assert.Equal(t, zone["fqdn"], "example.com")
	records := zone["records"].([]string)
	assert.Equal(t, len(records), 4)

	// zone-file notation, kept verbatim: the compiler stores what you wrote, so a
	// zone exported from another provider round-trips without translation.
	assert.Assert(t, strings.Contains(records[0], "MX 10 mail.example.com."))

	// A zone carries no routing and no verbs pointed at it: nothing `uses` a name.
	for k := range zone {
		assert.Assert(t, !strings.HasPrefix(k, "use-"), "a zone declared %q", k)
	}
	_, ok := schema.EffectOf("zone", "get")
	assert.Assert(t, !ok, "zones must not be a target kind")

	// and the gateway that serves a name under it still holds the ingress
	gw := namedResource(t, resources(t, flat, "gateway"), "shop")
	assert.Equal(t, nested(gw, "host", "name"), "shop")
}

// A namespace path is absolute with a leading "/" and relative without one — the same
// two forms the name resolver already uses for references. "/" is the project root
// itself; omitted means the resource's own scope, then its parents.
func TestNamespacePathForms(t *testing.T) {
	obj := root(t, compileBasic(t))

	shop := namespace(t, obj, "shop")
	notify := namedResource(t, shop["function"].(map[string]any), "notify")
	caches := notify["cache"].(map[string]any)

	// omitted: shop's own sessions
	_, scoped := caches["sessions"].(map[string]any)["namespace"]
	assert.Assert(t, !scoped, "a target in the resource's own scope names no namespace")

	// "/": the project root, from inside a namespace
	assert.Equal(t, caches["orders"].(map[string]any)["namespace"], "/")

	// absolute, from two levels down
	eu := only(t, shop["namespace"].(map[string]any))
	relay := only(t, eu["function"].(map[string]any))
	assert.Equal(t, relay["database"].(map[string]any)["ledger"].(map[string]any)["namespace"], "/billing")

	// and the shape is checked at load
	for _, bad := range []string{"shop/", "shop//eu", "../billing", "Shop-EU"} {
		fs := afero.NewMemMapFs()
		assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
		assert.NilError(t, afero.WriteFile(fs, "/function/checkout.yaml", []byte(
			"id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n"+
				"execution: {call: c, timeout: 1s, memory: 1MB}\n"+
				"cache:\n    orders:\n        namespace: "+bad+"\n        ops: [get]\n"), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		assert.ErrorContains(t, err, "namespace path", "%q must be refused", bad)
	}
}

// Per-use modifiers: an access entry is a place to put them, which a verb-keyed list
// of names never had. They get the same treatment as any other attribute — the
// duration parses — because a nested group is processed as an ordinary group, per
// entry.
func TestAccessEntryModifiers(t *testing.T) {
	flat := compileBasic(t)

	fn := namedResource(t, resources(t, flat, "function"), "checkout")
	orders := fn["cache"].(map[string]any)["orders"].(map[string]any)
	assert.Equal(t, orders["timeout"], int64(200000000), "200ms parses to nanoseconds")

	// and an entry that names none carries none
	sessions := fn["cache"].(map[string]any)["sessions"].(map[string]any)
	_, present := sessions["timeout"]
	assert.Assert(t, !present, "an unauthored modifier must not appear")
}

// The target owns the policy, the accessor only states intent: a reachable resource
// declares the authorizer that decides what a caller gets, and a caller declares the
// ops it asks for. The caller cannot name its own judge.
func TestTargetOwnsThePolicy(t *testing.T) {
	flat := compileBasic(t)

	// the cache being reached carries the authorizer
	orders := namedResource(t, resources(t, flat, "cache"), "orders")
	assert.Equal(t, nested(orders, "authorizer", "name"), "tier_check")
	assert.Equal(t, nested(orders, "authorizer", "namespace"), "/shop")

	// the caller states what it asks for, and names no authorizer
	fn := namedResource(t, resources(t, flat, "function"), "checkout")
	entry := fn["cache"].(map[string]any)["orders"].(map[string]any)
	assert.DeepEqual(t, entry["ops"], []string{"get", "put"})
	for _, gone := range []string{"via", "via-namespace", "authorizer", "authorizer-name"} {
		_, present := entry[gone]
		assert.Assert(t, !present, "an accessor must not name its own authorizer: %q", gone)
	}

	// a kind nothing can reach has no authorizer to declare
	zone := only(t, resources(t, flat, "zone"))
	_, present := nestedOK(zone, "authorizer", "name")
	assert.Assert(t, !present, "a kind with no ops is not reachable, so it authorises nothing")
}

// Ports are named because the external one is dynamic and there may be several
// instances of the same workload on one host — so there is no single external number
// to write down. Everything else refers to a port by name.
func TestPortsAreNamed(t *testing.T) {
	flat := compileBasic(t)

	app := namedResource(t, resources(t, flat, "application"), "api")
	ports := app["ports"].(map[string]any)
	assert.Equal(t, ports["http"].(map[string]any)["number"], 8080, "the number is the INTERNAL one")
	assert.Equal(t, ports["metrics"].(map[string]any)["number"], 9090)

	// a probe names a port rather than repeating a number, and says which kind it is
	assert.Equal(t, nested(app, "readiness", "kind"), "http")
	assert.Equal(t, nested(app, "readiness", "http", "port"), "http")
	assert.Equal(t, nested(app, "liveness", "kind"), "http")
	assert.Equal(t, nested(app, "liveness", "http", "port"), "http")

	// or a function decides, which is a different kind of answer and says so
	pool := namedResource(t, resources(t, flat, "application"), "worker_pool")
	assert.Equal(t, nested(pool, "readiness", "kind"), "function")
	assert.Equal(t, nested(pool, "readiness", "function", "name"), "pool_ready")

	// and a workload that declares no liveness simply has none — the case an
	// Either()+Captures() switch could not express before
	_, live := nestedOK(pool, "liveness", "kind")
	assert.Assert(t, !live, "no liveness declared, so nothing restarts it")
	_, probe := nestedOK(pool, "readiness", "http", "path")
	assert.Assert(t, !probe, "a function-checked workload declares no probe path")

	// so does a route targeting the workload
	gw := namedResource(t, resources(t, flat, "gateway"), "shop")
	assert.Equal(t, nested(gw, "route", "handler", "kind"), "application")
	assert.Equal(t, nested(gw, "route", "handler", "port"), "http")

	// ceilings are authored; counts are not
	assert.Equal(t, nested(app, "limits", "max_instances"), 10)
	assert.Equal(t, nested(app, "limits", "concurrency"), 80)
	for _, gone := range []string{"port", "replicas", "instances"} {
		_, present := app[gone]
		assert.Assert(t, !present, "%q must not be authorable", gone)
	}
}

// --- what the multi-profile review changed, asserted so it cannot drift back ---

// Egress is a property of a WORKLOAD, not of containers. It used to be on
// `applications` alone, which said a container reaching the internet is a decision and
// a function doing the same is not — while the guest HTTP client is exactly as real as
// the container's socket.
func TestEveryWorkloadCanDeclareEgress(t *testing.T) {
	for _, kind := range []string{"application", "function", "worker", "entity", "sandbox"} {
		var found bool
		for _, g := range schema.CompileRoot().Children {
			if name, _ := g.Match.(string); name != kind || len(g.Children) == 0 {
				continue
			}
			for _, a := range g.Children[0].Attributes {
				if a.Name == "egress" {
					found = true
				}
			}
		}
		assert.Assert(t, found, "%s cannot declare egress; a sandbox runs code somebody else wrote", kind)
	}
}

// A cron expression alone does not name a moment: "0 3 * * *" is a different instant
// in every zone.
func TestCronCarriesItsTimezone(t *testing.T) {
	cron := only(t, resources(t, compileBasic(t), "cron"))
	assert.Equal(t, cron["schedule"], "0 3 * * *")
	assert.Equal(t, cron["timezone"], "Europe/Paris", "the zone the schedule is written in")
	assert.Equal(t, cron["on_overlap"], "skip", "the only choice that cannot make things worse")

	// and a cron that says nothing gets the zone where every day has 24 hours
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/cron/c.yaml",
		[]byte("id: QmRAQB6YaCyidP37UdDnjFY5vQuiBrcqdyoW1CuDgwxkD4\nschedule: \"@daily\"\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	assert.Equal(t, only(t, resources(t, obj.Flat(), "cron"))["timezone"], "UTC")
}

// Whether credentials cross a network in the clear is not a question to answer with a
// default nobody reads — and a default would also land on a distributed database that
// never opens a socket.
func TestExternalDatabaseMustStateItsTransport(t *testing.T) {
	orders := namedResource(t, resources(t, compileBasic(t), "database"), "orders")
	assert.Equal(t, nested(orders, "sql", "tls"), "verify-full")

	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/secret/k.yaml",
		[]byte("id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/database/x.yaml", []byte(`
id: QmSgvGwSJmtqBCJ8Zw3rNKtCCTqTFcqSUQqLYyPBnW6cVs
sql:
    postgres:
        host: db.example.com
        credentials:
            name: k
`), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err, "omitting it is allowed; absent means verify-full")
	x := namedResource(t, resources(t, obj.Flat(), "database"), "x")
	_, stated := nestedOK(x, "sql", "tls")
	assert.Assert(t, !stated, "and it stays absent rather than compiling a value nobody wrote")

	// and tau's own engine is never asked the question
	ledger := namedResource(t, namespace(t, root(t, compileBasic(t)), "billing")["database"].(map[string]any), "ledger")
	_, present := nestedOK(ledger, "sql", "tls")
	assert.Assert(t, !present, "a distributed database opens no socket")
}

// Every attribute names a section, and a section has a title and a doc that a UI
// groups by. An attribute in a section its kind never declared lands in a group with
// no name — invisible in Go, and the reason `egress` arrived on four kinds inside a
// "network" section that only two of them had.
func TestEverySectionAnAttributeClaimsIsDeclared(t *testing.T) {
	walk := func(t *testing.T, kind string, it *engine.Node) {
		t.Helper()
		declared := map[string]bool{}
		if secs, ok := it.Meta["sections"].([]engine.SectionSpec); ok {
			for _, s := range secs {
				declared[s.ID] = true
			}
		}
		for _, a := range it.Attributes {
			s, _ := a.Meta["section"].(string)
			if s == "" {
				continue
			}
			assert.Assert(t, declared[s], "%s: attribute %q is in section %q, which %s never declares", kind, a.Name, s, kind)
		}
	}
	for _, g := range schema.CompileRoot().Children {
		name, _ := g.Match.(string)
		if len(g.Children) == 0 {
			continue
		}
		walk(t, name, g.Children[0])
	}
}

// F8: a workload can be configured. It could declare an image, ports, ceilings and
// which secrets it may read — but not a log level, so the only place to put one was
// inside the image, which makes one image per environment.
func TestWorkloadVariables(t *testing.T) {
	api := namedResource(t, resources(t, compileBasic(t), "application"), "api")
	vars, ok := api["variables"].(map[string]any)
	assert.Assert(t, ok, "no variables block")

	// a literal is in git and says so
	assert.Equal(t, vars["LOG_LEVEL"].(map[string]any)["value"], "info")

	// a secret is a REFERENCE: the value never enters the config
	sec, _ := vars["STRIPE_KEY"].(map[string]any)["secret"].(map[string]any)
	from, _ := sec["name"].(string)
	assert.Assert(t, strings.HasPrefix(from, "Qm"),
		"got %v, want a resolved secret id", vars["STRIPE_KEY"])
	_, literal := vars["STRIPE_KEY"].(map[string]any)["value"]
	assert.Assert(t, !literal)

	// every workload kind can be configured, not just the container one
	for _, kind := range []string{"application", "function", "worker", "entity", "sandbox"} {
		var found bool
		for _, g := range schema.CompileRoot().Children {
			if name, _ := g.Match.(string); name != kind || len(g.Children) == 0 {
				continue
			}
			for _, child := range g.Children[0].Children {
				if n, _ := child.Match.(string); n == "variables" {
					found = true
				}
			}
		}
		assert.Assert(t, found, "%s cannot be configured", kind)
	}
}

// A Default() and a Required() on one attribute contradict each other: Required is
// checked first, so the default never applies and the JSON Schema publishes a value
// the compiler rejects.
func TestDefaultsThatWereUnreachable(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/application/a.yaml", []byte(
		"id: QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY\nimage: img\nexecution:\n    memory: 1MB\nports:\n    p:\n        number: 80\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/queue/q.yaml",
		[]byte("id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\n"), 0o644))
	// a generated host needs no certificate even when TLS stops here — the cloud
	// holds a wildcard for its own zone, which is the gateway's rule too
	assert.NilError(t, afero.WriteFile(fs, "/proxy/p.yaml", []byte(
		"id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nhost:\n    generated:\n        name: db\ntls:\n    mode: terminate\nforward:\n    application:\n        name: a\n        port: p\n"), 0o644))

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	q := only(t, resources(t, obj.Flat(), "queue"))
	assert.Equal(t, q["visibility_timeout"], int64(30000000000), "the default has to be reachable")

	p := only(t, resources(t, obj.Flat(), "proxy"))
	assert.Equal(t, p["protocol"], "tcp")
	assert.Equal(t, nested(p, "certificate", "source"), "generated")
}

// The workflow's lowering, on the real vocabulary: what it becomes, and the wiring that
// lets a consumer draw the inside of one without knowing what a workflow is.
func TestWorkflowLowers(t *testing.T) {
	plan := schema.LoweringPlan(compileBasic(t))

	got := map[string]string{}
	for _, d := range plan {
		got[d.Kind+"/"+d.Name] = d.Why
	}
	assert.Assert(t, got["queue/checkout_reserve"] != "", "one queue per step: %v", got)
	assert.Assert(t, got["queue/checkout_charge"] != "")
	assert.Assert(t, got["queue/checkout_settle"] != "", "a wait is a step, so it becomes a queue and a driver like any other: %v", got)
	assert.Assert(t, got["queue/checkout_failed"] != "", "somewhere for a step that gave up")

	// AND ONE DRIVER PER STEP, which is what makes the box readable when it is opened:
	// the queues are wired to something that lives in there with them, rather than to
	// functions at the root that the pipeline's every edge has to leave the frame to
	// reach. It carries the step's name and no suffix — the kind is already part of a
	// resource's identity here.
	assert.Assert(t, got["function/checkout_reserve"] != "", "one driver per step: %v", got)
	assert.Assert(t, got["function/checkout_charge"] != "")
	assert.Assert(t, got["function/checkout_settle"] != "")
	// AND NOTHING ELSE. A stream was produced here so that finishing had an address,
	// and nothing authored asked for it — which is the rule everywhere else in this
	// vocabulary, and what every durable engine in the market does: step history for
	// replay and a UI, never an application-facing bus by default.
	for k := range got {
		assert.Assert(t, !strings.HasPrefix(k, "stream/"),
			"a workflow produced a stream nobody asked for: %v", got)
	}

	// NO store for the run's state: the state is the message, and which queue it is in
	// is which step it is at. It briefly lowered into an entity, which is a code-bearing
	// kind whose code nothing was setting — a resource nobody could instantiate.
	for _, d := range plan {
		if d.Of != "workflow/checkout" {
			continue
		}
		assert.Assert(t, d.Kind != "entity", "a workflow needs no entity: %v", got)
	}

	// EVERY WRITE THE PLAN CARRIES, keyed by what it lands on and where. That the value
	// is a verb and the path an access entry is this vocabulary's reading of it — the
	// compiler carries a value at a path and nothing else.
	writes := map[string]string{}
	for _, d := range plan {
		if d.Of != "workflow/checkout" {
			continue
		}
		for _, w := range d.Writes {
			on := "self"
			if w.On.Kind != "" {
				on = w.On.Kind + "/" + w.On.Name
			}
			writes[on+" "+strings.Join(w.Path, "/")] = w.Value
		}
	}

	// A queue nothing receives from is a queue nothing happens in — and what receives is
	// the step's DRIVER, produced beside it, never the author's function: the function is
	// invoked and knows no queues at all.
	assert.Equal(t, writes["function/checkout_reserve queue/checkout_reserve/ops"], "receive")

	// FORWARD PROGRESS IS ONE GRANT. The driver sends to exactly one queue — the next
	// step's — so "which of these means next" is not a question anybody has to answer by
	// reading names. The last step has no next and the write is dropped.
	assert.Equal(t, writes["function/checkout_reserve queue/checkout_settle/ops"], "send")
	assert.Equal(t, writes["function/checkout_charge queue/checkout_charge/ops"], "receive")
	for k, v := range writes {
		assert.Assert(t, !strings.HasPrefix(k, "function/checkout_charge queue/") || v != "send",
			"the last step sends nowhere: %s", k)
	}

	// AND THE WORK ITSELF IS AN INVOKE, out to the function the author wrote. It is the
	// one edge per step that leaves the box, and it is an ownership boundary: their
	// function is theirs, and other things may call it too.
	assert.Equal(t, writes["function/checkout_reserve function/reserve_stock/ops"], "invoke")

	// THE AUTHOR'S FUNCTION GAINS NOTHING. It used to carry the whole pipeline as grants
	// — receive from its own queue, send to the next — which is the pipeline stated a
	// second time, on resources outside the workflow, where the first copy could not be
	// seen next to it.
	for k := range writes {
		assert.Assert(t, !strings.HasPrefix(k, "function/reserve_stock ") && !strings.HasPrefix(k, "function/charge_card "),
			"the step's own function was written into: %s", k)
	}

	// The dead-letter queue is NOT an entry: it is the queue's own `dead_letter` field,
	// which is already config, and it fires at the one moment no code of the step is
	// running. A second statement of it was the whole reason the arrow between two
	// queues read as unexplained.
	for _, d := range plan {
		if d.Kind != "queue" || d.Name != "checkout_reserve" {
			continue
		}
		assert.Equal(t, d.Fields["dead_letter/name"], "checkout_failed")
		for _, w := range d.Writes {
			assert.Assert(t, !strings.Contains(strings.Join(w.Path, "/"), "checkout_failed"),
				"the dead-letter is a field, not an entry")
		}
	}
}

// A lowering into a kind the vocabulary does not have is a plan nothing can carry out,
// and the vocabulary author is the one who should hear about it.
func TestEveryLoweringNamesARealKind(t *testing.T) {
	assert.DeepEqual(t, interp.CheckLowerings(schema.CompileRoot()), []string(nil))
}

// THE STANDING RULE: no consumer hardcodes this vocabulary, so a graph, a CLI or a
// console draws a version of it they have never seen. Icon() and Category() and Ops()
// were the first of these; these are the rest.
//
// AND THERE IS ONE WAY TO SAY EACH THING. Pointing at another resource was briefly
// three terms — Ref, plus one for fields that keep a name, plus one for blocks whose
// kind the author picks — which is three places to add a case and two to forget. It is
// Ref with modifiers, and this asserts that the whole vocabulary is readable through it.
func TestConsumersNeedNoHardcodedShapes(t *testing.T) {
	refs := schema.References()
	find := func(on, field string) schema.Reference {
		t.Helper()
		for _, r := range refs {
			if r.On == on && r.Field == field {
				return r
			}
		}
		t.Fatalf("%s has no declared reference %q", on, field)
		return schema.Reference{}
	}

	// resolves to an id — the kind is on the Ref, and the namespace is authored beside
	// the name, so nothing is packed into the value
	src := find("function", "source/component")
	assert.Equal(t, src.Kind, "component")
	assert.Equal(t, src.Prefix, "", "a v2 reference carries no kind in its value")
	assert.Assert(t, !src.ByName)

	// keeps the authored name, and says where its namespace is
	auth := find("cache", "authorizer/name")
	assert.Equal(t, auth.Kind, "function")
	assert.Assert(t, auth.ByName, "an authorizer is named, not resolved")
	assert.Equal(t, auth.Namespace, "authorizer/namespace")

	// the kind is the author's choice, read from the switch beside it
	handler := find("gateway", "route/handler/name")
	assert.Equal(t, handler.KindFrom, "route/handler/kind")
	assert.Assert(t, handler.ByName)

	// the relationship runs the other way: the zone answers FOR the gateway
	zone := find("gateway", "host/zone/in/name")
	assert.Assert(t, zone.Inbound, "an arrow from the declarer would claim the opposite")

	// and one that lives on the ENTRIES of a nested group, not on the resource — the
	// distinction that stopped every resource pointing at a function of its own name
	step := find("workflow", "call/function")
	assert.Equal(t, step.In, "steps")
	assert.Equal(t, step.Namespace, "call/namespace")

	// the inbound blocks are derived from the references, not listed anywhere
	assert.DeepEqual(t, schema.InboundBlocks(), []string{"forward", "route/handler", "route/router", "router", "serve"})

	// how an access entry is written, and where a document lives
	access := schema.AccessEntryShape()
	assert.Equal(t, access.Ops, "ops")
	assert.Equal(t, access.Namespace, "namespace")

	layout := schema.LayoutOf()
	assert.Equal(t, layout.Resource, "{kind}/{name}.yaml")
	assert.Equal(t, layout.Scope, "{kind}/{name}")
}

// A NAME IS THREE PLACES GLUED: the label a resource chose, the zone that answers for
// it, and — for a generated one — the project's own label, derived from its id. Whoever
// glues them decides what the name is, so it is glued here and nowhere else. A drawing
// was doing its own version, which agreed with this one by coincidence.
func TestReachableNames(t *testing.T) {
	names := schema.ReachableNames(compileBasic(t))

	got := map[string]string{}
	generated := map[string]bool{}
	for _, r := range names {
		got[r.Name] = r.Fqdn
		generated[r.Name] = r.Generated
	}

	// a zone the project declares: the whole name is knowable
	assert.Equal(t, got["shop"], "shop.example.com")
	assert.Equal(t, got["ask"], "ask.example.com")
	assert.Assert(t, !generated["shop"])

	// the cloud's own zone: the project's half is derived from its id, and the cloud's
	// half is NOT here — it is not in the config and is not the project's to assert
	assert.Assert(t, generated["db"], "a generated name has to say that it is one")
	assert.Equal(t, got["db"], "db."+schema.ProjectLabel("QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"))
	assert.Assert(t, !strings.Contains(got["db"], "cloud"), "the missing half is left missing, not guessed")

	// a zone is what ANSWERS, not what is answered for
	for _, r := range names {
		assert.Assert(t, r.Name != "example_com", "a zone must not be listed as reachable")
	}
}

// nestedOK is nested with the comma-ok a presence test needs.
func nestedOK(res map[string]any, segments ...string) (any, bool) {
	at := res
	for _, seg := range segments[:len(segments)-1] {
		next, ok := at[seg].(map[string]any)
		if !ok {
			return nil, false
		}
		at = next
	}
	v, ok := at[segments[len(segments)-1]]
	return v, ok
}

// nested reads a value at a multi-segment key, which is what a nested Path compiles to.
func nested(res map[string]any, segments ...string) any {
	at := res
	for _, seg := range segments[:len(segments)-1] {
		next, ok := at[seg].(map[string]any)
		if !ok {
			return nil
		}
		at = next
	}
	return at[segments[len(segments)-1]]
}

// A CROSS-NAMESPACE ACCESS COMPILES AND NOTHING AUTHORISES IT.
//
// `namespace: /billing` is grammar for naming a sibling scope, which v1 did not have —
// isolation used to hold because there was no way to write it down. Now there is, and
// nothing decides whether it is allowed: the connection between two scopes, and the
// target-side grant meant to bound it, are control-plane objects that do not exist.
// The check has to live at publish or at runtime.
//
// Asserted rather than left in prose, because a security property nothing enforces is
// one somebody will assume is enforced. The day this fails is the day something DID
// authorise it, and this test is where to record what.
func TestACrossNamespaceAccessIsNotAuthorised(t *testing.T) {
	flat := compileBasic(t)
	shop := namespace(t, root(t, flat), "shop")
	eu := namespace(t, shop, "eu")
	relay := namedResource(t, resources(t, eu, "function"), "relay")

	// A SIBLING, which is the case v1 could not write down. /shop/eu reaching
	// /billing walks into a scope that is not its own and not its parent, so no
	// resolution rule permits it — the grammar does, and nothing else looks.
	ledger, ok := nestedOK(relay, "database", "ledger")
	assert.Assert(t, ok, "the cross-scope entry did not compile at all")
	entry, _ := ledger.(map[string]any)
	assert.Equal(t, entry["namespace"], "/billing", "the sibling scope is carried, not resolved away")

	// It is a NAME rather than an id: nothing resolved it, so nothing could have
	// checked it either. The day it resolves, something decided this was allowed.
	_, resolved := entry["name"]
	assert.Assert(t, !resolved, "the cross-scope target now resolves - what authorised it? record that, and delete this test")
}

// A RESOURCE'S OWN NAME IS NEVER VALIDATED, and the two ends disagree about it.
//
// The name comes from the FILE name and the compile driver sets it without checking,
// so `paid-tier.yaml` yields `paid-tier` — which IsVariableName() rejects, and which a
// reference written to it is checked against and fails. Anything that resolves a
// reference to such a resource cannot reach it, and nothing said so at compile time.
func TestAResourceNameIsNotValidated(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/function/paid-tier.yaml", []byte(
		"id: QmWQ7XwnqQzhLTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPYuB\n"+
			"execution: {call: c, timeout: 1s, memory: 1MB}\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err, "the hyphen is accepted as a name")

	fn := namedResource(t, resources(t, obj.Flat(), "function"), "paid-tier")
	assert.Equal(t, fn["name"], "paid-tier")

	// ...while a REFERENCE to that same name is checked and refused, which is the
	// disagreement: one end of the vocabulary can write what the other cannot read.
	assert.NilError(t, afero.WriteFile(fs, "/gateway/tier.yaml", []byte(
		"id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\n"+
			"route:\n    path: /\n    handler:\n        function:\n            name: paid-tier\n"), 0o644))
	c2, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c2.Compile(context.Background())
	assert.ErrorContains(t, err, "variable name",
		"a reference to a name the driver accepted must still be refused - if this changed, the two ends agree now")
}

// `instructions` names a FILE, so the repository root is not a value it can take —
// `.` is legal on the bare `source`, which names a directory, and legalizing it in a
// shared grammar put it on every field that grammar reaches.
func TestInstructionsCannotBeTheRepositoryRoot(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/agent/support.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Za\n"+
			"model:\n    name: opus\ninstructions: \".\"\nlimits: {steps: 4}\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "not a place this field can name")
}

// THE UNION IS ORDERED AND ITS EFFECTS AGREE ACROSS TYPES. `delete` is a verb of three
// data models and it is a write in all three; `get` is a read in two; `query` is a read
// for both sql and document. A verb that meant one class on one type and another class
// elsewhere would make the union meaningless — the effect is the join key every
// cross-cutting query groups by — so databaseOps() refuses to build one, and this is
// what says the table it builds from has not drifted.
func TestDatabaseVerbsAreTheFourTypesUnioned(t *testing.T) {
	got := make([]string, 0, 9)
	for _, v := range schema.OpsOf("database") {
		got = append(got, v.Name)
	}
	assert.DeepEqual(t, got, []string{
		"get", "list", "watch", "put", "delete", // kv
		"query",            // document, then sql, which shares it
		"exec",             // sql
		"search", "upsert", // vector; its delete is already in the list
	})

	// And it is declaration order, not sorted: an editor completes in the order the
	// vocabulary states, and the position of a bucket is not decoration here either.
	assert.Assert(t, got[0] == "get" && got[len(got)-1] == "upsert")
}

// `kv` HAS NO `ttl`, AND THAT IS THE STRUCTURAL DIFFERENCE FROM `cache`, not a
// difference in prose. A cache entry has a lifetime — losing one costs a recomputation
// — and a record that deletes itself is not a record. The distinction was undermined
// once already by the two kinds being documented apart and shaped the same, so it is
// pinned here: the field is not merely undocumented on a kv, it is not a key that can
// be written.
func TestADurableKvHasNoLifetime(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/database/d.yaml",
		[]byte("id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nkv:\n    distributed:\n        size: 1MB\n        ttl: 24h\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "unknown key 'ttl'")

	// …while the cache beside it is where a lifetime belongs, and still takes one.
	assert.Equal(t, only(t, resources(t, compileBasic(t), "cache"))["ttl"], int64(86400000000000))
}

// BOTH NAMES, ASSERTED BY MEANING, because the two of them swapped and a half-done swap
// reads as correct in either direction: `channel` and `stream` are both valid
// identifiers, so a tree where only one side moved still compiles and still passes any
// test that names only one of them.
//
// So this asserts what each name MEANS. A `channel` is 1:1 — it names a protocol and
// exactly one workload that answers it, and its verb is `dial`. A `stream` fans out — it
// matches a name or a pattern, it has no server at all, and its verbs are `publish` and
// a `subscribe` the platform DELIVERS. Neither document loads against the other kind.
func TestChannelIsOneToOneAndStreamFansOut(t *testing.T) {
	const id = "id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEC\n"
	compile := func(path, body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/application/db.yaml",
			[]byte("id: QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY\nimage: img\nexecution:\n    memory: 1MB\nports:\n    pg:\n        number: 5432\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, path, []byte(id+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		return err
	}
	const conduit = "protocol: pgwire\nserve:\n    application:\n        name: db\n        port: pg\n"
	const fanout = "match: orders\ndistributed:\n    local: true\n"

	assert.NilError(t, compile("/channel/sync.yaml", conduit), "a channel names a protocol and one server")
	assert.NilError(t, compile("/stream/orders.yaml", fanout), "a stream matches a name")

	// The other way round is not a document either kind can read, which is what makes a
	// half-applied swap a failure instead of a silence.
	//
	// ONE offending key per probe, deliberately: strict mode walks the document's keys
	// in map order and reports the first one it does not know, so a body carrying two
	// of them names a different key run to run.
	assert.ErrorContains(t, compile("/stream/x.yaml", fanout+"protocol: pgwire\n"), "unknown key 'protocol'")
	assert.ErrorContains(t, compile("/channel/x.yaml", conduit+"match: orders\n"), "unknown key 'match'")

	// And the verbs, which is the other half of what each name means.
	dial := schema.OpsOf("channel")
	assert.Equal(t, len(dial), 1)
	assert.Equal(t, dial[0].Name, "dial")
	assert.Equal(t, dial[0].Effect, schema.Write)
	assert.Assert(t, !dial[0].Delivered, "nothing is pushed to a dialler")

	fan := schema.OpsOf("stream")
	assert.Equal(t, len(fan), 2)
	assert.Equal(t, fan[0].Name, "subscribe")
	assert.Assert(t, fan[0].Delivered, "a subscriber is handed the message; it does not go and ask")
	assert.Equal(t, fan[1].Name, "publish")
	assert.Equal(t, fan[1].Effect, schema.Write)
}

// A STREAM'S ENGINE IS A KEY FROM A CLOSED SET, and each branch owns its own fields.
// Same construct a database's engine is, for the same reason: an engine that is a free
// string or a reference to some other resource compiles when it names something that
// answers nothing, and the closed key is what makes that unwritable rather than
// merely detectable.
//
// The bridge booleans are gone with it. They are asserted absent rather than left to
// the comment, because a deleted field that quietly still loads is the deletion not
// having happened.
func TestStreamEngineIsClosedAndBranchScoped(t *testing.T) {
	compile := func(body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/secret/k.yaml",
			[]byte("id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/stream/s.yaml",
			[]byte("id: QmdmQXB2d1q5PVFHrpKgHqPnnGSuVYd3ZWZAJf1YXcvJcz\nmatch: orders\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		return err
	}

	assert.NilError(t, compile("distributed: {}\n"))
	assert.NilError(t, compile("distributed:\n    local: true\n"))
	assert.NilError(t, compile("kafka:\n    host: b:9092\n    credentials:\n        name: k\n"))
	assert.NilError(t, compile("nats:\n    host: n:4222\n    credentials:\n        name: k\n"))

	// A pubsub-shaped product tau has no client for is not a key that exists.
	assert.ErrorContains(t, compile("rabbitmq:\n    host: r:5672\n"), "unknown key 'rabbitmq'")
	// An engine has to be chosen; there is no unstated default.
	assert.ErrorContains(t, compile("description: nothing\n"), "attribute engine has an empty key")
	// Two of them is the switch refusing rather than a winner being picked.
	assert.ErrorContains(t, compile("distributed: {}\nkafka:\n    host: b:9092\n"),
		"ambiguous path: distributed and kafka each match")

	// Neither branch borrows the other's fields. `local` is a thing the swarm's own
	// gossipsub does and a Kafka cluster has no notion of; a connection is the reverse.
	assert.ErrorContains(t, compile("kafka:\n    host: b:9092\n    local: true\n"), "unknown key 'local'")
	assert.ErrorContains(t, compile("distributed:\n    host: b:9092\n"), "unknown key 'host'")
	assert.ErrorContains(t, compile("kafka:\n    credentials:\n        name: k\n"), "required attribute 'kafka/host'")
	assert.ErrorContains(t, compile("kafka:\n    host: b:9092\n"), "required attribute 'kafka/credentials/name'")

	// THE BRIDGES ARE DELETED, not merely undocumented.
	assert.ErrorContains(t, compile("distributed: {}\nbridges:\n    websocket:\n        enable: true\n"),
		"unknown key 'bridges'")

	// And the compiled shape: one `engine` key, and the branch's fields flat beside it.
	flat := compileBasic(t)
	orders := namedResource(t, resources(t, flat, "stream"), "orders")
	assert.Equal(t, orders["engine"], "distributed")
	assert.Equal(t, orders["local"], false)
	shipments := namedResource(t, resources(t, flat, "stream"), "shipments")
	assert.Equal(t, shipments["engine"], "kafka")
	assert.Equal(t, shipments["host"], "broker.example.com:9092")
	assert.Equal(t, shipments["tls"], "verify-full")
	_, leaked := shipments["local"]
	assert.Assert(t, !leaked, "a kafka-backed stream carries no claim about node-local scope")
}

// A BUS IS A STREAM WITHOUT DURABILITY, AND THE SCHEMA SAYS SO RATHER THAN THE PROSE.
// Two kinds that fan out to everyone attached will be read as duplicates and merged by
// somebody unless what separates them is structural, so this pins the three places it
// is: a bus can name no engine (every engine a stream has is a durable log, and a bus
// that rewinds is a stream wearing the wrong kind), a bus admits instances by validator
// where a stream matches a pattern, and a bus has a host — the websocket a browser
// attaches to — where a stream has none.
func TestABusIsAStreamWithoutDurability(t *testing.T) {
	compile := func(path, body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/function/f.yaml",
			[]byte("id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\nexecution: {call: c, timeout: 1s, memory: 1MB}\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, path,
			[]byte("id: QmYb8sCzT9dGqLxKRp7BwVn2jMhFcXPtUdEaWqRk4vNhJ5\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		return err
	}

	// A bus needs nothing at all: it is the swarm's, and there is nothing to choose.
	assert.NilError(t, compile("/bus/chat.yaml", "description: internal only\n"))

	// NO ENGINE AXIS. Every one of a stream's engines is a durable log, so none of them
	// is a key a bus can write — which is what stops a bus that rewinds existing.
	for _, engine := range []string{"kafka", "nats", "distributed"} {
		assert.ErrorContains(t, compile("/bus/chat.yaml", engine+":\n    host: b:9092\n"),
			"unknown key '"+engine+"'", "a bus must not be able to name %q", engine)
	}

	// The bus admits instances; the stream matches a pattern. Neither takes the other's.
	assert.NilError(t, compile("/bus/chat.yaml", "instances:\n    limit: 100\n    validator:\n        name: f\n"))
	assert.ErrorContains(t, compile("/bus/chat.yaml", "match: chat\n"), "unknown key 'match'")
	assert.ErrorContains(t, compile("/stream/s.yaml", "match: o\ndistributed: {}\ninstances:\n    validator:\n        name: f\n"),
		"unknown key 'instances'")

	// The websocket is an ADDRESS, so it is the same `host:` a gateway declares — and
	// the bus is what answers on it.
	assert.NilError(t, compile("/bus/chat.yaml", "host:\n    generated:\n        name: chat\n"))
	assert.ErrorContains(t, compile("/stream/s.yaml", "match: o\ndistributed: {}\nhost:\n    generated:\n        name: x\n"),
		"unknown key 'host'")
	// …and the deleted flag is not a spelling of it.
	assert.ErrorContains(t, compile("/bus/chat.yaml", "websocket: true\n"), "unknown key 'websocket'")

	chat := namedResource(t, resources(t, compileBasic(t), "bus"), "chat")
	assert.Equal(t, nested(chat, "host", "kind"), "generated")
	assert.Equal(t, nested(chat, "host", "name"), "chat")
	// It answers on a name, so a client is told what to dial rather than guessing.
	found := false
	for _, r := range schema.ReachableNames(rootOf(compileBasic(t))) {
		if r.Name == "chat" && r.Generated {
			found = true
		}
	}
	assert.Assert(t, found, "a bus with a host has to appear in ReachableNames")

	// The verbs are the stream's, deliberately: what you DO to a bus is what you do to
	// a topic, and the kinds differ in what happens afterwards.
	assert.DeepEqual(t, opsOf(t, "bus"), opsOf(t, "stream"))
	assert.Assert(t, schema.OpsOf("bus")[0].Delivered, "a subscriber is handed the message")
}

func opsOf(t *testing.T, kind string) []string {
	t.Helper()
	var out []string
	for _, v := range schema.OpsOf(kind) {
		out = append(out, v.Name+":"+v.Effect)
	}
	return out
}

// EVERY ENGINE KEY IS CLOSED, AND THERE IS NO LONGER AN OPEN KIND BESIDE THEM.
//
// This used to pin an asymmetry: engines closed, `connector.type` open, with the reason
// each way. Half of it is gone. A closed set is what makes a wrong pair unrepresentable
// and it is affordable exactly where the set is knowable — an engine is a wire protocol
// and a driver tau ships, so adding one is a release. "An external system you receive
// events from" is not knowable, which is why `connector` was open; but every case it
// carried is now written with kinds that already exist, so the kind is gone and the
// openness moved one construct out, to a workload's `egress` allowlist and a `secret`.
//
// BOTH HALVES ARE ASSERTED, because a deletion that quietly still loads is the deletion
// not having happened — the same reason the stream bridges are pinned absent below.
func TestEveryEngineIsClosedAndNoKindIsOpen(t *testing.T) {
	compile := func(path, body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/secret/k.yaml",
			[]byte("id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, path,
			[]byte("id: QmUNLLsPACCz1vLxQVkXqqLX5R1X345qqfHbsf67hvA3Nn\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		return err
	}

	// THE KIND IS GONE, and what that looks like is worth knowing: a directory named
	// after a kind the vocabulary does not have is walked past, so the document that
	// used to be the fixture's own connector compiles to NOTHING — no error, and no
	// resource. Strict keys are a property of a document the loader reads; a directory
	// nothing reads has no keys to be strict about.
	stray := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(stray, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
	assert.NilError(t, afero.WriteFile(stray, "/connector/c.yaml",
		[]byte("id: QmUNLLsPACCz1vLxQVkXqqLX5R1X345qqfHbsf67hvA3Nn\ntype: slack\npolls: true\n"), 0o644))
	sc, err := schema.New(schema.WithVirtual(stray, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := sc.Compile(context.Background())
	assert.NilError(t, err)
	_, present := rootOf(obj.Flat())["connector"]
	assert.Assert(t, !present, "a connector still compiles to a resource: %v", rootOf(obj.Flat()))

	// AND IT IS GONE FROM THE VOCABULARY ITSELF, which is the list every consumer walks
	// — the JSON Schema, completion, the graph. Asserted over V2Resources and over the
	// root the loader is built from, and NOT over `Kinds()`: that answers with the kinds
	// declaring Ops, which `connector` never did, so a check there would have passed on
	// the day the kind was written.
	for _, g := range schema.V2Resources {
		name, _ := g.Match.(string)
		assert.Assert(t, name != "connector", "connector is still a group in V2Resources")
	}
	for _, g := range schema.CompileRoot().Children {
		name, _ := g.Match.(string)
		assert.Assert(t, name != "connector", "connector is still a group of the compile root")
	}

	// AND THE ENGINES STAY CLOSED, which is the half that did not change. An engine is
	// a driver tau ships, so the set is knowable and the key is closed — which is what
	// makes naming something that answers nothing impossible rather than merely wrong.
	assert.ErrorContains(t, compile("/stream/s.yaml", "match: m\nslack:\n    host: h\n"), "unknown key 'slack'")
	assert.ErrorContains(t, compile("/database/d.yaml", "sql:\n    s3:\n        host: h\n"), "unknown key 's3'")

	// WHAT STAYS OPEN, and it is one construct further out: a workload names the host
	// it may reach and the secret it authenticates with, and nothing has to declare the
	// vendor as a resource first. This is the poller the fixture's connector became.
	assert.NilError(t, compile("/function/p.yaml",
		"execution:\n    call: p\n    timeout: 5s\n    memory: 8MB\negress:\n    - some-vendor-api.example.com\nsecret:\n    k:\n        ops: [get]\n"),
		"a workload reaching an external system needs no kind for it")
}

// A PLACEHOLDER THAT STAYS A PLACEHOLDER. `log` has no fields of its own on purpose —
// its shape is owed and has not been given — and the two things that could quietly
// happen to it are pinned here: somebody deleting it as inert, and somebody completing
// it by guessing.
//
// The verbs are the part that is not a placeholder. `append` alone is the kind's whole
// claim, and the absence of `put` and `delete` is where "append-only" is STATED rather
// than described: a record that can be overwritten or removed is not one.
func TestALogIsAppendOnlyAndOtherwiseUnspecified(t *testing.T) {
	assert.DeepEqual(t, opsOf(t, "log"), []string{"append:write", "read:read"})
	for _, gone := range []string{"put", "delete", "exec", "upsert"} {
		_, has := schema.EffectOf("log", gone)
		assert.Assert(t, !has, "an append-only log must not declare %q", gone)
	}

	// Nothing but the common block, so a field arriving here is a decision somebody
	// took and this is where they are asked to have taken it deliberately.
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/log/a.yaml",
		[]byte("id: QmPq5xVnB7LtWcHdKzR3JgEyUM8fasNbT2vXrQwCk6hDZm\nretention: 30d\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "unknown key 'retention'",
		"the shape is owed; a field guessed at here is not it")

	// And it is a real target: something appends to it in the fixture, which is the
	// difference between a kind and a name nothing can reach.
	audit := namedResource(t, resources(t, compileBasic(t), "log"), "audit")
	assert.Equal(t, audit["name"], "audit")
}

// A MULTI-INSTANCE STORE SAYS HOW MANY AND WHO, AND NEITHER IS OPTIONAL.
//
// Both hang off the one thing that knows the block was written — the `Captures()`
// switch on `instances/admission` — so this pins the three answers that shape gives and
// the two it refuses. A limit with no validator does not compile, because the block
// still owes its admission branch; a validator with no limit does not compile, because
// an unbounded store has to be a sentence somebody wrote. `0` is that sentence.
//
// And an instance name is a KEY: the pattern pair v1 carried has no spelling here, at
// the top level or inside the block. A pattern answers "which instance is this?" with a
// set, which is both ambiguous — two patterns claiming one name — and unindexable.
func TestInstancesTakeALimitAndAreNamedByAKey(t *testing.T) {
	compile := func(body string) (map[string]any, error) {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/function/f.yaml",
			[]byte("id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\nexecution: {call: c, timeout: 1s, memory: 1MB}\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/cache/c.yaml",
			[]byte("id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nsize: 1MB\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		obj, _, err := c.Compile(context.Background())
		if err != nil {
			return nil, err
		}
		return namedResource(t, resources(t, obj.Flat(), "cache"), "c"), nil
	}

	const validator = "    validator:\n        name: f\n"

	res, err := compile("instances:\n    limit: 10\n" + validator)
	assert.NilError(t, err)
	assert.Equal(t, nested(res, "instances", "limit"), 10)
	assert.Equal(t, nested(res, "instances", "admission"), "validator")

	// 0 IS AN ANSWER, not an absent one: no ceiling, said out loud.
	res, err = compile("instances:\n    limit: 0\n" + validator)
	assert.NilError(t, err)
	assert.Equal(t, nested(res, "instances", "limit"), 0)

	// One instance is the absent block, and nothing writes a limit onto it — a
	// Default() here would put a ceiling on every store that never asked for one.
	res, err = compile("")
	assert.NilError(t, err)
	_, present := res["instances"]
	assert.Assert(t, !present, "a store with no instances block carries no instance keys")

	// The two refusals, which are what makes the pair a decision rather than a shape.
	_, err = compile("instances:\n    limit: 10\n")
	assert.ErrorContains(t, err, "attribute instances/admission has an empty key",
		"a limit alone does not open the block: something still has to admit an instance")
	_, err = compile("instances:\n" + validator)
	assert.ErrorContains(t, err, "required attribute 'instances/limit'",
		"an unbounded store is a choice, so it has to be written")
	_, err = compile("instances:\n    limit: -1\n" + validator)
	assert.ErrorContains(t, err, "value must be >= 0", "a negative ceiling is not a spelling of no ceiling")

	// NO PATTERN, ANYWHERE. v1's pair is not a key of the block and not a key of the
	// store; an instance is addressed by an exact name or not at all.
	for _, pattern := range []string{"match: sess_*\n", "regex: true\n"} {
		_, err = compile(pattern)
		assert.ErrorContains(t, err, "unknown key", "a store must not match a pattern: %q", pattern)
		_, err = compile("instances:\n    limit: 10\n    " + pattern + validator)
		assert.ErrorContains(t, err, "unknown key", "an instance name is a key, not a pattern: %q", pattern)
	}
}

// EVERY KIND THAT SAYS WHERE IT RUNS, AND THE WHOLE LIST, so a kind joining or leaving
// it is a deliberate line in a diff rather than a side effect of declaring `Placed()`.
//
// The line is a PHYSICAL FOOTPRINT: something that runs, or bytes that sit somewhere.
// The four kinds a category would have dragged in are what the list is really about —
// `website` runs no process and its bytes are the CAS's, exactly as a function's wasm
// is; `model` is an endpoint somebody else operates, or a `local` application carrying
// its own placement; `channel` is a name two placed workloads meet at; `secret` holds
// no value. Placing any of them is a second, weaker statement about something already
// placed.
func TestRegionIsDeclaredOnKindsWithAFootprint(t *testing.T) {
	assert.DeepEqual(t, schema.PlacedKinds(), []string{
		"application", "function", "worker", "sandbox",
		"cache", "database", "storage", "volume", "log",
		"entity", "queue", "agent", "workflow",
	})

	placed := map[string]bool{}
	for _, kind := range schema.PlacedKinds() {
		placed[kind] = true
	}
	// BOTH SHAPES. V2Resources is what an author writes against; KindRoot is what a
	// lowered kind becomes, built from a COPY of the iterator's attributes — so a block
	// attached after that copy is taken exists only in the authored half, and an agent
	// or a workflow saying where it runs stops compiling in the pass that lowers it.
	for _, root := range [][]*engine.Node{schema.V2Resources, schema.KindRoot()} {
		for _, g := range root {
			kind, _ := g.Match.(string)
			fields := map[string]bool{}
			for _, a := range g.Children[0].Attributes {
				fields[a.Name] = true
			}
			for _, f := range []string{"region/policy", "region/name", "region/main", "region/standby"} {
				assert.Equal(t, fields[f], placed[kind], "%s carries %q; PlacedKinds says %v", kind, f, placed[kind])
			}
		}
	}
}

// THE PLACEMENT SWITCH, verified against the compiler rather than against the note that
// designed it. Three branches, `failover`'s two regions spelled by NAME so that "the
// first one is main" is not a position a reader has to know the significance of, and a
// `name` on the two branches where a single region means something and nowhere else.
func TestRegionPlacementIsASwitch(t *testing.T) {
	compile := func(body string) (map[string]any, error) {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/cache/c.yaml",
			[]byte("id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nsize: 1MB\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		obj, _, err := c.Compile(context.Background())
		if err != nil {
			return nil, err
		}
		return namedResource(t, resources(t, obj.Flat(), "cache"), "c"), nil
	}

	res, err := compile("region:\n    auto:\n        name: eu\n")
	assert.NilError(t, err)
	assert.Equal(t, nested(res, "region", "policy"), "auto")
	assert.Equal(t, nested(res, "region", "name"), "eu")

	// A FIELDLESS BRANCH STILL CAPTURES, so `cdn` needs no filler field to be sayable.
	res, err = compile("region:\n    cdn: {}\n")
	assert.NilError(t, err)
	assert.Equal(t, nested(res, "region", "policy"), "cdn")

	res, err = compile("region:\n    failover:\n        main: eu\n        standby: us\n")
	assert.NilError(t, err)
	assert.Equal(t, nested(res, "region", "policy"), "failover")
	assert.Equal(t, nested(res, "region", "main"), "eu")
	assert.Equal(t, nested(res, "region", "standby"), "us")

	// AN ABSENT BLOCK IS THE DEFAULT, and it writes nothing: `auto`, anywhere the
	// allowance permits, is what the common case gets for free.
	res, err = compile("")
	assert.NilError(t, err)
	_, present := res["region"]
	assert.Assert(t, !present, "an absent region block leaves no keys behind")

	// EXACTLY TWO, AND ONLY TWO. A third has no key to arrive under and a missing one
	// is a compile error — which is what "the first one is main" costs instead of a
	// list whose order is load-bearing.
	_, err = compile("region:\n    failover:\n        main: eu\n")
	assert.ErrorContains(t, err, "required attribute 'region/failover/standby'")
	_, err = compile("region:\n    failover:\n        standby: us\n")
	assert.ErrorContains(t, err, "required attribute 'region/failover/main'")
	_, err = compile("region:\n    failover:\n        main: eu\n        standby: us\n        third: ap\n")
	assert.ErrorContains(t, err, "unknown key 'third'")

	// `name` IS ON THE TWO BRANCHES A SINGLE REGION MEANS SOMETHING ON. On failover it
	// would name neither of the two, so the subset Either() refuses it.
	_, err = compile("region:\n    failover:\n        main: eu\n        standby: us\n        name: eu\n")
	assert.ErrorContains(t, err, "unknown key 'name'")

	// EXACTLY ONE BRANCH is a property of the declaration, not a check somebody wrote.
	_, err = compile("region:\n    auto:\n        name: eu\n    cdn: {}\n")
	assert.ErrorContains(t, err, "ambiguous path: auto and cdn each match")
	_, err = compile("region: {}\n")
	assert.ErrorContains(t, err, "attribute region/policy has an empty key")

	// The ALLOWANCE is a scope's word and never a resource's: a resource may not widen
	// or narrow what runs under it, because it is one of the things being placed.
	_, err = compile("regions: [eu]\n")
	assert.ErrorContains(t, err, "unknown key 'regions'")
}
