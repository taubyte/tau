package pass4

import (
	"fmt"
	"slices"

	specs "github.com/taubyte/tau/pkg/specs/messaging"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type messaging struct {
	branch string
}

func Messaging(branch string) transform.Transformer[object.Refrence] {
	return &messaging{branch: branch}
}

func (m *messaging) Process(ct transform.Context[object.Refrence], config object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
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

	messagingConfig, err := config.Child(string(specs.PathVariable)).Object()
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

	for _, messagingId := range messagingConfig.Children() {
		tnsPath, err := specs.Tns().IndexValue(m.branch, projectId, appId, messagingId)
		if err != nil {
			return nil, fmt.Errorf("getting index value for messaging %s failed with %w", messagingId, err)
		}

		indexPath, err := specs.Tns().WebSocketHashPath(projectId, appId)
		if err != nil {
			return nil, fmt.Errorf("getting index value for messaging %s failed with %w", messagingId, err)
		}

		indexPathLinks := indexPath.String()
		links, ok := index.Get(indexPathLinks).([]string)
		if !ok {
			links = []string{}
		}

		if !slices.Contains(links, tnsPath.String()) {
			links = append(links, tnsPath.String())
		}

		index.Set(indexPathLinks, links)

	}

	return config, nil
}
