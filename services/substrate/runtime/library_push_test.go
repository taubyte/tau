package runtime

import (
	"testing"

	"github.com/taubyte/tau/core/services/substrate/components"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/core/vm"
	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	vmContext "github.com/taubyte/tau/pkg/vm/context"
	"github.com/taubyte/tau/services/substrate/runtime/cache"
	"github.com/taubyte/tau/services/tns/mocks"
	"github.com/taubyte/tau/utils/id"
)

const (
	lpProject = "lp_project"
	lpBranch  = "main"
	lpCommit  = "config_commit"
)

// libPushTns is the mocked TNS plus the config commit lookup cache validation needs.
type libPushTns struct{ mocks.MockedTns }

func (libPushTns) Simple() tns.SimpleIface { return libPushSimple{} }

type libPushSimple struct{ tns.SimpleIface }

func (libPushSimple) Commit(string, ...string) (string, string, error) {
	return lpCommit, lpBranch, nil
}

type libPushService struct {
	components.ServiceComponent
	tns tns.Client
}

func (s *libPushService) Tns() tns.Client { return s.tns }

type libPushMatcher struct{ components.MatchDefinition }

func (libPushMatcher) CachePrefix() string { return "lp" }

// libPushServiceable is shaped like the http/p2p/pubsub function serviceables:
// it embeds *Function.
type libPushServiceable struct {
	srv *libPushService
	*Function
}

func (*libPushServiceable) Match(components.MatchDefinition) matcherSpec.Index {
	return matcherSpec.HighMatch
}
func (*libPushServiceable) Validate(components.MatchDefinition) error { return nil }
func (*libPushServiceable) Matcher() components.MatchDefinition       { return libPushMatcher{} }
func (*libPushServiceable) Ready() error                              { return nil }
func (*libPushServiceable) Project() string                           { return lpProject }
func (*libPushServiceable) Application() string                       { return "" }
func (*libPushServiceable) Id() string                                { return "fn_id" }
func (*libPushServiceable) Commit() string                            { return lpCommit }
func (*libPushServiceable) Branch() string                            { return lpBranch }
func (*libPushServiceable) AssetId() string                           { return "" }
func (s *libPushServiceable) Service() components.ServiceComponent    { return s.srv }
func (*libPushServiceable) Close()                                    {}

// libRuntime is a runtime that loaded the library lib1 (as its code or as an import).
type libRuntime struct{ fakeRuntime }

func (*libRuntime) Modules() []string { return []string{"libraries/lib1"} }

// After a library push only the library's asset moves; the config commit and
// the function's own asset stay. The cached function must still be dropped.
func TestLibraryPushInvalidatesCachedFunction(t *testing.T) {
	tnsClient := mocks.New()
	lib := &structureSpec.Library{Id: "lib_id", Name: "lib1"}
	push := func(cid string) {
		if err := tnsClient.Inject(lib, mocks.InjectConfig{Project: lpProject, Branch: lpBranch, Commit: lpCommit, Cid: cid}); err != nil {
			t.Fatal(err)
		}
	}
	push(id.Generate())

	ctx, err := vmContext.New(t.Context(), vmContext.Project(lpProject), vmContext.Branch(lpBranch), vmContext.Commit(lpCommit))
	if err != nil {
		t.Fatal(err)
	}

	s := &libPushServiceable{srv: &libPushService{tns: libPushTns{tnsClient}}}
	s.Function = &Function{
		serviceable:        s,
		config:             &structureSpec.Function{Name: "fn", Source: "libraries/lib1"},
		vmContext:          ctx,
		vmConfig:           &vm.Config{MemoryLimitPages: 4},
		availableInstances: make(chan Instance, InstanceMaxRequests),
	}

	// a served call: the instance loaded the library and goes back to the pool
	if err := (&instance{runtime: &libRuntime{}, parent: s.Function}).Free(); err != nil {
		t.Fatal(err)
	}

	c := cache.New()
	if _, err := c.Add(s); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(libPushMatcher{}, components.GetOptions{Validation: true}); err != nil {
		t.Fatalf("unchanged library should keep the cached function: %v", err)
	}

	push(id.Generate())

	if _, err := c.Get(libPushMatcher{}, components.GetOptions{Validation: true}); err == nil {
		t.Fatal("cached function still served after its library was pushed")
	}
}
