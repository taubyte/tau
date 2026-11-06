package schema_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

// compileBasic is the fixture as written, lowered resources left out; lowering
// tests ask for the lowered object by name.
func compileBasic(t *testing.T) map[string]any {
	t.Helper()
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Authored(context.Background())
	assert.NilError(t, err)
	return obj.Flat()
}

func TestFunctionHasNoTrigger(t *testing.T) {
	fn := namedResource(t, resources(t, compileBasic(t), "function"), "checkout")

	for _, gone := range []string{"type", "local", "protocol", "command", "method", "methods", "domains", "paths", "secure"} {
		_, present := fn[gone]
		assert.Assert(t, !present, "functions carries %q; the trigger block is meant to be deleted, not extended", gone)
	}

	// On a function `stream` is an access block keyed by stream name, not v1's
	// trigger scalar; the shape tells them apart.
	if ch, present := fn["stream"]; present {
		_, isBlock := ch.(map[string]any)
		assert.Assert(t, isBlock, "functions carries a scalar %q: that is v1's trigger coming back", "stream")
	}

	assert.Equal(t, nested(fn, "execution", "call"), "checkout")
	assert.Equal(t, nested(fn, "execution", "timeout"), int64(20000000000), "20s parses to nanoseconds")
	assert.Equal(t, nested(fn, "execution", "memory"), int64(32000000), "32MB parses to bytes")
}

func TestAPackageReferenceResolves(t *testing.T) {
	flat := compileBasic(t)
	paylib := ""
	for id, p := range resources(t, flat, "package") {
		if p.(map[string]any)["name"] == "paylib" {
			paylib = id
		}
	}
	assert.Assert(t, paylib != "", "no paylib package compiled")

	// two workloads, one artifact
	for _, name := range []string{"checkout", "edge_router"} {
		fn := namedResource(t, resources(t, flat, "function"), name)
		assert.DeepEqual(t, fn["package"], map[string]any{"name": paylib})
	}
}

