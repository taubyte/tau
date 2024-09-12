package session

var (
	_session         *tauSession
	sessionDirPrefix = "tau"
	sessionFileName  = "session"
)

func Clear() {
	_session = nil
}
