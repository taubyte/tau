package testutil

import (
	"os"

	"github.com/taubyte/tau/tools/tau/session"
)

// ActivateAuthMockSession loads the session from sessionPath and sets TAUBYTE_AUTH_URL and SelectedCloud
// for the auth mock. Returns a cleanup that clears the session and restores env. Use with ActivateAuthMock for flow tests.
// The caller must ensure the same sessionPath is used when running the CLI (e.g. via RunCLIWithDir).
func ActivateAuthMockSession(sessionPath string) (cleanup func()) {
	if err := session.LoadSessionInDir(sessionPath); err != nil {
		return func() {}
	}
	oldAuthURL := os.Getenv("TAUBYTE_AUTH_URL")
	os.Setenv("TAUBYTE_AUTH_URL", AuthMockBaseURL)
	session.Set().SelectedCloud("test")
	return func() {
		session.Clear()
		if oldAuthURL == "" {
			os.Unsetenv("TAUBYTE_AUTH_URL")
		} else {
			os.Setenv("TAUBYTE_AUTH_URL", oldAuthURL)
		}
	}
}