func TestQueueDeadLetterResolves(t *testing.T) {
	queues := map[string]any{}
	for id, v := range resources(t, compileBasic(t), "sync") {
		if r, ok := v.(map[string]any); ok && r["type"] == "queue" {
			queues[id] = r["queue"]
		}
	}
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

func TestDatabaseEngineIsClosed(t *testing.T) {
	compile := func(body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
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

// The tier is the package's, so a workload says what it runs and what to call in it,
// and the compiler asks nothing about the tier itself; whether the call is needed is
// checkWorkloadPackages', which can see the package.
func TestAWorkloadNamesWhatItRuns(t *testing.T) {
	head := "id: " + cidFor("probe") + "\n"
	compile := func(kind, body string) (map[string]any, error) {
		flat, err := buildDoc(t, "/"+kind+"/probe.yaml", head+body)
		if err != nil {
			return nil, err
		}
		return namedResource(t, resources(t, flat, kind), "probe"), nil
	}
	const limits = "execution:\n    timeout: 20s\n    memory: 32MB\n"
	const called = "execution:\n    call: handle\n    timeout: 20s\n    memory: 32MB\n"

	for _, kind := range []string{"function", "worker"} {
		_, err := compile(kind, limits)
		assert.ErrorContains(t, err, "required attribute 'package/name'", "%s: what runs has to be named", kind)

		_, err = compile(kind, "runtime: wasm\npackage:\n    name: redact_code\n"+called)
		assert.ErrorContains(t, err, "unknown key 'runtime'", "%s: the tier is the package's", kind)

		res, err := compile(kind, "package:\n    name: redact_code\n"+called)
		assert.NilError(t, err)
		assert.Equal(t, nested(res, "execution", "call"), "handle")

		res, err = compile(kind, "package:\n    name: redact_code\n"+limits)
		assert.NilError(t, err, "%s: an entrypoint is a check, not a load error", kind)
		_, defaulted := nestedOK(res, "execution", "call")
		assert.Assert(t, !defaulted, "%s: no tier's convention is written into the object", kind)
	}
}

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

func TestRequiredOnOneEngineBranchIsEnforced(t *testing.T) {
	compile := func(body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
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

	// No default on `metric`: it would land on every non-vector database.
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
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

func TestDatabaseConsensusIsUnderTheDistributedEngineOnly(t *testing.T) {
	compile := func(path, body string) (map[string]any, error) {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
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
	type testCase struct{ typ, body, want string }
	for _, tc := range []testCase{
		{"kv", "kv:\n    distributed:\n        size: 1MB\n        consensus: crdt\n", "crdt"},
		{"document", "document:\n    distributed:\n        size: 1MB\n        consensus: raft\n", "raft"},
		{"sql", "sql:\n    distributed:\n        size: 1MB\n        consensus: raft\n", "raft"},
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

	// A cache asks the same question at the document root; it has no engine axis.
	flat, err := compile("/cache/c.yaml", "size: 1MB\nconsensus: crdt\n")
	assert.NilError(t, err)
	assert.Equal(t, namedResource(t, resources(t, flat, "cache"), "c")["consensus"], "crdt")
	_, err = compile("/cache/c.yaml", "distributed:\n    size: 1MB\n    consensus: crdt\n")
	assert.ErrorContains(t, err, "unknown key 'distributed'",
		"a cache has no engine block for this to sit in")
}

func TestCacheSaysWhereItsDataLives(t *testing.T) {
	compile := func(path, body string) (map[string]any, error) {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
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
		flat, err := compile("/cache/c.yaml", "consensus: raft\n"+body)
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

	// A database has no such key: it chooses consensus alone.
	_, err = compile("/database/d.yaml", "kv:\n    distributed:\n        size: 1MB\n        consensus: crdt\n        replicated: true\n")
	assert.ErrorContains(t, err, "unknown key 'replicated'")
	_, err = compile("/database/d.yaml", "replicated: true\nkv:\n    distributed:\n        size: 1MB\n        consensus: crdt\n")
	assert.ErrorContains(t, err, "unknown key 'replicated'")

	// And the fixture carries the opt-in on the cache where it is the point, with the
	// rest of them silent — so "absent is legal" is held by every other cache in the
	// project rather than by an assertion about one.
	basic := compileBasic(t)
	orders := namedResource(t, resources(t, basic, "cache"), "orders")
	assert.Equal(t, orders["replicated"], true)
}

func TestCacheConsensusIsRequiredAndTakesTheLossyValue(t *testing.T) {
	compile := func(path, body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, path,
			[]byte("id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		return err
	}

	// WRITING NOTHING IS REFUSED, and the error names the field.
	assert.ErrorContains(t, compile("/cache/c.yaml", "size: 1MB\n"), "required attribute 'consensus'")
	assert.ErrorContains(t, compile("/cache/c.yaml", "size: 1MB\nconsensus:\n"), "required attribute 'consensus'",
		"the key with nothing after it is the same omission")

	// ALL THREE ANSWERS ARE SAYABLE.
	for _, v := range []string{"raft", "crdt", "gossip"} {
		assert.NilError(t, compile("/cache/c.yaml", "size: 1MB\nconsensus: "+v+"\n"), "consensus: %q", v)
	}

	// AND NOTHING ELSE IS — including `single-writer`, which is what `raft` used to be
	// spelled, the underscored near-miss, and the bool this replaced.
	for _, v := range []string{"single-writer", "eventual", "true", "single_writer", "gossip "} {
		assert.ErrorContains(t, compile("/cache/c.yaml", "size: 1MB\nconsensus: \""+v+"\"\n"),
			"invalid value", "consensus: %q is not one of the three answers", v)
	}

	// A database refuses the lossy one.
	assert.ErrorContains(t, compile("/database/d.yaml", "kv:\n    distributed:\n        size: 1MB\n        consensus: gossip\n"),
		"invalid value", "a system of record must not be able to lose a write")

	// And the fixture spends all three, on the caches where each is the point.
	flat := compileBasic(t)
	caches := resources(t, flat, "cache")
	assert.Equal(t, namedResource(t, caches, "orders")["consensus"], "gossip")

	root := rootOf(flat)
	shop := namespace(t, root, "shop")
	assert.Equal(t, namedResource(t, shop["cache"].(map[string]any), "sessions")["consensus"], "raft")
	billing := namespace(t, root, "billing")
	assert.Equal(t, namedResource(t, billing["cache"].(map[string]any), "sessions")["consensus"], "crdt")
}

func TestDatabaseConsensusIsRequiredAndClosed(t *testing.T) {
	compile := func(body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/database/d.yaml",
			[]byte("id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		return err
	}

	// WRITING NOTHING IS REFUSED, on every type, and the error names the field rather
	// than the resource: the author is told which line to add and where.
	type testCase struct{ typ, body string }
	for _, tc := range []testCase{
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

	// A value outside the set is refused, including the old `single-writer`.
	for _, v := range []string{"single-writer", "true", "single_writer", "eventual", "crdt "} {
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
	assert.Equal(t, nested(ledger, "sql", "consensus"), "raft")
}

func TestNewKindsCompile(t *testing.T) {
	flat := compileBasic(t)

	assert.Equal(t, only(t, resources(t, flat, "cron"))["schedule"], "0 3 * * *")
	assert.Equal(t, only(t, resources(t, flat, "cache"))["ttl"], int64(86400000000000), "24h in nanoseconds")
	assert.Equal(t, namedResource(t, resources(t, flat, "secret"), "stripe")["source"], "value", "default applies")
}

func TestNamespaceScope(t *testing.T) {
	obj := root(t, compileBasic(t))
	shop := namespace(t, obj, "shop")

	fn := namedResource(t, shop["function"].(map[string]any), "notify")
	_, hasSource := fn["source"]
	assert.Assert(t, !hasSource, "a resource built from its own code writes no source block")
	assert.Equal(t, nested(fn, "execution", "call"), "notify")

	kv := only(t, shop["cache"].(map[string]any))
	assert.Equal(t, kv["name"], "sessions")

	flat := compileBasic(t)
	app := namedResource(t, resources(t, flat, "application"), "api")
	image := namedResource(t, resources(t, flat, "package"), "api_image")
	assert.Equal(t, nested(image, "source", "image"), "ghcr.io/acme/api:1.4")
	assert.Assert(t, nested(app, "package", "name") != nil, "applications is a compute kind, not a scope")
	// what the platform assigns is never authored: the port is handed to the
	// workload and tunnelled, and how many instances run is placement
	for _, gone := range []string{"port", "replicas"} {
		_, present := app[gone]
		assert.Assert(t, !present, "%q must not be authorable", gone)
	}
}

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
	pkg, _ := fn["package"].(map[string]any)
	assert.Assert(t, pkg != nil && strings.HasPrefix(pkg["name"].(string), "Qm"),
		"refs still resolve: %v", fn["package"])

	// and a verb the kind does not implement is refused at load
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/function/checkout.yaml", []byte(`
id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh
package:
    name: redact_code
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

func TestGatewayRoute(t *testing.T) {
	flat := compileBasic(t)

	gw := namedResource(t, resources(t, flat, "gateway"), "shop")
	// The name names a zone plus a label, not a flat fqdn.
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
	// An unlisted handler key is refused as unknown, at the offending line.
	assert.ErrorContains(t, err, "unknown key 'kv'",
		"a real but non-HTTP kind must not be a handler")
	assert.ErrorContains(t, err, "agent, application, bus, function, gateway, mcp, package",
		"the refusal has to say what a handler may be")
	// the position is the value's, so for a key holding a block it lands on the
	// first line inside that block rather than on the key
	assert.ErrorContains(t, err, "/gateway/shop.yaml:7:",
		"and where")
}

func TestNamespacesNest(t *testing.T) {
	shop := namespace(t, root(t, compileBasic(t)), "shop")

	inner, ok := shop["namespace"].(map[string]any)
	assert.Assert(t, ok, "a namespace must be able to hold namespaces")
	local := only(t, inner)
	assert.Equal(t, local["name"], "local")

	// a third `sessions`, two levels down, distinct from both above it
	kv := only(t, local["cache"].(map[string]any))
	assert.Equal(t, kv["name"], "sessions")
	assert.Equal(t, nested(kv, "size"), int64(2000000))
}

func TestDeepNamespaceRefsAreNotResolved(t *testing.T) {
	flat := compileBasic(t)

	// the contrast first: a first-level namespace declares access like anywhere else
	shop := namespace(t, root(t, flat), "shop")
	notify := namedResource(t, shop["function"].(map[string]any), "notify")
	sessions := notify["cache"].(map[string]any)["sessions"].(map[string]any)
	assert.DeepEqual(t, sessions["ops"], []string{"get"})

	// two levels down there is nothing to resolve against yet, so the assertion is
	// simply that the scope compiles at all and holds its own resources
	local := only(t, shop["namespace"].(map[string]any))
	assert.Equal(t, local["name"], "local")
	assert.Equal(t, len(local["cache"].(map[string]any)), 1)
}

func TestEffectClasses(t *testing.T) {
	type testCase struct {
		kind, verb, effect string
	}
	for _, c := range []testCase{
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
	for _, kind := range []string{"function", "application", "package", "gateway", "mcp"} {
		for _, m := range []string{"GET", "POST"} {
			_, ok := schema.EffectOf(kind, m)
			assert.Assert(t, !ok, "%s must not carry the HTTP method %q as an op", kind, m)
		}
	}
}

// `namespaces` is a container group: each instance holds a nested copy of every
// resource group, its own scope.

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

func TestSiblingNamespaceIsNameableAndUnchecked(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))

	// shop reaches into billing's books
	assert.NilError(t, afero.WriteFile(fs, "/namespace/shop/function/notify.yaml", []byte(`
id: QmbWqxBEKC3P8tqsKc98xmWNzrzDtRLMiMPL8wBuTGsMnR
package:
    name: redact_code
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

func TestZoneIsNotRouting(t *testing.T) {
	flat := compileBasic(t)

	zone := only(t, resources(t, flat, "zone"))
	assert.Equal(t, zone["fqdn"], "example.com")
	// A file, not lines in yaml: $ORIGIN, $TTL, multi-line SOA and `@` survive
	// because the file is what is served.
	assert.Equal(t, zone["file"], "dns/example.com.zone")

	// A zone still carries no ROUTING -- it says what a name resolves to, not
	// where a request goes -- and this fixture's zone reaches nothing.
	for k := range zone {
		assert.Assert(t, !strings.HasPrefix(k, "use-"), "a zone declared %q", k)
	}

	// Nothing points at a zone: it is an ingress, so no verbs and no authorizer. A
	// record unknown at deploy is the resolver's.
	_, ok := schema.EffectOf("zone", "get")
	assert.Assert(t, !ok, "zones must not be a target kind")

	// and the gateway that serves a name under it still holds the ingress
	gw := namedResource(t, resources(t, flat, "gateway"), "shop")
	assert.Equal(t, nested(gw, "host", "name"), "shop")
}

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
	local := only(t, shop["namespace"].(map[string]any))
	relay := only(t, local["function"].(map[string]any))
	assert.Equal(t, relay["database"].(map[string]any)["ledger"].(map[string]any)["namespace"], "/billing")

	// and the shape is checked at load
	for _, bad := range []string{"shop/", "shop//local", "../billing", "Shop-Local"} {
		fs := afero.NewMemMapFs()
		assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
		assert.NilError(t, afero.WriteFile(fs, "/function/checkout.yaml", []byte(
			"id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\n"+
				"package:\n    name: redact_code\n"+
				"execution: {call: c, timeout: 1s, memory: 1MB}\n"+
				"cache:\n    orders:\n        namespace: "+bad+"\n        ops: [get]\n"), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		assert.ErrorContains(t, err, "namespace path", "%q must be refused", bad)
	}
}

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

func TestEveryWorkloadCanDeclareEgress(t *testing.T) {
	for _, kind := range []string{"application", "function", "worker", "sandbox"} {
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

func TestCronCarriesItsTimezone(t *testing.T) {
	cron := only(t, resources(t, compileBasic(t), "cron"))
	assert.Equal(t, cron["schedule"], "0 3 * * *")
	assert.Equal(t, cron["timezone"], "Europe/Paris", "the zone the schedule is written in")
	assert.Equal(t, cron["on_overlap"], "skip", "the only choice that cannot make things worse")

	// and a cron that says nothing gets the zone where every day has 24 hours
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/cron/c.yaml",
		[]byte("id: QmRAQB6YaCyidP37UdDnjFY5vQuiBrcqdyoW1CuDgwxkD4\nschedule: \"@daily\"\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	assert.Equal(t, only(t, resources(t, obj.Flat(), "cron"))["timezone"], "UTC")
}

func TestExternalDatabaseMustStateItsTransport(t *testing.T) {
	orders := namedResource(t, resources(t, compileBasic(t), "database"), "orders")
	assert.Equal(t, nested(orders, "sql", "tls"), "verify-full")

	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
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

func TestWorkloadVariables(t *testing.T) {
	api := namedResource(t, resources(t, compileBasic(t), "application"), "api")
	vars, ok := api["variables"].(map[string]any)
	assert.Assert(t, ok, "no variables block")

	// a literal is in git and says so
	assert.Equal(t, vars["LOG_LEVEL"].(map[string]any)["value"], "info")

	// a secret is never a variable: the workload reads it through the vault, under
	// the grant it declares
	_, handedOver := vars["STRIPE_KEY"]
	assert.Assert(t, !handedOver, "a secret was handed over as a variable: %v", vars)
	assert.DeepEqual(t, nested(api, "secret", "stripe", "ops"), []string{"get"})

	// every workload kind can be configured, not just the container one
	for _, kind := range []string{"application", "function", "worker", "sandbox"} {
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

func TestDefaultsThatWereUnreachable(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/package/img.yaml", []byte(standingPackages["/package/img.yaml"]), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/application/a.yaml", []byte(
		"id: QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY\npackage:\n    name: img\nexecution:\n    memory: 1MB\nports:\n    p:\n        number: 80\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/sync/q.yaml",
		[]byte("id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nqueue:\n    consensus: raft\n"), 0o644))
	// A log beside it, so the queue's defaults are shown to land on the queue only.
	assert.NilError(t, afero.WriteFile(fs, "/sync/l.yaml",
		[]byte("id: QmPq5xVnB7LtWcHdKzR3JgEyUM8fasNbT2vXrQwCk6hDZm\nlog:\n    consensus: crdt\n"), 0o644))
	// a generated host needs no certificate even when TLS stops here — the cloud
	// holds a wildcard for its own zone, which is the gateway's rule too
	assert.NilError(t, afero.WriteFile(fs, "/proxy/p.yaml", []byte(
		"id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nhost:\n    generated:\n        name: db\ntls:\n    mode: terminate\nforward:\n    application:\n        name: a\n        port: p\n"), 0o644))

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	q := only(t, byType(t, obj.Flat(), "queue"))
	assert.Equal(t, nested(q, "queue", "visibility_timeout"), int64(30000000000),
		"the default has to be reachable")

	l := only(t, byType(t, obj.Flat(), "log"))
	_, strayed := l["queue"]
	assert.Assert(t, !strayed, "a queue default landed on a log: %v", l)

	p := only(t, resources(t, obj.Flat(), "proxy"))
	assert.Equal(t, nested(p, "certificate", "source"), "generated")
}

func TestWorkflowLowers(t *testing.T) {
	plan := schema.LoweringPlan(compileBasic(t))

	got := map[string]string{}
	for _, d := range plan {
		got[d.Kind+"/"+d.Name] = d.Why
	}
	assert.Assert(t, got["sync/checkout_reserve"] != "", "one queue per step: %v", got)
	assert.Assert(t, got["sync/checkout_charge"] != "")
	assert.Assert(t, got["sync/checkout_settle"] != "", "a wait is a step, so it becomes a queue and a driver like any other: %v", got)
	assert.Assert(t, got["sync/checkout_failed"] != "", "somewhere for a step that gave up")

	// One driver per step, named after the step.
	assert.Assert(t, got["function/checkout_reserve"] != "", "one driver per step: %v", got)
	assert.Assert(t, got["function/checkout_charge"] != "")
	assert.Assert(t, got["function/checkout_settle"] != "")
	// And nothing else: no completion stream.
	for k := range got {
		assert.Assert(t, !strings.HasPrefix(k, "stream/"),
			"a workflow produced a stream nobody asked for: %v", got)
	}

	// The whole produced set, not the absence of one kind: the run's state is the
	// message.
	var produced []string
	for _, d := range plan {
		if d.Of == "workflow/checkout" {
			produced = append(produced, d.Kind+"/"+d.Name)
		}
	}
	sort.Strings(produced)
	assert.DeepEqual(t, produced, []string{
		"function/checkout_charge", "function/checkout_reserve", "function/checkout_settle",
		"package/checkout_driver",
		"sync/checkout_charge", "sync/checkout_failed", "sync/checkout_reserve",
		"sync/checkout_settle",
	})

	// Every write the plan carries, keyed by what it lands on and where.
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
	assert.Equal(t, writes["function/checkout_reserve sync/checkout_reserve/ops"], "receive")

	// FORWARD PROGRESS IS ONE GRANT. The driver sends to exactly one queue — the next
	// step's — so "which of these means next" is not a question anybody has to answer by
	// reading names. The last step has no next and the write is dropped.
	assert.Equal(t, writes["function/checkout_reserve sync/checkout_settle/ops"], "send")
	assert.Equal(t, writes["function/checkout_charge sync/checkout_charge/ops"], "receive")
	for k, v := range writes {
		assert.Assert(t, !strings.HasPrefix(k, "function/checkout_charge sync/") || v != "send",
			"the last step sends nowhere: %s", k)
	}

	// AND THE WORK ITSELF IS AN INVOKE, out to the function the author wrote. It is the
	// one edge per step that leaves the box, and it is an ownership boundary: their
	// function is theirs, and other things may call it too.
	assert.Equal(t, writes["function/checkout_reserve function/reserve_stock/ops"], "invoke")

	// The author's function gains no pipeline grants.
	for k := range writes {
		assert.Assert(t, !strings.HasPrefix(k, "function/reserve_stock ") && !strings.HasPrefix(k, "function/charge_card "),
			"the step's own function was written into: %s", k)
	}

	// The dead letter is the queue's own field, not an entry.
	for _, d := range plan {
		if d.Kind != "sync" || d.Name != "checkout_reserve" {
			continue
		}
		assert.Equal(t, d.Fields["queue/dead_letter/name"], "checkout_failed")
		for _, w := range d.Writes {
			assert.Assert(t, !strings.Contains(strings.Join(w.Path, "/"), "checkout_failed"),
				"the dead-letter is a field, not an entry")
		}
	}
}

func TestEveryLoweringNamesARealKind(t *testing.T) {
	assert.DeepEqual(t, interp.CheckLowerings(schema.CompileRoot()), []string(nil))
}

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
	src := find("function", "package/name")
	assert.Equal(t, src.Kind, "package")
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

	// The inbound blocks are derived from the references. `resolver` is reached like
	// a router; an http-authorizer is a guard, not a destination, so it is absent.
	assert.DeepEqual(t, schema.InboundBlocks(), []string{"forward", "resolver", "route/handler", "route/router", "router", "serve"})

	// how an access entry is written, and where a document lives
	access := schema.AccessEntryShape()
	assert.Equal(t, access.Ops, "ops")
	assert.Equal(t, access.Namespace, "namespace")

	layout := schema.LayoutOf()
	assert.Equal(t, layout.Resource, "{kind}/{name}.yaml")
	assert.Equal(t, layout.Scope, "{kind}/{name}")
}

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

func TestACrossNamespaceAccessIsNotAuthorised(t *testing.T) {
	flat := compileBasic(t)
	shop := namespace(t, root(t, flat), "shop")
	local := namespace(t, shop, "local")
	relay := namedResource(t, resources(t, local, "function"), "relay")

	// A SIBLING, which is the case v1 could not write down. /shop/local reaching
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

func TestAResourceNameIsNotValidated(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/function/paid-tier.yaml", []byte(
		"id: QmWQ7XwnqQzhLTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPYuB\n"+
			"package:\n    name: redact_code\n"+
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

func TestADurableKvHasNoLifetime(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/database/d.yaml",
		[]byte("id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nkv:\n    distributed:\n        size: 1MB\n        ttl: 24h\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "unknown key 'ttl'")

	// …while the cache beside it is where a lifetime belongs, and still takes one.
	assert.Equal(t, only(t, resources(t, compileBasic(t), "cache"))["ttl"], int64(86400000000000))
}

func TestChannelIsOneToOneAndStreamFansOut(t *testing.T) {
	const id = "id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEC\n"
	compile := func(path, body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/package/img.yaml", []byte(standingPackages["/package/img.yaml"]), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/application/db.yaml",
			[]byte("id: QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY\npackage:\n    name: img\nexecution:\n    memory: 1MB\nports:\n    pg:\n        number: 5432\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, path, []byte(id+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		return err
	}
	const conduit = "protocol: pgwire\nserve:\n    application:\n        name: db\n        port: pg\n"
	const fanout = "distributed: {}\n"

	assert.NilError(t, compile("/channel/sync.yaml", conduit), "a channel names a protocol and one server")
	assert.NilError(t, compile("/stream/orders.yaml", fanout), "a stream is named by its own name")

	// One offending key per probe: strict mode reports the first unknown key in map
	// order.
	assert.ErrorContains(t, compile("/stream/x.yaml", fanout+"protocol: pgwire\n"), "unknown key 'protocol'")
	assert.ErrorContains(t, compile("/channel/x.yaml", conduit+"distributed: {}\n"), "unknown key 'distributed'")

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

func TestStreamEngineIsClosedAndBranchScoped(t *testing.T) {
	compile := func(body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/secret/k.yaml",
			[]byte("id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/stream/s.yaml",
			[]byte("id: QmdmQXB2d1q5PVFHrpKgHqPnnGSuVYd3ZWZAJf1YXcvJcz\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		return err
	}

	assert.NilError(t, compile("distributed: {}\n"))
	assert.ErrorContains(t, compile("distributed:\n    local: true\n"), "unknown key 'local'", "a stream is one topic across the swarm, with no node-local scope to declare")
	assert.NilError(t, compile("kafka:\n    host: b:9092\n    credentials:\n        name: k\n"))
	assert.NilError(t, compile("nats:\n    host: n:4222\n    credentials:\n        name: k\n"))

	// A pubsub-shaped product tau has no client for is not a key that exists.
	assert.ErrorContains(t, compile("rabbitmq:\n    host: r:5672\n"), "unknown key 'rabbitmq'")
	// An engine has to be chosen; there is no unstated default.
	assert.ErrorContains(t, compile("description: nothing\n"), "attribute engine has an empty key")
	// Two of them is the switch refusing rather than a winner being picked.
	assert.ErrorContains(t, compile("distributed: {}\nkafka:\n    host: b:9092\n"),
		"ambiguous path: distributed and kafka each match")

	// Neither branch borrows the other's fields: a connection is an external engine's.
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
	_, kept := orders["local"]
	assert.Assert(t, !kept, "a stream has no node-local scope to declare")
	shipments := namedResource(t, resources(t, flat, "stream"), "shipments")
	assert.Equal(t, shipments["engine"], "kafka")
	assert.Equal(t, shipments["host"], "broker.example.com:9092")
	assert.Equal(t, shipments["tls"], "verify-full")
}

func TestAStreamIsNamedNotMatched(t *testing.T) {
	compile := func(body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/stream/orders.yaml",
			[]byte("id: QmdmQXB2d1q5PVFHrpKgHqPnnGSuVYd3ZWZAJf1YXcvJcz\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		return err
	}

	// NEITHER HALF OF THE PAIR LOADS, and a literal spelling is refused with the same
	// error a pattern is: the field is gone, not narrowed.
	for _, body := range []string{"distributed: {}\nmatch: orders\n", "distributed: {}\nmatch: ord_*\n", "distributed: {}\nregex: true\n"} {
		assert.ErrorContains(t, compile(body), "unknown key", "a stream still takes %q", body)
	}

	// Nothing is owed in its place: a stream with only an engine is complete.
	assert.NilError(t, compile("distributed: {}\n"))

	// THE NAME IS THE HANDLE, and it comes from the file rather than from a field: the
	// fixture writes no `name:` on any stream and every one of them is found by one.
	flat := compileBasic(t)
	streams := resources(t, flat, "stream")
	orders := namedResource(t, streams, "orders")
	for _, gone := range []string{"match", "regex"} {
		_, present := orders[gone]
		assert.Assert(t, !present, "a compiled stream still carries %q: %v", gone, orders)
	}

	// And it is the name an access block reaches it by — the same string, resolved
	// through the index rather than matched against a pattern.
	publisher := namedResource(t, resources(t, flat, "function"), "checkout")
	published, _ := publisher["stream"].(map[string]any)
	entry, _ := published["orders"].(map[string]any)
	assert.Assert(t, entry != nil, "nothing publishes to stream/orders by name: %v", publisher["stream"])
	assert.DeepEqual(t, entry["ops"], []string{"publish"})
}

func TestABusIsAStreamThatCannotBecomeALog(t *testing.T) {
	compile := func(path, body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/package/wasm.yaml", []byte(standingPackages["/package/wasm.yaml"]), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/function/f.yaml",
			[]byte("id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\npackage:\n    name: wasm\nexecution: {call: c, timeout: 1s, memory: 1MB}\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, path,
			[]byte("id: QmYb8sCzT9dGqLxKRp7BwVn2jMhFcXPtUdEaWqRk4vNhJ5\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		return err
	}

	// A bus needs nothing at all: it is the swarm's, and there is nothing to choose.
	assert.NilError(t, compile("/bus/chat.yaml", "description: internal only\n"))

	// NO ENGINE AXIS — not one key, `distributed` included, which is the counterexample
	// the loop below names out loud. A bus has nothing to choose, so what it cannot do
	// is what a stream can: name a log and move onto it.
	for _, engine := range []string{"kafka", "nats", "distributed"} {
		assert.ErrorContains(t, compile("/bus/chat.yaml", engine+":\n    host: b:9092\n"),
			"unknown key '"+engine+"'", "a bus must not be able to name %q", engine)
	}

	// THE BUS ADMITS INSTANCES AND THE STREAM IS ONE TOPIC. Neither takes the other's,
	// and neither matches a pattern any more: v1's `match`/`regex` is gone from both,
	// which is what makes "which topic is this?" a name in every kind here.
	assert.NilError(t, compile("/bus/chat.yaml", "instances:\n    limit: 100\n    validator:\n        name: f\n"))
	for _, kind := range []string{"/bus/chat.yaml", "/stream/s.yaml"} {
		for _, pattern := range []string{"match: chat\n", "regex: true\n"} {
			assert.ErrorContains(t, compile(kind, pattern), "unknown key",
				"%s still takes %q", kind, pattern)
		}
	}
	assert.ErrorContains(t, compile("/stream/s.yaml", "distributed: {}\ninstances:\n    validator:\n        name: f\n"),
		"unknown key 'instances'")

	// A bus owns no name: it is reached by a route naming it as the handler.
	assert.ErrorContains(t, compile("/bus/chat.yaml", "host:\n    generated:\n        name: chat\n"),
		"unknown key 'host'")
	assert.ErrorContains(t, compile("/stream/s.yaml", "distributed: {}\nhost:\n    generated:\n        name: x\n"),
		"unknown key 'host'")
	// …and the deleted flag is not a spelling of it.
	assert.ErrorContains(t, compile("/bus/chat.yaml", "websocket: true\n"), "unknown key 'websocket'")

	chat := namedResource(t, resources(t, compileBasic(t), "bus"), "chat")
	assert.Assert(t, nested(chat, "host", "kind") == nil, "a bus owns no name of its own")

	// The reachable name is the gateway's that routes to the bus.
	found := false
	for _, r := range schema.ReachableNames(rootOf(compileBasic(t))) {
		if r.Name == "presence" && r.Generated {
			found = true
		}
	}
	assert.Assert(t, found, "the gateway that routes to a bus has to appear in ReachableNames")

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

func TestEveryEngineIsClosedAndNoKindIsOpen(t *testing.T) {
	compile := func(path, body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/secret/k.yaml",
			[]byte("id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/package/wasm.yaml", []byte(standingPackages["/package/wasm.yaml"]), 0o644))
		assert.NilError(t, afero.WriteFile(fs, path,
			[]byte("id: QmUNLLsPACCz1vLxQVkXqqLX5R1X345qqfHbsf67hvA3Nn\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		return err
	}

	// A directory named after a kind the vocabulary lacks is walked past: no error,
	// no resource.
	stray := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(stray, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
	assert.NilError(t, afero.WriteFile(stray, "/connector/c.yaml",
		[]byte("id: QmUNLLsPACCz1vLxQVkXqqLX5R1X345qqfHbsf67hvA3Nn\ntype: slack\npolls: true\n"), 0o644))
	sc, err := schema.New(schema.WithVirtual(stray, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := sc.Compile(context.Background())
	assert.NilError(t, err)
	_, present := rootOf(obj.Flat())["connector"]
	assert.Assert(t, !present, "a connector still compiles to a resource: %v", rootOf(obj.Flat()))

	// Asserted over V2Resources and the loader's root, not Kinds(), which lists only
	// kinds declaring Ops.
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
	assert.ErrorContains(t, compile("/stream/s.yaml", "slack:\n    host: h\n"), "unknown key 'slack'")
	assert.ErrorContains(t, compile("/database/d.yaml", "sql:\n    s3:\n        host: h\n"), "unknown key 's3'")

	// What stays open: a workload names the host it reaches and the secret it uses.
	assert.NilError(t, compile("/function/p.yaml",
		"package:\n    name: wasm\nexecution:\n    call: p\n    timeout: 5s\n    memory: 8MB\negress:\n    - some-vendor-api.example.com\nsecret:\n    k:\n        ops: [get]\n"),
		"a workload reaching an external system needs no kind for it")
}

func TestThereIsNoEntityKind(t *testing.T) {
	for _, g := range schema.V2Resources {
		name, _ := g.Match.(string)
		assert.Assert(t, name != "entity", "entity is still a group in V2Resources")
	}
	for _, g := range schema.CompileRoot().Children {
		name, _ := g.Match.(string)
		assert.Assert(t, name != "entity", "entity is still a group of the compile root")
	}
	for _, g := range schema.KindRoot() {
		name, _ := g.Match.(string)
		assert.Assert(t, name != "entity", "entity is still a group of the lowered root")
	}
	for _, k := range schema.Kinds() {
		assert.Assert(t, k != "entity", "entity still declares verbs")
	}

	// Silent rather than an error, which is why it is asserted.
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/entity/counter.yaml", []byte(
		"id: QmUNLLsPACCz1vLxQVkXqqLX5R1X345qqfHbsf67hvA3Nn\n"+
			"execution:\n    call: tick\n    timeout: 5s\n    memory: 8MB\n"+
			"durable: true\nsize: 64MB\n"), 0o644))
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	_, present := root(t, obj.Flat())["entity"]
	assert.Assert(t, !present, "an entity still compiles to a resource: %v", root(t, obj.Flat()))

}

func TestNoShippedStringNamesADeletedKind(t *testing.T) {
	b, err := schema.JSONSchema()
	assert.NilError(t, err)
	shipped := string(b)

	// The plurals too: access-block descriptions were generated from them.
	for _, gone := range []string{"entity", "entities", "connector", "connectors", "smartops"} {
		word := regexp.MustCompile(`(?i)\b` + gone + `\b`)
		hit := word.FindStringIndex(shipped)
		assert.Assert(t, hit == nil, "the shipped schema still names %q, a kind this vocabulary does not have: %s",
			gone, excerpt(shipped, hit))
	}

	// AND THE CHECK IS LIVE, which for a needle-in-a-haystack assertion is the half worth
	// proving: the same scan finds a kind that IS here, so a silent pass would be a bug in
	// the scan rather than a clean schema.
	for _, here := range []string{"sync", "cache", "agent"} {
		word := regexp.MustCompile(`(?i)\b` + here + `\b`)
		assert.Assert(t, word.FindStringIndex(shipped) != nil,
			"the scan cannot find %q, which is a kind this vocabulary has", here)
	}
}

// excerpt is the text around a match, so a failure says which sentence rather than that
// there is one somewhere in 400KB.
func excerpt(s string, at []int) string {
	if at == nil {
		return ""
	}
	lo, hi := at[0]-120, at[1]+120
	if lo < 0 {
		lo = 0
	}
	if hi > len(s) {
		hi = len(s)
	}
	return "…" + s[lo:hi] + "…"
}

func TestALogIsAppendOnlyAndOtherwiseUnspecified(t *testing.T) {
	ops, known := schema.SyncTypeOf("log")
	assert.Assert(t, known, "the log type is gone")
	assert.DeepEqual(t, ops.Names(), []string{"append", "read"})

	for _, gone := range []string{"put", "delete", "exec", "upsert"} {
		_, has := schema.EffectOf("sync", gone)
		assert.Assert(t, !has, "an append-only log must not declare %q", gone)
	}

	// A FIELD BELONGS TO ONE TYPE'S BLOCK AND TO NO OTHER. A log has no delivery
	// budget, so writing a queue's field into a log is an unknown key rather than a
	// number filed under a branch nobody took — the same discipline `database` keeps
	// between its `sql:` and `kv:` blocks.
	logWith := func(body string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/sync/a.yaml",
			[]byte("id: QmPq5xVnB7LtWcHdKzR3JgEyUM8fasNbT2vXrQwCk6hDZm\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		return err
	}

	assert.NilError(t, logWith("log:\n    consensus: crdt\n    retention:\n        age: 720h\n"))
	assert.ErrorContains(t,
		logWith("log:\n    consensus: crdt\n    visibility_timeout: 30s\n"),
		"unknown key 'visibility_timeout'", "a log has no delivery budget")
	assert.ErrorContains(t,
		logWith("log:\n    retention:\n        age: 720h\n"),
		"required attribute",
		"what happens to two concurrent appends is not a question silence may answer")

	// Something appends to it in the fixture, and it carries no queue defaults.
	audit := namedResource(t, byType(t, compileBasic(t), "log"), "audit")
	assert.Equal(t, audit["name"], "audit")
	_, strayed := audit["queue"]
	assert.Assert(t, !strayed, "a log was given a queue block: %v", audit)
}

// byType is the sync resources of one type, which is what a consumer of a typed kind
// actually wants — the kind alone answers queues and logs together.
func byType(t *testing.T, flat map[string]any, want string) map[string]any {
	t.Helper()

	out := map[string]any{}
	for id, v := range resources(t, flat, "sync") {
		if r, ok := v.(map[string]any); ok && r["type"] == want {
			out[id] = r
		}
	}
	return out
}

func TestInstancesTakeALimitAndAreNamedByAKey(t *testing.T) {
	compile := func(body string) (map[string]any, error) {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/package/wasm.yaml", []byte(standingPackages["/package/wasm.yaml"]), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/function/f.yaml",
			[]byte("id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\npackage:\n    name: wasm\nexecution: {call: c, timeout: 1s, memory: 1MB}\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/cache/c.yaml",
			[]byte("id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nsize: 1MB\nconsensus: crdt\n"+body), 0o644))
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

func TestRegionIsDeclaredOnEveryKind(t *testing.T) {
	assert.DeepEqual(t, schema.FootprintKinds(), []string{
		"application", "function", "worker", "sandbox",
		"cache", "database", "storage", "volume", "sync",
		"agent", "workflow",
	})

	// Both shapes: KindRoot copies the iterator's attributes, so a block attached
	// after the copy exists only in V2Resources.
	kinds := map[string]bool{}
	for _, g := range schema.V2Resources {
		if kind, _ := g.Match.(string); !g.Children[0].Group {
			kinds[kind] = true // a scope container's `regions` is the allowance
		}
	}
	assert.Assert(t, len(kinds) > 20)
	for _, root := range [][]*engine.Node{schema.V2Resources, schema.KindRoot()} {
		for _, g := range root {
			if kind, _ := g.Match.(string); kinds[kind] {
				fields := map[string]bool{}
				for _, a := range g.Children[0].Attributes {
					fields[a.Name] = true
				}
				for _, f := range []string{"regions/policy", "regions/in", "regions/main", "regions/standby"} {
					assert.Assert(t, fields[f], "%s carries no %q", kind, f)
				}
			}
		}
	}
}

func TestRegionPlacementIsASwitch(t *testing.T) {
	compile := func(body string) (map[string]any, error) {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
			[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/cache/c.yaml",
			[]byte("id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nsize: 1MB\nconsensus: crdt\n"+body), 0o644))
		c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
		assert.NilError(t, err)
		obj, _, err := c.Compile(context.Background())
		if err != nil {
			return nil, err
		}
		return namedResource(t, resources(t, obj.Flat(), "cache"), "c"), nil
	}

	res, err := compile("regions:\n    pinned: [region-1]\n")
	assert.NilError(t, err)
	assert.Equal(t, nested(res, "regions", "policy"), "pinned")
	assert.DeepEqual(t, nested(res, "regions", "in"), []string{"region-1"})

	// AN EMPTY BRANCH STILL CAPTURES, so `cdn` needs no regions to be sayable —
	// it is the branch whose regions the platform picks.
	res, err = compile("regions:\n    cdn: []\n")
	assert.NilError(t, err)
	assert.Equal(t, nested(res, "regions", "policy"), "cdn")

	res, err = compile("regions:\n    failover:\n        main: region-1\n        standby: region-2\n")
	assert.NilError(t, err)
	assert.Equal(t, nested(res, "regions", "policy"), "failover")
	assert.Equal(t, nested(res, "regions", "main"), "region-1")
	assert.Equal(t, nested(res, "regions", "standby"), "region-2")

	// An absent block writes nothing: the only spelling of "anywhere".
	res, err = compile("")
	assert.NilError(t, err)
	_, present := res["region"]
	assert.Assert(t, !present, "an absent region block leaves no keys behind")

	// EXACTLY TWO, AND ONLY TWO. A third has no key to arrive under and a missing one
	// is a compile error — which is what "the first one is main" costs instead of a
	// list whose order is load-bearing.
	_, err = compile("regions:\n    failover:\n        main: region-1\n")
	assert.ErrorContains(t, err, "required attribute 'regions/failover/standby'")
	_, err = compile("regions:\n    failover:\n        standby: region-2\n")
	assert.ErrorContains(t, err, "required attribute 'regions/failover/main'")
	_, err = compile("regions:\n    failover:\n        main: region-1\n        standby: region-2\n        third: ap\n")
	assert.ErrorContains(t, err, "unknown key 'third'")

	// `name` IS ON THE TWO BRANCHES A SINGLE REGION MEANS SOMETHING ON. On failover it
	// would name neither of the two, so the subset Either() refuses it.
	_, err = compile("regions:\n    failover:\n        main: region-1\n        standby: region-2\n        name: local\n")
	assert.ErrorContains(t, err, "unknown key 'name'")

	// EXACTLY ONE BRANCH is a property of the declaration, not a check somebody wrote.
	_, err = compile("regions:\n    pinned: [region-1]\n    cdn: []\n")
	assert.ErrorContains(t, err, "each match")
	_, err = compile("regions: {}\n")
	assert.ErrorContains(t, err, "attribute regions/policy has an empty key")

	// A PIN NAMING NO REGION PINS NOTHING, so the list is required on this branch
	// and optional on `cdn`, where an absent one means seeded rather than unplaced.
	_, err = compile("regions:\n    pinned: []\n")
	assert.ErrorContains(t, err, "required attribute 'regions/pinned' is empty")

	// ONE WORD, TWO SHAPES, AND THEY DO NOT COLLIDE. A scope writes `regions` as a
	// LIST — its allowance, and the default for everything under it — and a resource
	// writes it as the SWITCH above. A bare list on a resource picks no branch, so
	// there is no policy to capture and the compile says so rather than guessing one.
	_, err = compile("regions: [region-1]\n")
	assert.ErrorContains(t, err, "attribute regions/policy has an empty key")
}

func TestNoAuthoredFindingNamesADeletedKind(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "checks.go", nil, 0)
	assert.NilError(t, err)

	var lits []string
	ast.Inspect(f, func(n ast.Node) bool {
		if b, ok := n.(*ast.BasicLit); ok && b.Kind == token.STRING {
			if v, err := strconv.Unquote(b.Value); err == nil {
				lits = append(lits, v)
			}
		}
		return true
	})
	assert.Assert(t, len(lits) > 50, "only %d string literals in checks.go, the walk is wrong", len(lits))

	for _, gone := range []string{"entity", "entities", "connector", "connectors", "smartops"} {
		word := regexp.MustCompile(`(?i)\b` + gone + `\b`)
		for _, lit := range lits {
			assert.Assert(t, !word.MatchString(lit),
				"an authored string in checks.go names %q, a kind this vocabulary does not have: %q", gone, lit)
		}
	}

	// LIVE, the same way the schema scan proves itself: kinds that ARE here are named in
	// these strings, so a clean run means clean rather than a walk that found nothing.
	for _, here := range []string{"queue", "function"} {
		word := regexp.MustCompile(`(?i)\b` + here + `\b`)
		found := false
		for _, lit := range lits {
			if word.MatchString(lit) {
				found = true
				break
			}
		}
		assert.Assert(t, found, "no authored string names %q, so this scan proves nothing", here)
	}
}

func TestABusDeclaresNoConsistency(t *testing.T) {
	var bus *engine.Node
	for _, g := range schema.V2Resources {
		if name, _ := g.Match.(string); name == "bus" {
			bus = g
			break
		}
	}
	assert.Assert(t, bus != nil, "there is no bus kind")

	banned := regexp.MustCompile(`(?i)consensus|crdt|raft|linearizab|quorum`)
	var walk func(n *engine.Node)
	walk = func(n *engine.Node) {
		for _, a := range n.Attributes {
			for _, seg := range a.Segments {
				assert.Assert(t, !banned.MatchString(seg),
					"bus has an attribute %q: a bus is libp2p pubsub and can honour no such promise", strings.Join(a.Segments, "/"))
			}
			assert.Assert(t, !banned.MatchString(a.Name),
				"bus has an attribute named %q, which a gossipsub bus cannot honour", a.Name)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(bus)

	// LIVE: the same walk over `cache` finds the axis, so a clean bus is a clean bus and
	// not a walk that never reached an attribute.
	var cache *engine.Node
	for _, g := range schema.V2Resources {
		if name, _ := g.Match.(string); name == "cache" {
			cache = g
			break
		}
	}
	assert.Assert(t, cache != nil, "there is no cache kind")
	seen := false
	var look func(n *engine.Node)
	look = func(n *engine.Node) {
		for _, a := range n.Attributes {
			if banned.MatchString(a.Name) || banned.MatchString(strings.Join(a.Segments, "/")) {
				seen = true
			}
		}
		for _, c := range n.Children {
			look(c)
		}
	}
	look(cache)
	assert.Assert(t, seen, "the walk cannot find cache's consensus axis, so it proves nothing about bus")
}
