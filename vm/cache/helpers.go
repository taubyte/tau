package cache

import (
	"fmt"

	iface "github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/go-specs/methods"
)

// TODO: This should return a cid.Cid
func ResolveAssetCid(serviceable iface.Serviceable, branch string) (string, error) {
	assetPath, err := methods.GetTNSAssetPath(serviceable.Project(), serviceable.Id(), branch)
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
