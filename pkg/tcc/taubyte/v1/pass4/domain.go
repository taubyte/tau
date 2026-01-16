package pass4

import (
	"fmt"

	specs "github.com/taubyte/tau/pkg/specs/domain"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type domains struct {
	branch string
}

func Domains(branch string) transform.Transformer[object.Refrence] {
	return &domains{branch: branch}
}

func (d *domains) Process(ct transform.Context[object.Refrence], config object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
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

	// Extract project ID
	projectId, err := configRoot.GetString("id")
	if err != nil {
		return nil, fmt.Errorf("project id is not a string: %w", err)
	}

	// Extract app name if in application context
	appId := ""
	if configRoot != config {
		appsObj, err := configRoot.Child("applications").Object()
		if err == nil {
			appId = appsObj.Child(config).Name()
		}
	}

	domainConfig, err := config.Child(string(specs.PathVariable)).Object()
	if err == object.ErrNotExist {
		return config, nil
	} else if err != nil {
		return nil, fmt.Errorf("fetching domain config failed with %w", err)
	}

	index, err := root.CreatePath("indexes")
	if err != nil {
		return nil, fmt.Errorf("creating path for indexes failed with %w", err)
	}

	// Get validations store
	validationsStore := ct.Store().Validators()
	validations := validationsStore.Get()

	for _, domainId := range domainConfig.Children() {
		domainObj, err := domainConfig.Child(domainId).Object()
		if err != nil {
			return nil, fmt.Errorf("fetching domain object for %s failed with %w", domainId, err)
		}

		fqdn, err := domainObj.GetString("fqdn")
		if err != nil {
			return nil, fmt.Errorf("domain fqdn is not a string: %w", err)
		}

		// Build context map
		context := map[string]interface{}{
			"project": projectId,
		}
		if appId != "" {
			context["app"] = appId
		}

		// Emit validation request
		validations = append(validations, engine.NewNextValidation(
			"domain",
			fqdn,
			"dns",
			context,
		))

		// referencing wasm module
		indexPath, err := specs.Tns().BasicPath(fqdn)
		if err != nil {
			return nil, fmt.Errorf("getting basic path for domain %s failed with %w", domainId, err)
		}

		indexPathLinks := indexPath.Versioning().Links().String()

		// compat with config-compiler, which sets domains index to nil if no entry exists
		if index.Get(indexPathLinks) == nil {
			index.Set(indexPathLinks, nil)
		}
	}

	// Store validations back
	_, err = validationsStore.Set(validations)
	if err != nil {
		return nil, fmt.Errorf("storing validations failed with %w", err)
	}

	return config, nil
}
