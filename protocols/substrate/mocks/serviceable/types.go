package serviceable

import (
	"github.com/taubyte/go-interfaces/services/substrate/components"
	matcherSpec "github.com/taubyte/go-specs/matcher"
	structureSpec "github.com/taubyte/go-specs/structure"
)

func New(projectId, appName, resourceId, commit string, structure *structureSpec.Function) MockedServiceable {
	return &mockServiceable{project: projectId, app: appName, id: resourceId, structure: structure, commit: commit}
}

type MockedServiceable interface {
	components.Serviceable
}
type mockServiceable struct {
	project   string
	app       string
	id        string
	commit    string
	structure *structureSpec.Function
}

type MockedMatchDefinition interface {
	components.MatchDefinition
}

type mockMatchDefinition struct{}

func (*mockServiceable) Match(components.MatchDefinition) matcherSpec.Index { return 0 }
func (*mockServiceable) Validate(components.MatchDefinition) error          { return nil }
func (*mockServiceable) Matcher() components.MatchDefinition                { return &mockMatchDefinition{} }
func (*mockServiceable) Ready() error                                       { return nil }

func (m *mockServiceable) Project() string                    { return m.project }
func (m *mockServiceable) Application() string                { return m.app }
func (m *mockServiceable) Id() string                         { return m.id }
func (m *mockServiceable) Structure() *structureSpec.Function { return m.structure }

func (m *mockServiceable) Commit() string { return m.commit }
func (*mockServiceable) Service() components.ServiceComponent
func (*mockServiceable) Close()

func (*mockMatchDefinition) String() string
func (*mockMatchDefinition) CachePrefix() string
