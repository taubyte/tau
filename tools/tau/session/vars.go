package session

var (
	_session               *tauSession
	_sessionDir            string // current session root dir (exact or suffix-matched); used to decide if we need to switch to exact on mutate
	sessionDirPrefix       = "tau"
	sessionFileName        = "session"
	sessionAncestorDepth   = 6
	sessionTempDirOverride string // if set (e.g. in tests to t.TempDir()), used instead of os.TempDir() for session dirs
)

func Clear() {
	_session = nil
	_sessionDir = ""
}
