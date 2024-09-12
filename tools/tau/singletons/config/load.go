package config

import (
	"os"
	"path/filepath"

	seer "github.com/taubyte/go-seer"
	"github.com/taubyte/tau/tools/tau/constants"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/singletons"
	"github.com/taubyte/utils/fs/file"
)

func loadConfig() error {
	if !file.Exists(constants.TauConfigFileName) {
		_, err := os.Create(constants.TauConfigFileName)
		if err != nil {
			return singletonsI18n.CreatingConfigFileFailed(err)
		}
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
