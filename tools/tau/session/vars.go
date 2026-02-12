package session

var (
	_session               *tauSession
	_sessionDir            string // directory containing the current session file (seer root)
	_sessionDocName        string // base name of current session file without .yaml (e.g. "tau-session-8-7-6-5-4-3-2-1" or "session" for tests)
	sessionDirPrefix       = "tau"
	sessionFileName        = "session" // default doc name for tests (session.yaml)
	sessionMaxAncestors    = 16        // max ancestors to collect for PID chain
	sessionTempDirOverride string
	sessionRootDirOverride string
)

func Clear() {
	_session = nil
	_sessionDir = ""
	_sessionDocName = ""
}
