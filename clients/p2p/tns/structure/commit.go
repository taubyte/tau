package structure

import (
	"fmt"

	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/pkg/specs/common"
)

func (c *Structure[T]) Commit(projectId string, branches ...string) (commit, branch string, err error) {
	var (
		commitObj tns.Object
		ok        bool
	)

	for _, b := range branches {
		commitObj, err = c.tns.Fetch(common.Current(projectId, b))
		if err != nil {
			continue
		}

		iface := commitObj.Interface()
		if commit, ok = iface.(string); ok {
			branch = b
			break
		}
	}

	if commit == "" {
		err = fmt.Errorf("commit not found for %s in %v", projectId, branches)
	}

	return
}
