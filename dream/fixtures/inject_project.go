package fixtures

import (
	"fmt"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/dream"
)

// injectProject compiles and publishes a project using TCC
// Expects params: (afero.Fs, configPath string) where:
//   - fs: the filesystem containing the project (from tcc.GenerateProject)
//   - configPath: optional path to config root within fs (defaults to "/")
func injectProject(u *dream.Universe, params ...interface{}) error {
	simple, err := u.Simple("client")
	if err != nil {
		return fmt.Errorf("failed getting simple with error: %v", err)
	}

	err = simple.Provides("tns")
	if err != nil {
		return err
	}

	if len(params) < 1 {
		return fmt.Errorf("injectProject requires at least 1 parameter (afero.Fs)")
	}

	fs, ok := params[0].(afero.Fs)
	if !ok {
		return fmt.Errorf("param 0 not a valid afero.Fs, got %T", params[0])
	}

	// Default config path is "/" since GenerateProject uses VirtualFS(fs, "/")
	configPath := "/"
	if len(params) >= 2 {
		if cp, ok := params[1].(string); ok {
			configPath = cp
		}
	}

	return inject(fs, configPath, simple)
}
