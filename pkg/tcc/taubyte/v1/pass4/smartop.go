package pass4

import (
	"fmt"
	"slices"

	specs "github.com/taubyte/tau/pkg/specs/smartops"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type smartops struct {
	branch string
}

func Smartops(branch string) transform.Transformer[object.Refrence] {
	return &smartops{branch: branch}
}

func (s *smartops) Process(ct transform.Context[object.Refrence], config object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	if len(ct.Path()) < 2 {
		return nil, fmt.Errorf("path %v is too short", ct.Path())
	}

	root, ok := ct.Path()[0].(object.Object[object.Refrence])
	if !ok {
		return nil, fmt.Errorf("root is not an object")
	}

	configRoot, ok := ct.Path()[1].(object.Object[object.Refrence])
	if !ok {
		return nil, fmt.Errorf("config root is not an object")
	}

	appId := ""
	if configRoot != config {
		appsObj, err := configRoot.Child("applications").Object()
		if err != nil {
			return nil, fmt.Errorf("fetching applications failed with %w", err)
		}
		appId = appsObj.Child(config).Name()
	}

	smartopConfig, err := config.Child(string(specs.PathVariable)).Object()
	if err == object.ErrNotExist {
		return config, nil
	} else if err != nil {
		return nil, fmt.Errorf("fetching smartops config failed with %w", err)
	}

	projectId, err := configRoot.GetString("id")
	if err != nil {
		return nil, fmt.Errorf("project id is not a string: %w", err)
	}

	index, err := root.CreatePath("indexes")
	if err != nil {
		return nil, fmt.Errorf("creating path for indexes failed with %w", err)
	}

	for _, smartopId := range smartopConfig.Children() {
		tnsPath, err := specs.Tns().IndexValue(s.branch, projectId, appId, smartopId)
		if err != nil {
			return nil, fmt.Errorf("getting index value for smartop %s failed with %w", smartopId, err)
		}

		smartopObj, err := smartopConfig.Child(smartopId).Object()
		if err != nil {
			return nil, fmt.Errorf("fetching smartop object for %s failed with %w", smartopId, err)
		}

		name, err := smartopObj.GetString("name")
		if err != nil {
			return nil, fmt.Errorf("smartop name is not a string: %w", err)
		}

		// referencing wasm module
		wasmPath, err := specs.Tns().WasmModulePath(projectId, appId, name)
		if err != nil {
			return nil, fmt.Errorf("getting wasm module path for %s failed with %w", name, err)
		}

		wasmLinkPath := wasmPath.Versioning().Links().String()
		links, ok := index.Get(wasmLinkPath).([]string)
		if !ok {
			links = []string{}
		}

		if !slices.Contains(links, tnsPath.String()) {
			links = append(links, tnsPath.String())
		}

		index.Set(wasmLinkPath, links)

	}

	return config, nil
}
