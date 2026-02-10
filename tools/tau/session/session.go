package session

import (
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/shared"

	// Importing to run the common initialization
	seer "github.com/taubyte/tau/pkg/yaseer"
)

func getOrCreateSession() *tauSession {
	if _session == nil {
		debugSession("getOrCreateSession: _session is nil, loading...")
		err := loadSession()
		if err != nil {
			debugSession("getOrCreateSession: loadSession err=%v", err)
			panic(err)
		}
		debugSession("getOrCreateSession: session ready")
	}
	return _session
}

func (s *tauSession) Document() *seer.Query {
	return _session.root.Get(sessionFileName).Document().Fork()
}

func (s *tauSession) keys() (values []string, err error) {
	values, err = _session.Document().List()
	if err != nil {
		err = singletonsI18n.SessionListFailed(err)
	}

	return
}
