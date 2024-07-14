package methods

import (
	"errors"

	"github.com/taubyte/tau/pkg/specs/common"
)

func WasmModulePath(projectId, appId, name string, resourceType common.PathVariable) (*common.TnsPath, error) {
	if len(projectId) == 0 {
		return nil, errors.New("project id is required for creating a Wasm path")
	}

	if len(appId) == 0 {
		return common.NewTnsPath([]string{"wasm", "project", projectId, "modules", string(resourceType), name}), nil
	}

	return common.NewTnsPath([]string{"wasm", "project", projectId, "application", appId, "modules", string(resourceType), name}), nil
}

func WasmModulePathFromModule(projectId, appId, moduleType string, name string) (*common.TnsPath, error) {
	if len(projectId) == 0 {
		return nil, errors.New("project id is required for creating a Wasm path")
	}

	if len(appId) == 0 {
		return common.NewTnsPath([]string{"wasm", "project", projectId, "modules", moduleType, name}), nil
	}

	return common.NewTnsPath([]string{"wasm", "project", projectId, "application", appId, "modules", moduleType, name}), nil
}
