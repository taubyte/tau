//go:build dreaming

package tests

import (
	"bytes"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/ipfs/go-log/v2"
	hoarderClient "github.com/taubyte/tau/clients/p2p/hoarder"
	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	commonIface "github.com/taubyte/tau/core/common"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/pkg/kvdb"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	tccCompiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
	substrateSrv "github.com/taubyte/tau/services/substrate"
	_ "github.com/taubyte/tau/services/substrate/dream"
	"github.com/taubyte/tau/services/substrate/migration"
	_ "github.com/taubyte/tau/services/tns/dream"
	mh "github.com/taubyte/tau/utils/multihash"
	tcc "github.com/taubyte/tau/utils/tcc"
	"gotest.tools/v3/assert"

	dsquery "github.com/ipfs/go-datastore/query"
)

const (
	projectID  = "Qmc3WjpDvCaVY3jWmxranUY7roFhRj66SNqstiRbKxDbU4"
	dbMatch    = "/mig/db1"
	stMatch    = "/mig/st1"
	rtPattern  = "^/rt/.*"
	rtConcrete = "/rt/one"
	branch     = "main"
)

var (
	testLogger            = log.Logger("tau.migration.tests")
	generatedDomainRegExp = regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)
	fileBytes             = bytes.Repeat([]byte("stored file payload "), 64)
)

func fastConvergence(t *testing.T) {
	t.Helper()
	origHB := hoarderSpecs.HeartbeatInterval
	origBackstop := hoarderSpecs.ReconcileBackstop
	origJitter := hoarderSpecs.ReconcileJitter
	hoarderSpecs.HeartbeatInterval = 500 * time.Millisecond
	hoarderSpecs.ReconcileBackstop = 3 * time.Second
	hoarderSpecs.ReconcileJitter = 100 * time.Millisecond
	t.Cleanup(func() {
		hoarderSpecs.HeartbeatInterval = origHB
		hoarderSpecs.ReconcileBackstop = origBackstop
		hoarderSpecs.ReconcileJitter = origJitter
	})
}

// publishGenerated compiles a virtual project holding the given resources and
// publishes it to the universe's TNS.
func publishGenerated(t *testing.T, u *dream.Universe, resources ...interface{}) {
	t.Helper()
	fs, _, err := tcc.GenerateProject(projectID, resources...)
	assert.NilError(t, err)

	compiler, err := tccCompiler.New(tccCompiler.WithVirtual(fs, "/"), tccCompiler.WithBranch(branch))
	assert.NilError(t, err)
	obj, validations, err := compiler.Compile(u.Context())
	assert.NilError(t, err)
	pid, err := tcc.ExtractProjectID(validations)
	assert.NilError(t, err)
	assert.NilError(t, tcc.ProcessDNSValidations(validations, generatedDomainRegExp, true, nil))

	flat := obj.Flat()
	object := flat["object"].(map[string]interface{})
	indexes := flat["indexes"].(map[string]interface{})

	simple, err := u.Simple("client")
	assert.NilError(t, err)
	tns, err := simple.TNS()
	assert.NilError(t, err)
	assert.NilError(t, tcc.Publish(tns, object, indexes, pid, branch, "migCommit1"))
}

// seedLocal writes a namespace into the substrate node's datastore the way the
// node-local architecture did — through the kvdb layer, then closed.
func seedLocal(t *testing.T, u *dream.Universe, hash string, entries map[string][]byte) {
	t.Helper()
	factory := kvdb.New(u.Substrate().Node())
	db, err := factory.New(testLogger, hash, 5)
	assert.NilError(t, err)
	for k, v := range entries {
		assert.NilError(t, db.Put(u.Context(), k, v))
	}
	db.Close()
}

func crdtKeyCount(t *testing.T, u *dream.Universe, prefix string) int {
	t.Helper()
	res, err := u.Substrate().Node().Store().Query(u.Context(), dsquery.Query{Prefix: prefix, KeysOnly: true})
	assert.NilError(t, err)
	entries, err := res.Rest()
	assert.NilError(t, err)
	return len(entries)
}

