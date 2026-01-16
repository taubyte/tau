package pass4

import (
	"fmt"
	"slices"

	specs "github.com/taubyte/tau/pkg/specs/function"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type functions struct {
	branch string
}

func Functions(branch string) transform.Transformer[object.Refrence] {
	return &functions{branch: branch}
}

func (f *functions) Process(ct transform.Context[object.Refrence], config object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
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

	funcConfig, err := config.Child(string(specs.PathVariable)).Object()
	if err == object.ErrNotExist {
		return config, nil
	} else if err != nil {
		return nil, fmt.Errorf("fetching function config failed with %w", err)
	}

	projectId, err := configRoot.GetString("id")
	if err != nil {
		return nil, fmt.Errorf("project id is not a string: %w", err)
	}

	index, err := root.CreatePath("indexes")
	if err != nil {
		return nil, fmt.Errorf("creating path for indexes failed with %w", err)
	}

	for _, funcId := range funcConfig.Children() {
		tnsPath, err := specs.Tns().IndexValue(f.branch, projectId, appId, funcId)
		if err != nil {
			return nil, fmt.Errorf("getting index value for function %s failed with %w", funcId, err)
		}

		funObj, err := funcConfig.Child(funcId).Object()
		if err != nil {
			return nil, fmt.Errorf("fetching function object for %s failed with %w", funcId, err)
		}

		name, err := funObj.GetString("name")
		if err != nil {
			return nil, fmt.Errorf("function name is not a string: %w", err)
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

		// referencing domains

		secondaryDomains, _ := configRoot.Child("domains").Object() // equals global domains when in an app
		primaryDomains, _ := config.Child("domains").Object()       // equals apps domains when in an app
		if primaryDomains == nil {                                  // if app has no domains, use global domains
			primaryDomains = secondaryDomains
		}
		if primaryDomains == secondaryDomains { // if we're in project-level, use global domains
			secondaryDomains = nil
		}

		domainsVal := funObj.Get("domains")
		domains, ok := domainsVal.([]string)
		if !ok && domainsVal != nil {
			return nil, fmt.Errorf("domains is not a []string")
		}
		if domains == nil {
			domains = []string{}
		}
		for _, domainId := range domains {
			domainObj, err := primaryDomains.Child(domainId).Object()
			if err != nil {
				if secondaryDomains != nil {
					domainObj, err = secondaryDomains.Child(domainId).Object()
					if err != nil {
						return nil, fmt.Errorf("fetching domain object for %s failed with %w", domainId, err)
					}
				} else {
					return nil, fmt.Errorf("fetching domain object for %s failed with %w", domainId, err)
				}
			}

			fqdnStr, err := domainObj.GetString("fqdn")
			if err != nil {
				return nil, fmt.Errorf("fqdn is not a string for domain %s: %w", domainId, err)
			}
			domainPath, err := specs.Tns().HttpPath(fqdnStr)
			if err != nil {
				return nil, fmt.Errorf("getting HTTP path for domain %s failed with %w", domainId, err)
			}

			domainLinkPath := domainPath.Versioning().Links().String()
			links, ok := index.Get(domainLinkPath).([]string)
			if !ok {
				links = []string{}
			}

			if !slices.Contains(links, tnsPath.String()) {
				links = append(links, tnsPath.String())
			}

			index.Set(domainLinkPath, links)
		}
	}

	return config, nil
}
