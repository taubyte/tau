package fixtures

import (
	"errors"

	"github.com/taubyte/tau/dream"
	spec "github.com/taubyte/tau/pkg/specs/common"
)

func setBranch(u *dream.Universe, args ...interface{}) error {
	if len(args) < 1 {
		return errors.New("arguments required for fixture `branch`")
	}

	branch, ok := args[0].(string)
	if !ok {
		return errors.New("expected branch argument to be string")
	}

	spec.DefaultBranches = []string{branch}
	return nil
}
