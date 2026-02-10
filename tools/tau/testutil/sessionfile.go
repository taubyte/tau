package testutil

import (
	"github.com/taubyte/tau/tools/tau/session"
)

// ActivateAuthMockSession loads the session from sessionPath and sets AuthURL and SelectedCloud
// for the auth mock. Returns a cleanup that clears the session. Use with ActivateAuthMock for flow tests.
// The caller must ensure the same sessionPath is used when running the CLI (e.g. via RunCLIWithDir).
func ActivateAuthMockSession(sessionPath string) (cleanup func()) {
	if err := session.LoadSessionInDir(sessionPath); err != nil {
		return func() {}
	}
	session.Set().AuthURL(AuthMockBaseURL)
	session.Set().SelectedCloud("test")
	return func() {
		session.Clear()
	}
}
