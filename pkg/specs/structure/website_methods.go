package structureSpec

// Object-addressing methods for the tcc-gen'd Website struct type (see website.go).

import (
	"github.com/taubyte/tau/pkg/specs/common"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
)

func (w Website) GetName() string {
	return w.Name
}

func (w *Website) SetId(id string) {
	w.Id = id
}

func (w *Website) BasicPath(branch, commit, projectId, appId string) (*common.TnsPath, error) {
	return websiteSpec.Tns().BasicPath(branch, commit, projectId, appId, w.Id)
}

func (w *Website) IndexValue(branch, projectId, appId string) (*common.TnsPath, error) {
	return websiteSpec.Tns().IndexValue(branch, projectId, appId, w.Id)
}

func (w *Website) HttpPath(fqdn string) (*common.TnsPath, error) {
	return websiteSpec.Tns().HttpPath(fqdn)
}

func (w *Website) WasmModulePath(projectId, appId string) (*common.TnsPath, error) {
	return websiteSpec.Tns().WasmModulePath(projectId, appId, w.Name)
}

func (w *Website) GetId() string {
	return w.Id
}