// TestMigration_Dreaming is the end-to-end keystone: seed node-local data in
// every shape the old architecture produced (exact-match database, storage
// metadata + file bytes, the project global under its old hash, and a
// regex-matched instance), migrate, and prove everything is readable through
// the hoarder path and gone locally — with a live write never clobbered and a
// re-run finding nothing.
func TestMigration_Dreaming(t *testing.T) {
	fastConvergence(t)

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	assert.NilError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":       {},
			"hoarder":   {},
			"substrate": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {Clients: dream.SimpleConfigClients{TNS: &commonIface.ClientConfig{}}.Compat()},
		},
	}))
	u.Mesh()

	publishGenerated(t, u,
		&structureSpec.Database{Name: "migdb", Match: dbMatch, Size: 1000000},
		&structureSpec.Database{Name: "migrt", Match: rtPattern, Regex: true, Size: 1000000},
		&structureSpec.Storage{Name: "migst", Type: "object", Match: stMatch, Size: 1000000000},
	)

	// Let the substrate node discover the hoarder before driving remote ops.
	time.Sleep(6 * time.Second)

	// Seed the pre-hoarder shape on the substrate node.
	cid, err := u.Substrate().Node().AddFile(bytes.NewReader(fileBytes))
	assert.NilError(t, err)

	dbHash := mh.Hash(projectID + "" + dbMatch)
	stHash := mh.Hash(projectID + "" + stMatch)
	rtHash := mh.Hash(projectID + "" + rtConcrete)
	oldGlobal := mh.Hash("global" + projectID)

	seedLocal(t, u, dbHash, map[string][]byte{"alpha": []byte("old"), "/nested/k": []byte("v2")})
	seedLocal(t, u, stHash, map[string][]byte{"file/doc/1": []byte(cid), "v/doc": []byte("1")})
	seedLocal(t, u, rtHash, map[string][]byte{"rk": []byte("rv")})
	seedLocal(t, u, oldGlobal, map[string][]byte{"g": []byte("gv")})

	hcli, err := hoarderClient.New(u.Context(), u.Substrate().Node())
	assert.NilError(t, err)

	// A live write through the data plane, racing ahead of the migration: it
	// must win over the replayed value. First-touch may need the config to
	// propagate — bounded retry.
	liveKV, err := hcli.KVDB(hoarderIface.Database, projectID, "", dbMatch, branch)
	assert.NilError(t, err)
	deadline := time.Now().Add(90 * time.Second)
	for {
		if err = liveKV.Put(u.Context(), "alpha", []byte("live")); err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Second)
	}
	assert.NilError(t, err)

	sub, ok := u.Substrate().(*substrateSrv.Service)
	assert.Assert(t, ok, "universe substrate is not the concrete service")

	// Pass 1: everything TNS can name migrates; the regex instance has no
	// durable name yet and must be left intact.
	var report *migration.Report
	deadline = time.Now().Add(90 * time.Second)
	for {
		report = sub.Migrator().Migrate(u.Context())
		if len(report.Instances) == 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	assert.Equal(t, len(report.Instances), 3, report.Summary())
	for _, h := range []string{dbHash, stHash, oldGlobal} {
		rep := report.Instances[h]
		assert.Assert(t, rep != nil && rep.Scrubbed, "instance %s not scrubbed: %s", h, report.Summary())
	}
	assert.Equal(t, len(report.Unresolved), 1, report.Summary())
	assert.Equal(t, report.Unresolved[0], rtHash)

	// The live write survived; the replayed keys read back via the hoarder.
	v, err := liveKV.Get(u.Context(), "alpha")
	assert.NilError(t, err)
	assert.Equal(t, string(v), "live")
	v, err = liveKV.Get(u.Context(), "/nested/k")
	assert.NilError(t, err)
	assert.Equal(t, string(v), "v2")

	globalKV, err := hcli.KVDB(hoarderIface.Global, projectID, "", "global", "")
	assert.NilError(t, err)
	v, err = globalKV.Get(u.Context(), "g")
	assert.NilError(t, err)
	assert.Equal(t, string(v), "gv")

	stKV, err := hcli.KVDB(hoarderIface.Storage, projectID, "", stMatch, branch)
	assert.NilError(t, err)
	v, err = stKV.Get(u.Context(), "file/doc/1")
	assert.NilError(t, err)
	assert.Equal(t, string(v), cid)

	// File bytes are stash-claimed and served by the hoarder; the substrate
	// node no longer holds the namespaces nor the blocks.
	claims, target, err := hcli.StashStatus(cid)
	assert.NilError(t, err)
	assert.Assert(t, claims[cid] >= target, "cid claims %d < target %d", claims[cid], target)
	f, err := u.Hoarder().Node().GetFile(u.Context(), cid)
	assert.NilError(t, err)
	got := make([]byte, len(fileBytes))
	_, err = f.Read(got)
	f.Close()
	assert.NilError(t, err)
	assert.DeepEqual(t, got, fileBytes)

	for _, h := range []string{dbHash, stHash, oldGlobal} {
		assert.Equal(t, crdtKeyCount(t, u, "/crdt/"+h), 0, "namespace %s not empty", h)
	}
	assert.Assert(t, crdtKeyCount(t, u, "/crdt/"+rtHash) > 0, "unresolved namespace must stay intact")

	// Live traffic first-touches the regex instance, giving it a durable
	// identity; the next pass drains it.
	rtKV, err := hcli.KVDB(hoarderIface.Database, projectID, "", rtConcrete, branch)
	assert.NilError(t, err)
	if _, err = rtKV.Get(u.Context(), "rk"); err != nil {
		assert.Assert(t, errors.Is(err, hoarderClient.ErrNotFound), "first-touch read: %v", err)
	}

	report = sub.Migrator().Migrate(u.Context())
	rep := report.Instances[rtHash]
	assert.Assert(t, rep != nil && rep.Scrubbed, "regex instance not drained: %s", report.Summary())
	v, err = rtKV.Get(u.Context(), "rk")
	assert.NilError(t, err)
	assert.Equal(t, string(v), "rv")

	// Nothing local remains; a re-run finds nothing to do.
	assert.Equal(t, crdtKeyCount(t, u, "/crdt/"), 0)
	report = sub.Migrator().Migrate(u.Context())
	assert.Assert(t, report.Empty(), "re-run not empty: %s", report.Summary())
}

