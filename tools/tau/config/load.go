package config

import (
	"os"
	"path/filepath"

	seer "github.com/taubyte/tau/pkg/yaseer"
	"github.com/taubyte/tau/tools/tau/constants"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/shared"
	"github.com/taubyte/tau/utils/fs/file"
)

func loadConfig() error {
	if !file.Exists(constants.TauConfigFileName) {
		// THE DIRECTORY FIRST, because the config now lives in one. os.Create
		// does not make parents, and a first run on a machine with no ~/.tau
		// would have failed with the same ENOENT the old path failed with as
		// EISDIR — a CLI that cannot start until somebody makes a directory
		// for it by hand.
		if err := os.MkdirAll(filepath.Dir(constants.TauConfigFileName), 0o700); err != nil {
			return singletonsI18n.CreatingConfigFileFailed(err)
		}
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
