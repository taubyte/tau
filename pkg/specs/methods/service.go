package methods

import (
	"errors"

	"github.com/taubyte/tau/pkg/specs/common"
)

func ServicePath(projectId, appId, serviceId, command string) (*common.TnsPath, error) {
	if len(projectId) == 0 {
		return nil, errors.New("project id is required for creating a Wasm path")
	}

	var path *common.TnsPath
	if len(appId) == 0 {
		path = common.NewTnsPath([]string{"project", projectId, "app", appId, "service", serviceId, "command", command})
	} else {
		path = common.NewTnsPath([]string{"project", projectId, "service", serviceId, "command", command})
	}

	return path, nil
}
