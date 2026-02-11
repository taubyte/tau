package session

var (
	_session                     *tauSession
	_sessionDir                  string // directory containing the current session file (seer root)
	_sessionDocName              string // base name of current session file without .yaml (e.g. "tau-session-p0-p1-..." or "session" for tests)
	sessionDirPrefix             = "tau"
	sessionFileName              = "session" // default doc name for tests (session.yaml)
	sessionAncestorDepth         = 16
	maxAncestorDepthForPath      = 16   // max ancestors to collect (matches sessionAncestorDepth; they should agree)
	sessionCommonSuffixThreshold = 2    // min common suffix length (leaf-side match) to reuse an existing session
	sessionTempDirOverride       string // if set (e.g. in tests to t.TempDir()), used instead of os.TempDir()
	sessionRootDirOverride       string // if set (e.g. in tests), single folder that contains session YAML files; else sessionBaseDir()/tau
)

func Clear() {
	_session = nil
	_sessionDir = ""
	_sessionDocName = ""
}
