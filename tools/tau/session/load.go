package session

import (
	"path/filepath"
	"strings"

	seer "github.com/taubyte/tau/pkg/yaseer"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/shared"
)

func loadSession() (err error) {
	debugSession("loadSession: calling discoverOrCreateConfigFileLoc")
	filePath, err := discoverOrCreateConfigFileLoc()
	if err != nil {
		debugSession("loadSession: discover failed path=%q err=%v", filePath, err)
		return singletonsI18n.SessionCreateFailed(filePath, err)
	}
	debugSession("loadSession: loading session file=%q", filePath)
	return LoadSessionAt(filePath)
}

// LoadSessionAt loads the session from a specific session YAML file (path in filename). Used by discovery.
func LoadSessionAt(filePath string) error {
	debugSession("LoadSessionAt: file=%q (len=%d)", filePath, len(filePath))
	if filePath == "" {
		return singletonsI18n.SessionFileLocationEmpty()
	}
	_sessionDir = filepath.Dir(filePath)
	_sessionDocName = strings.TrimSuffix(filepath.Base(filePath), ".yaml")
	if _sessionDocName == "" {
		_sessionDocName = sessionFileName
	}
	_seer, err := seer.New(seer.SystemFS(_sessionDir))
	if err != nil {
		debugSession("LoadSessionAt: seer.New err=%v", err)
		return singletonsI18n.CreatingSeerAtLocFailed(filePath, err)
	}
	_session = &tauSession{root: _seer}
	debugSession("LoadSessionAt: session loaded doc=%q dir=%q", _sessionDocName, _sessionDir)
	return nil
}

// LoadSessionInDir loads the session from a directory using the default "session" document (session.yaml). Used in tests.
func LoadSessionInDir(loc string) error {
	debugSession("LoadSessionInDir: dir=%q (len=%d)", loc, len(loc))
	if len(loc) == 0 {
		return singletonsI18n.SessionFileLocationEmpty()
	}
	_sessionDir = loc
	_sessionDocName = sessionFileName
	_seer, err := seer.New(seer.SystemFS(loc))
	if err != nil {
		debugSession("LoadSessionInDir: seer.New err=%v", err)
		return singletonsI18n.CreatingSeerAtLocFailed(loc, err)
	}
	_session = &tauSession{root: _seer}
	debugSession("LoadSessionInDir: session loaded for %q", loc)
	return nil
}
