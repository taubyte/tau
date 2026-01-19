package pass4

import (
	"fmt"
	"slices"

	"github.com/taubyte/tau/core/common"
	specs "github.com/taubyte/tau/pkg/specs/library"
	"github.com/taubyte/tau/pkg/specs/methods"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type libraries struct {
	branch string
}

func Libraries(branch string) transform.Transformer[object.Refrence] {
	return &libraries{branch: branch}
}

func (l *libraries) Process(ct transform.Context[object.Refrence], config object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
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

	libraryConfig, err := config.Child(string(specs.PathVariable)).Object()
	if err == object.ErrNotExist {
		return config, nil
	} else if err != nil {
		return nil, fmt.Errorf("fetching library config failed with %w", err)
	}

	projectId, err := configRoot.GetString("id")
	if err != nil {
		return nil, fmt.Errorf("project id is not a string: %w", err)
	}

	index, err := root.CreatePath("indexes")
	if err != nil {
		return nil, fmt.Errorf("creating path for indexes failed with %w", err)
	}

	for _, libraryId := range libraryConfig.Children() {
		tnsPath, err := specs.Tns().IndexValue(l.branch, projectId, appId, libraryId)
		if err != nil {
			return nil, fmt.Errorf("getting index value for library %s failed with %w", libraryId, err)
		}

		libraryObj, err := libraryConfig.Child(libraryId).Object()
		if err != nil {
			return nil, fmt.Errorf("fetching library object for %s failed with %w", libraryId, err)
		}

		gitProvider, err := libraryObj.GetString("provider")
		if err != nil {
			return nil, fmt.Errorf("git provider is not a string: %w", err)
		}

		githubId, err := libraryObj.GetString("repository-id")
		if err != nil {
			return nil, fmt.Errorf("git repository is not a string: %w", err)
		}

		repoPath, err := methods.GetRepositoryPath(gitProvider, githubId, projectId)
		if err != nil {
			return nil, fmt.Errorf("getting repository path for %s failed with %w", githubId, err)
		}

		name, err := libraryObj.GetString("name")
		if err != nil {
			return nil, fmt.Errorf("library name is not a string: %w", err)
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
		index.Set(repoPath.Type().String(), common.LibraryRepository)
		index.Set(repoPath.Resource(libraryId).String(), tnsPath.String())
		index.Set(specs.Tns().NameIndex(libraryId).String(), name)
	}

	return config, nil
}
