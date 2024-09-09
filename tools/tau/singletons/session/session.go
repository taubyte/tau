package session

import (
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/singletons"

	// Importing to run the common initialization
	"github.com/taubyte/go-seer"
	_ "github.com/taubyte/tau/tools/tau/singletons/common"
)

func getOrCreateSession() *tauSession {
	if _session == nil {
		err := loadSession()
		if err != nil {
			panic(err)
		}
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
