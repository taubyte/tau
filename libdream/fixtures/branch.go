package fixtures

import (
	"errors"

	spec "github.com/taubyte/go-specs/common"
	"github.com/taubyte/tau/libdream"
)

func setBranch(u *libdream.Universe, args ...interface{}) error {
	if len(args) < 1 {
		return errors.New("arguments required for fixture `branch`")
	}

	branch, ok := args[0].(string)
	if !ok {
		return errors.New("expected branch argument to be string")
	}

	spec.DefaultBranch = branch
	return nil
}
