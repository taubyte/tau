package flags

import "testing"

// Git Bash rewrites a leading "/" argument to the Git install dir.
func TestURLPath(t *testing.T) {
	for in, want := range map[string]string{
		"/fn1":                                      "/fn1",
		"C:/Program Files/Git/fn1":                  "/fn1",
		"C:/Program Files/Git/":                     "/",
		"C:/Program Files/Git":                      "/",
		`c:\Program Files\Git\a\b`:                  "/a/b",
		"D:/Tools/Git/api/git/x":                    "/api/git/x",
		"C:/Users/me/AppData/Local/Programs/Git/v1": "/v1",
		"C:/data/file":                              "C:/data/file",
		"api":                                       "api",
	} {
		if got := URLPath(in); got != want {
			t.Errorf("URLPath(%q) = %q, want %q", in, got, want)
		}
	}
}
