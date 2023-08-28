package cache

import (
	"fmt"

	iface "github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/go-specs/methods"
)

func computeServiceableCid(serviceable iface.Serviceable, projectCid, branch string) (string, error) {
	if len(projectCid) < 1 {
		project, err := serviceable.Project()
		if err != nil {
			return "", fmt.Errorf("getting project id failed with: %w", err)
		}

		projectCid = project.String()
	}

	assetPath, err := methods.GetTNSAssetPath(projectCid, serviceable.Id(), branch)
	if err != nil {
		return "", fmt.Errorf("getting tns asset path failed with: %w", err)
	}

	cidObj, err := serviceable.Service().Tns().Fetch(assetPath)
	if err != nil {
		return "", fmt.Errorf("fetching cid object failed with: %w", err)
	}

	cid, ok := cidObj.Interface().(string)
	if !ok {
		return "", fmt.Errorf("cid %#v is not a string", cidObj)
	}

	return cid, nil
}
