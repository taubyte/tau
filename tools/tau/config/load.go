package config

import (
	"fmt"
	"os"
	"path/filepath"

	seer "github.com/taubyte/tau/pkg/yaseer"
	"github.com/taubyte/tau/tools/tau/constants"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/shared"
	"github.com/taubyte/tau/utils/fs/file"
)

func loadConfig() error {
	if err := openConfig(); err != nil {
		return fmt.Errorf("tau config file %s: %w (TAU_CONFIG_FILE must be a path this OS opens: on Windows C:\\..., not a Git Bash /c/... path)", constants.TauConfigFileName, err)
	}
	return nil
}

func openConfig() error {
	if !file.Exists(constants.TauConfigFileName) {
		f, err := os.Create(constants.TauConfigFileName)
		if err != nil {
			return singletonsI18n.CreatingConfigFileFailed(err)
		}
		f.Close()
	}

	_seer, err := seer.New(seer.SystemFS(filepath.Dir(constants.TauConfigFileName)))
	if err != nil {
		return singletonsI18n.CreatingSeerAtLocFailed(constants.TauConfigFileName, err)
	}

	_config = &tauConfig{
		root: _seer,
	}
	return nil
}
