package hoarder

import (
	"errors"
	"testing"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	ifaceTns "github.com/taubyte/tau/core/services/tns"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

// fakeTns embeds tns.Client and overrides only the two typed accessors
// validateConfig reaches (Database/Storage). Everything else panics if touched,
// which is exactly what we want in a unit test. A non-nil listErr makes List()
// fail, standing in for a TNS transport outage (as opposed to a successful list
// that simply contains no match).
type fakeTns struct {
	ifaceTns.Client
	dbs      map[string]*structureSpec.Database
	storages map[string]*structureSpec.Storage
	listErr  error
}

func (f *fakeTns) Database() ifaceTns.StructureIface[*structureSpec.Database] {
	return fakeStruct[*structureSpec.Database]{list: f.dbs, err: f.listErr}
}
func (f *fakeTns) Storage() ifaceTns.StructureIface[*structureSpec.Storage] {
	return fakeStruct[*structureSpec.Storage]{list: f.storages, err: f.listErr}
}

type fakeStruct[T structureSpec.Structure] struct {
	ifaceTns.StructureIface[T]
	list map[string]T
	err  error
}

func (f fakeStruct[T]) All(_, _ string, _ ...string) ifaceTns.StructureGetter[T] {
	return fakeGetter[T]{list: f.list, err: f.err}
}

type fakeGetter[T structureSpec.Structure] struct {
	ifaceTns.StructureGetter[T]
	list map[string]T
	err  error
}

func (f fakeGetter[T]) List() (map[string]T, string, string, error) {
	return f.list, "", "", f.err
}

func TestValidateConfig_DatabaseAndStorage(t *testing.T) {
	srv := newTestService(t)
	srv.tnsClient = &fakeTns{
		dbs:      map[string]*structureSpec.Database{"db-1": {Name: "users", Match: "users"}},
		storages: map[string]*structureSpec.Storage{"st-1": {Name: "files", Match: "files"}},
	}

	// Database: matcher resolves the config id onto the auction.
	dbAuc := &hoarderIface.Auction{MetaType: hoarderIface.Database, Meta: meta("users")}
	if err := srv.validateConfig(dbAuc); err != nil {
		t.Fatalf("database validate: %v", err)
	}
	if dbAuc.Meta.ConfigId != "db-1" {
		t.Fatalf("database ConfigId = %q, want db-1", dbAuc.Meta.ConfigId)
	}

	// Database: no matching config → error, no id set.
	if err := srv.validateConfig(&hoarderIface.Auction{MetaType: hoarderIface.Database, Meta: meta("ghost")}); err == nil {
		t.Fatal("expected no-match error for database")
	}

	// Storage: matcher resolves; an explicit branch drives the branch-override path.
	stAuc := &hoarderIface.Auction{MetaType: hoarderIface.Storage, Meta: hoarderIface.MetaData{ProjectId: "proj", Match: "files", Branch: "dev"}}
	if err := srv.validateConfig(stAuc); err != nil {
		t.Fatalf("storage validate: %v", err)
	}
	if stAuc.Meta.ConfigId != "st-1" {
		t.Fatalf("storage ConfigId = %q, want st-1", stAuc.Meta.ConfigId)
	}

	// Unknown kind → error.
	if err := srv.validateConfig(&hoarderIface.Auction{MetaType: hoarderIface.ResourceKind(99), Meta: meta("x")}); err == nil {
		t.Fatal("expected error for invalid resource kind")
	}
}

// TestValidateConfig_NoMatchIsSentinel pins the wire that carries the definitive
// signal: a successful list with no match must wrap errNoConfigMatch, while a
// listing failure (outage) must NOT — otherwise configDeleted cannot tell a real
// deletion from a transient blip.
func TestValidateConfig_NoMatchIsSentinel(t *testing.T) {
	srv := newTestService(t)
	srv.tnsClient = &fakeTns{dbs: map[string]*structureSpec.Database{"db-1": {Name: "users", Match: "users"}}}

	// TNS answered, nothing matched → definitive: wraps the sentinel.
	noMatch := srv.validateConfig(&hoarderIface.Auction{MetaType: hoarderIface.Database, Meta: meta("ghost")})
	if !errors.Is(noMatch, errNoConfigMatch) {
		t.Fatalf("no-match error must wrap errNoConfigMatch, got %v", noMatch)
	}

	// TNS listing failed (outage) → still an error, but NOT the sentinel.
	srv.tnsClient = &fakeTns{listErr: errors.New("tns unreachable")}
	outage := srv.validateConfig(&hoarderIface.Auction{MetaType: hoarderIface.Database, Meta: meta("users")})
	if outage == nil {
		t.Fatal("a listing failure must still error so claim paths fail safe")
	}
	if errors.Is(outage, errNoConfigMatch) {
		t.Fatalf("a listing failure must NOT wrap errNoConfigMatch, got %v", outage)
	}
}

// TestConfigDeleted_OutageVsDeletion is the crux of F2: configDeleted may fire
// only on a DEFINITIVE deletion (TNS listed configs, none matched), never on a
// TNS outage — and in both cases validateConfig still errors so claim paths stay
// fail-safe. Also covers Global (no backing config) and a corrupt unknown Kind
// (record corruption, left alone rather than purged).
func TestConfigDeleted_OutageVsDeletion(t *testing.T) {
	// A database resource whose backing config genuinely no longer exists.
	deleted := &RegistryMeta{Kind: hoarderIface.Database, ProjectId: "proj", Match: "ghost"}

	// (a) TNS lists configs and none match → genuine deletion → true.
	srv := newTestService(t)
	srv.tnsClient = &fakeTns{dbs: map[string]*structureSpec.Database{"db-1": {Name: "users", Match: "users"}}}
	if !srv.configDeleted(deleted) {
		t.Fatal("configDeleted must be true when TNS lists configs and none match (genuine deletion)")
	}
	if err := srv.validateConfig(metaAuction(deleted)); err == nil {
		t.Fatal("validateConfig must still error on a no-match so claim paths fail safe")
	}

	// (b) TNS List fails (transport error / outage) → NOT a deletion → false.
	outageSrv := newTestService(t)
	outageSrv.tnsClient = &fakeTns{listErr: errors.New("tns unreachable")}
	if outageSrv.configDeleted(deleted) {
		t.Fatal("configDeleted must be false on a TNS listing failure (transient outage, not a deletion)")
	}
	if err := outageSrv.validateConfig(metaAuction(deleted)); err == nil {
		t.Fatal("validateConfig must still error on a listing failure so claim paths fail safe")
	}

	// (c) Global has no backing TNS config → never config-deleted (existing behavior).
	if srv.configDeleted(&RegistryMeta{Kind: hoarderIface.Global}) {
		t.Fatal("Global resources are never config-deleted")
	}

	// (d) Corrupt/unknown Kind is not evidence of deletion → left alone (false),
	// even though validateConfig errors on it.
	corrupt := &RegistryMeta{Kind: hoarderIface.ResourceKind(99), Match: "x"}
	if srv.configDeleted(corrupt) {
		t.Fatal("an unknown Kind is record corruption, not a config deletion — must not be purged")
	}
	if err := srv.validateConfig(metaAuction(corrupt)); err == nil {
		t.Fatal("validateConfig must still error on an unknown kind")
	}
}
