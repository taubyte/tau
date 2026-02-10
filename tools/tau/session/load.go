package session

import (
	seer "github.com/taubyte/tau/pkg/yaseer"
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/shared"
)

func loadSession() (err error) {
	debugSession("loadSession: calling discoverOrCreateConfigFileLoc")
	loc, err := discoverOrCreateConfigFileLoc()
	if err != nil {
		debugSession("loadSession: discover failed loc=%q err=%v", loc, err)
		return singletonsI18n.SessionCreateFailed(loc, err)
	}
	debugSession("loadSession: loading session in dir=%q", loc)
	return LoadSessionInDir(loc)
}

// Used in tests for confirming values were set
func LoadSessionInDir(loc string) error {
	debugSession("LoadSessionInDir: loc=%q (len=%d)", loc, len(loc))
	if len(loc) == 0 {
		return singletonsI18n.SessionFileLocationEmpty()
	}

	_seer, err := seer.New(seer.SystemFS(loc))
	if err != nil {
		debugSession("LoadSessionInDir: seer.New err=%v", err)
		return singletonsI18n.CreatingSeerAtLocFailed(loc, err)
	}

	_session = &tauSession{
		root: _seer,
	}
	_sessionDir = loc
	debugSession("LoadSessionInDir: session loaded for %q", loc)
	return nil
}
