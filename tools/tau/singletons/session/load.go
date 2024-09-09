package session

import (
	"os"
	"path"

	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/cli/common"
	"github.com/taubyte/tau/tools/tau/constants"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/singletons"
	"github.com/taubyte/utils/fs/file"
)

func loadSession() (err error) {
	loc, isSet := os.LookupEnv(constants.TauSessionLocationEnvVarName)
	if !isSet {
		loc, err = discoverOrCreateConfigFileLoc()
		if err != nil {
			return singletonsI18n.SessionCreateFailed(loc, err)
		}
	} else {
		sessionFileLoc := path.Join(loc, sessionFileName+".yaml")

		if !file.Exists(sessionFileLoc) {
			err = os.MkdirAll(loc, common.DefaultDirPermission)
			if err != nil {
				return singletonsI18n.CreatingSessionFileFailed(err)
			}

			err = os.WriteFile(sessionFileLoc, nil, common.DefaultFilePermission)
			if err != nil {
				return singletonsI18n.CreatingSessionFileFailed(err)
			}

		}
	}

	return LoadSessionInDir(loc)
}

// Used in tests for confirming values were set
func LoadSessionInDir(loc string) error {
	if len(loc) == 0 {
		return singletonsI18n.SessionFileLocationEmpty()
	}

	_seer, err := seer.New(seer.SystemFS(loc))
	if err != nil {
		return singletonsI18n.CreatingSeerAtLocFailed(loc, err)
	}

	_session = &tauSession{
		root: _seer,
	}

	return nil
}
