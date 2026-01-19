package fixtures

import (
	"fmt"

	"github.com/taubyte/tau/dream"
	testFixtures "github.com/taubyte/tau/pkg/tcc/taubyte/v1/fixtures"
)

func fakeProject(u *dream.Universe, params ...interface{}) error {
	simple, err := u.Simple("client")
	if err != nil {
		return fmt.Errorf("failed getting simple with error: %v", err)
	}

	err = simple.Provides("tns")
	if err != nil {
		return err
	}

	fs, err := testFixtures.VirtualFSWithBuiltProject()
	if err != nil {
		return fmt.Errorf("getting virtual FS failed with: %w", err)
	}

	return injectWithFS(fs, "/test_project/config", "main", "testCommit", simple)
}
