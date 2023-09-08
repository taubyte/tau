package fixtures

import (
	"fmt"

	"github.com/taubyte/config-compiler/fixtures"
	"github.com/taubyte/tau/libdream"
)

func fakeProject(u *libdream.Universe, params ...interface{}) error {
	simple, err := u.Simple("client")
	if err != nil {
		return fmt.Errorf("failed getting simple with error: %v", err)
	}

	err = simple.Provides("tns")
	if err != nil {
		return err
	}

	project, err := fixtures.Project()
	if err != nil {
		return err
	}

	return inject(project, simple)
}
