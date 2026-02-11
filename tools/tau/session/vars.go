package session

var (
	_session                   *tauSession
	_sessionDir                string // directory containing the current session file (seer root)
	_sessionDocName            string // base name of current session file without .yaml (e.g. "tau-session-ts-p0-..." or "session" for tests)
	sessionDirPrefix           = "tau"
	sessionFileName            = "session" // default doc name for tests (session.yaml)
	sessionAncestorDepth       = 6
	maxAncestorDepthForPath    = 20   // max ancestors to collect for root-first path (used for LCP discovery)
	sessionCommonRootThreshold = 2    // min LCP length to reuse an existing session
	sessionTempDirOverride     string // if set (e.g. in tests to t.TempDir()), used instead of os.TempDir()
	sessionRootDirOverride     string // if set (e.g. in tests), single folder that contains session YAML files; else sessionBaseDir()/tau
)

func Clear() {
	_session = nil
	_sessionDir = ""
	_sessionDocName = ""
}
