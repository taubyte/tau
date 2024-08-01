package internal

import (
	"github.com/taubyte/tau/pkg/schema/pretty"
	commonSpec "github.com/taubyte/tau/pkg/specs/common"
)

type PrettierFetchMethod func(path *commonSpec.TnsPath) (pretty.Object, error)
type PrettierStringMethod func() string
type PrettierStringSliceMethod func() []string

type Setter interface {
	Fetch(PrettierFetchMethod)
	Project(PrettierStringMethod)
	Branches(PrettierStringSliceMethod)
	AssetCID(cid string)
}

type Prettier interface {
	Fetch(path *commonSpec.TnsPath) (pretty.Object, error)
	Project() string
	Branches() []string

	Set() Setter
}

type prettier struct {
	cid      string
	fetch    PrettierFetchMethod
	project  PrettierStringMethod
	branches PrettierStringSliceMethod
}

func (p *prettier) Fetch(path *commonSpec.TnsPath) (pretty.Object, error) {
	return p.fetch(path)
}

func (p *prettier) Project() string {
	return p.project()
}

func (p *prettier) Branches() []string {
	return p.branches()
}

type setter struct {
	*prettier
}

func (s *setter) Fetch(method PrettierFetchMethod) {
	s.fetch = method
}

func (s *setter) Project(method PrettierStringMethod) {
	s.project = method
}

func (s *setter) Branches(method PrettierStringSliceMethod) {
	s.branches = method
}

func (s *setter) AssetCID(cid string) {
	s.cid = cid
}

func (p *prettier) Set() Setter {
	return &setter{p}
}

type object struct {
	*prettier
}

func (o *object) Interface() interface{} {
	return o.cid
}

// Used for overriding calls to Prettier and testing error returns
func NewMockPrettier() Prettier {
	p := &prettier{
		project: func() string {
			return "test_project_id"
		},
		branches: func() []string {
			return []string{"main"}
		},
	}

	p.fetch = func(path *commonSpec.TnsPath) (pretty.Object, error) {
		return &object{p}, nil
	}

	return p
}
