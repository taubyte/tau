package structureSpec

// Object-addressing methods for the tcc-gen'd Function struct type (see function.go).

import (
	"github.com/taubyte/tau/pkg/specs/common"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
)

func (f Function) GetName() string {
	return f.Name
}

func (f *Function) SetId(id string) {
	f.Id = id
}

func (f *Function) BasicPath(branch, commit, project, app string) (*common.TnsPath, error) {
	return functionSpec.Tns().BasicPath(branch, commit, project, app, f.Id)
}

func (f *Function) IndexValue(branch, project, app string) (*common.TnsPath, error) {
	return functionSpec.Tns().IndexValue(branch, project, app, f.Id)
}

func (f *Function) HttpPath(fqdn string) (*common.TnsPath, error) {
	return functionSpec.Tns().HttpPath(fqdn)
}

func (f *Function) WasmModulePath(project, app string) (*common.TnsPath, error) {
	return functionSpec.Tns().WasmModulePath(project, app, f.Name)
}

func (f *Function) ModuleName() string {
	return functionSpec.ModuleName(f.Name)
}

func (f *Function) ServicesPath(project, app, serviceId string) (*common.TnsPath, error) {
	return functionSpec.Tns().ServicesPath(project, app, serviceId, f.Command)
}

func (f *Function) GetId() string {
	return f.Id
}
