package components

import (
	"github.com/taubyte/tau/core/services/substrate"
	matcherSpec "github.com/taubyte/tau/pkg/specs/matcher"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

type ServiceComponent interface {
	substrate.Service
	CheckTns(MatchDefinition) ([]Serviceable, error)
	Cache() Cache
}

/*
GetOptions defines the parameters of serviceables returned by the Cache.Get() method

Validation: if set asset cid, and config commit are validated of the serviceable are validated

Branch: used by the validation method, if not set spec.DefaultBranch is used, currently
this is the only branch handled by production deployed services.

MatchIndex: the required Match Index for a serviceable
if not set then matcherSpec.HighMatch is used
*/
type GetOptions struct {
	Validation bool
	Branches   []string
	MatchIndex *matcherSpec.Index
}

type Cache interface {
	Add(serviceable Serviceable) (Serviceable, error)
	Get(MatchDefinition, GetOptions) ([]Serviceable, error)
	Remove(Serviceable)
	Close()
}

type Serviceable interface {
	Match(MatchDefinition) matcherSpec.Index
	Validate(MatchDefinition) error
	Matcher() MatchDefinition
	Ready() error

	Project() string
	Application() string
	Id() string

	Commit() string
	Branch() string

	AssetId() string

	Service() ServiceComponent
	Close()
}

type FunctionServiceable interface {
	Serviceable
	Config() *structureSpec.Function
}

type MatchDefinition interface {
	String() string
	CachePrefix() string
}
