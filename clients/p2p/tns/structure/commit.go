package structure

import (
	"fmt"

	"github.com/taubyte/tau/pkg/specs/common"
)

func (c *Structure[T]) Commit(projectId, branch string) (string, error) {
	commitObj, err := c.tns.Fetch(common.Current(projectId, branch))
	if err != nil {
		return "", err
	}

	iface := commitObj.Interface()
	commit, ok := iface.(string)
	if !ok {
		return "", fmt.Errorf("Commit not found for %s/%s: %v", branch, projectId, iface)
	}

	return commit, nil
}
