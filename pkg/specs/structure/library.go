package structureSpec

import (
	"github.com/taubyte/tau/pkg/specs/common"
	librarySpec "github.com/taubyte/tau/pkg/specs/library"
)

type Library struct {
	Id          string
	Name        string
	Description string
	Tags        []string

	Path     string
	Branch   string
	Provider string
	RepoID   string `mapstructure:"repository-id"`
	RepoName string `mapstructure:"repository-name"`

	// noset, this is parsed from the tags
	SmartOps []string

	Wasm
}

func (l Library) GetName() string {
	return l.Name
}

func (l *Library) SetId(id string) {
	l.Id = id
}

func (l *Library) BasicPath(branch, commit, project, app string) (*common.TnsPath, error) {
	return librarySpec.Tns().BasicPath(branch, commit, project, app, l.Id)
}

func (l *Library) NameIndex() *common.TnsPath {
	return librarySpec.Tns().NameIndex(l.Name)
}

func (l *Library) IndexValue(branch, project, app string) (*common.TnsPath, error) {
	return librarySpec.Tns().IndexValue(branch, project, app, l.Id)
}

func (l *Library) WasmModulePath(project, app string) (*common.TnsPath, error) {
	return librarySpec.Tns().WasmModulePath(project, app, l.Name)
}

func (l *Library) ModuleName() string {
	return librarySpec.ModuleName(l.Name)
}

func (l *Library) GetId() string {
	return l.Id
}
