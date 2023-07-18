package structure

import (
	"fmt"

	"github.com/taubyte/go-specs/common"
)

func (c *Structure[T]) Commit(projectId, branch string) (string, error) {
	commitObj, err := c.tns.Fetch(common.Current(projectId, branch))
	if err != nil {
		return "", err
	}

	iface := commitObj.Interface()
	commit, ok := iface.(string)
	if ok == false {
		return "", fmt.Errorf("Commit not found for %s/%s: %v", branch, projectId, iface)
	}

	return commit, nil
}
