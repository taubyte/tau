package structureSpec

import (
	"github.com/taubyte/tau/pkg/specs/common"
	smartopSpec "github.com/taubyte/tau/pkg/specs/smartops"
)

type SmartOp struct {
	Id          string
	Name        string
	Description string
	Tags        []string

	Timeout uint64
	Memory  uint64
	Call    string
	Source  string

	// noset, this is parsed from the tags
	SmartOps []string

	Wasm
}

func (s SmartOp) GetName() string {
	return s.Name
}

func (s *SmartOp) SetId(id string) {
	s.Id = id
}

func (s *SmartOp) BasicPath(branch, commit, projectId, appId string) (*common.TnsPath, error) {
	return smartopSpec.Tns().BasicPath(branch, commit, projectId, appId, s.Id)
}

func (s *SmartOp) IndexValue(branch, projectId, appId string) (*common.TnsPath, error) {
	return smartopSpec.Tns().IndexValue(branch, projectId, appId, s.Id)
}

func (s *SmartOp) WasmModulePath(projectId, appId string) (*common.TnsPath, error) {
	return smartopSpec.Tns().WasmModulePath(projectId, appId, s.Name)
}

func (s *SmartOp) ModuleName() string {
	return smartopSpec.ModuleName(s.Name)
}

func (s *SmartOp) GetId() string {
	return s.Id
}