// TestMigrationFailClosed_Dreaming proves the never-delete-before-verified
// rule under infrastructure failure: with the hoarders dead the replay fails
// and the local data survives untouched; with TNS dead resolution carries
// everything over without classifying anything.
func TestMigrationFailClosed_Dreaming(t *testing.T) {
	fastConvergence(t)

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	assert.NilError(t, err)

	assert.NilError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"tns":       {},
			"hoarder":   {},
			"substrate": {},
		},
		Simples: map[string]dream.SimpleConfig{
			"client": {Clients: dream.SimpleConfigClients{TNS: &commonIface.ClientConfig{}}.Compat()},
		},
	}))
	u.Mesh()

	publishGenerated(t, u, &structureSpec.Database{Name: "migdb", Match: dbMatch, Size: 1000000})
	time.Sleep(6 * time.Second)

	dbHash := mh.Hash(projectID + "" + dbMatch)
	seedLocal(t, u, dbHash, map[string][]byte{"alpha": []byte("v")})

	sub, ok := u.Substrate().(*substrateSrv.Service)
	assert.Assert(t, ok)

	// Kill the hoarder: replay must fail and delete nothing.
	assert.NilError(t, u.Kill("hoarder"))
	report := sub.Migrator().Migrate(u.Context())
	if rep := report.Instances[dbHash]; rep != nil {
		assert.Assert(t, !rep.Scrubbed && rep.Err != "", "unreachable hoarders must fail the instance: %+v", rep)
	}
	assert.Assert(t, crdtKeyCount(t, u, "/crdt/"+dbHash) > 0, "local data must survive a dead fleet")

	// Kill TNS too: resolution errors and everything carries over.
	assert.NilError(t, u.Kill("tns"))
	report = sub.Migrator().Migrate(u.Context())
	assert.Assert(t, report.Err != "", "a dead TNS must abort the pass")
	assert.Equal(t, len(report.Unresolved), 1)
	assert.Assert(t, crdtKeyCount(t, u, "/crdt/"+dbHash) > 0, "local data must survive a TNS outage")
}
